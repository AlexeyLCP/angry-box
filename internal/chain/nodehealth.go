package chain

// nodehealth.go — pure per-node health state machine computed from the
// SSH+systemd probe collected each metrics tick by collectAllMetrics
// (internal/web/server.go). Mirrors the model.User.ComputeStatus() pattern
// (P0b): a single pure function is the source of truth, called from the loop.
//
// States (model.NodeState*): healthy → suspect → down → unreachable, plus the
// operator-marked NodeStateBlocked. "Blocked by DPI" is NOT auto-detected —
// the orchestrator SSHes from a free region and cannot observe a DPI block
// (AGENTS.md / P1a). NodeStateBlocked is sticky: probe outcomes never clear
// it; only the unblock handler (web/nodes.go) does, resetting to Unknown so
// the next tick reclassifies from live signals.
//
// Hysteresis (HysteresisConfig): a single transient SSH timeout must NOT flip
// a node to down/unreachable (which would spam audit + flap the badge). We
// require DownThreshold consecutive fails before leaving healthy/suspect, and
// RecoverThreshold consecutive oks before leaving down/unreachable. Defaults
// (3 / 2) at the 15-min metrics interval ≈ 45 min to flag, 30 min to recover.
//
// This file has no I/O and no store dependency so the transition table is
// unit-testable in isolation (nodehealth_test.go). The metrics loop calls
// classifyProbe + NextState; the operator handlers call SetNodeState.

import (
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// ProbeOutcome is the raw result of one probe tick, derived from the backend
// GetStatus call (SSH dial + systemctl is-active sing-box). classifyProbe
// builds it from (err, *Status). The extension point for a future sentinel
// probe (P1a+) is here: a richer ProbeOutcome{Blocked bool} would let NextState
// auto-detect a DPI block from a censored-region vantage point.
type ProbeOutcome struct {
	SSHOK   bool   // err == nil from GetStatus (SSH dial succeeded)
	Running bool   // status.Running (sing-box systemd unit active). Only meaningful when SSHOK.
	Reason  string // human-readable cause: "ssh dial: ...", "sing-box inactive", "" (healthy)
}

// ClassifyProbe maps a GetStatus result into a ProbeOutcome. err != nil → SSH
// unreachable; err == nil + !Running → service down; err == nil + Running →
// healthy. Reason is the short first-line of the error (capped) so the audit
// payload stays readable. status may be nil when err != nil.
//
// Exported because the metrics loop (internal/web/server.go) calls it; the
// rest of nodehealth.go stays pure for unit testing.
func ClassifyProbe(err error, status *model.Status) ProbeOutcome {
	if err != nil {
		return ProbeOutcome{SSHOK: false, Reason: "ssh dial: " + shortErr(err)}
	}
	if status == nil || !status.Running {
		return ProbeOutcome{SSHOK: true, Running: false, Reason: "sing-box inactive"}
	}
	return ProbeOutcome{SSHOK: true, Running: true}
}

// shortErr returns the first non-empty line of err.Error(), trimmed and capped
// to ~80 chars, so a multi-line SSH error does not bloat the audit payload.
func shortErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const cap = 80
	if len(s) > cap {
		s = s[:cap]
	}
	return s
}

// NextState advances the node state machine by one probe tick, mutating m in
// place (State/StateReason/StateChangedAt/counters/Online). Returns changed ==
// true when m.State differs from its value before the call. Operator-marked
// NodeStateBlocked is sticky — a probe never clears it; only SetNodeState
// (via the unblock handler) does, so a blocked node that also goes SSH-dead
// stays blocked (the operator still sees the block, not a transient "down").
//
// Transition table (cfg.DownThreshold=D, cfg.RecoverThreshold=R):
//
//	current       | probe        | fails      | oks       | next
//	--------------+--------------+------------+-----------+-----------
//	healthy       | ok           | 0          | —         | healthy
//	healthy       | fail (SSH)   | 1..D-1     | —         | suspect
//	healthy       | fail (SSH)   | >=D        | —         | unreachable
//	healthy       | fail (sysd)  | >=D        | —         | down
//	suspect       | ok           | reset      | 1..R-1    | suspect
//	suspect       | ok           | reset      | >=R       | healthy
//	suspect       | fail (SSH)   | >=D        | —         | unreachable
//	suspect       | fail (sysd)  | >=D        | —         | down
//	down/unreach. | ok           | reset      | 1..R-1    | stays (recovering)
//	down/unreach. | ok           | reset      | >=R       | healthy
//	down/unreach. | fail         | ++ (capped)| —         | stays
//	blocked       | any          | —          | —         | blocked (sticky)
//	unknown       | ok          | 0          | >=1       | healthy
//	unknown       | fail         | >=D        | —         | unreachable/down
func NextState(m *model.NodeMetrics, probe ProbeOutcome, cfg model.HysteresisConfig) bool {
	if m == nil {
		return false
	}
	if cfg.DownThreshold <= 0 {
		cfg.DownThreshold = model.DefaultHysteresis.DownThreshold
	}
	if cfg.RecoverThreshold <= 0 {
		cfg.RecoverThreshold = model.DefaultHysteresis.RecoverThreshold
	}

	prev := m.State
	// Blocked is sticky — probes never change it. The operator clears it via
	// SetNodeState(NodeStateUnknown) in the unblock handler, after which the
	// next tick reclassifies from live signals.
	if prev == model.NodeStateBlocked {
		return false
	}

	healthy := probe.SSHOK && probe.Running

	switch {
	case healthy:
		m.ConsecutiveFails = 0
		m.ConsecutiveOKs++
		switch prev {
		case model.NodeStateHealthy, "":
			// already healthy (or old store with empty State) — nothing to do
		case model.NodeStateUnknown:
			// first good probe clears unknown → healthy immediately
		default:
			// suspect/down/unreachable — recover only after R consecutive oks
			if m.ConsecutiveOKs < cfg.RecoverThreshold {
				return false // still recovering, hold current state
			}
		}
		transition(m, model.NodeStateHealthy, "")

	case !probe.SSHOK:
		// SSH dial failed → host/network unreachable (not a service crash).
		m.ConsecutiveOKs = 0
		m.ConsecutiveFails++
		switch prev {
		case model.NodeStateUnreachable:
			return false // already unreachable, keep counting (capped below)
		case model.NodeStateDown:
			// was down (service), now also unreachable — escalate
			transition(m, model.NodeStateUnreachable, probe.Reason)
			return true
		case model.NodeStateUnknown, "":
			if m.ConsecutiveFails < cfg.DownThreshold {
				// unknown + a few fails → hold unknown (don't pre-judge a fresh node)
				if prev == "" {
					transition(m, model.NodeStateSuspect, probe.Reason)
				}
				return prev != m.State
			}
		default: // healthy, suspect
			if m.ConsecutiveFails < cfg.DownThreshold {
				if prev != model.NodeStateSuspect {
					transition(m, model.NodeStateSuspect, probe.Reason)
				}
				return prev != m.State
			}
		}
		transition(m, model.NodeStateUnreachable, probe.Reason)

	default: // SSH ok but sing-box not running → service down
		m.ConsecutiveOKs = 0
		m.ConsecutiveFails++
		switch prev {
		case model.NodeStateDown:
			return false // already down
		case model.NodeStateUnreachable:
			// was unreachable (SSH dead), now SSH ok but service down — de-escalate to down
			transition(m, model.NodeStateDown, probe.Reason)
			return true
		case model.NodeStateUnknown, "":
			if m.ConsecutiveFails < cfg.DownThreshold {
				if prev == "" {
					transition(m, model.NodeStateSuspect, probe.Reason)
				}
				return prev != m.State
			}
		default: // healthy, suspect
			if m.ConsecutiveFails < cfg.DownThreshold {
				if prev != model.NodeStateSuspect {
					transition(m, model.NodeStateSuspect, probe.Reason)
				}
				return prev != m.State
			}
		}
		transition(m, model.NodeStateDown, probe.Reason)
	}

	// Cap the counters so a long outage does not grow them without bound.
	if m.ConsecutiveFails > 1000 {
		m.ConsecutiveFails = 1000
	}
	if m.ConsecutiveOKs > 1000 {
		m.ConsecutiveOKs = 1000
	}
	return prev != m.State
}

// transition sets m.State/StateReason/StateChangedAt to the new values. It is
// only called when the state is actually changing; callers gate on that.
func transition(m *model.NodeMetrics, state, reason string) {
	m.State = state
	m.StateReason = reason
	m.StateChangedAt = timeNow()
}

// SetNodeState force-sets a node's state + reason + resets the hysteresis
// counters. Used by the operator mark/clear-blocked handlers (web/nodes.go)
// and by tests. For the blocked path this makes NodeStateBlocked sticky until
// the operator clears it; for the unblock path it resets to NodeStateUnknown so
// the next metrics tick reclassifies from live signals rather than inheriting
// stale counters.
func SetNodeState(m *model.NodeMetrics, state, reason string) {
	if m == nil {
		return
	}
	m.State = state
	m.StateReason = reason
	m.StateChangedAt = timeNow()
	m.ConsecutiveFails = 0
	m.ConsecutiveOKs = 0
}