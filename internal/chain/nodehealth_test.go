package chain

// nodehealth_test.go — pure unit tests for the per-node health state machine
// (nodehealth.go). No I/O, no store — just NextState transitions + counters.
// Mirrors the transition table documented in NextState's doc comment.

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// probe helpers — keep the table tests readable.
var (
	probeOK      = ProbeOutcome{SSHOK: true, Running: true}
	probeDown    = ProbeOutcome{SSHOK: true, Running: false, Reason: "sing-box inactive"}
	probeUnreach = ProbeOutcome{SSHOK: false, Reason: "ssh dial: timeout"}
)

func freshMetrics(state string) *model.NodeMetrics {
	return &model.NodeMetrics{HostID: "n1", State: state}
}

func assertState(t *testing.T, m *model.NodeMetrics, want string) {
	t.Helper()
	if m.State != want {
		t.Fatalf("state = %q, want %q (fails=%d oks=%d)", m.State, want, m.ConsecutiveFails, m.ConsecutiveOKs)
	}
}

func assertChanged(t *testing.T, changed, want bool) {
	t.Helper()
	if changed != want {
		t.Fatalf("changed = %v, want %v", changed, want)
	}
}

// TestNextState_HealthyStaysHealthy — healthy + ok → healthy, no transition.
func TestNextState_HealthyStaysHealthy(t *testing.T) {
	m := freshMetrics(model.NodeStateHealthy)
	changed := NextState(m, probeOK, model.DefaultHysteresis)
	assertChanged(t, changed, false)
	assertState(t, m, model.NodeStateHealthy)
	if m.ConsecutiveFails != 0 || m.ConsecutiveOKs != 1 {
		t.Fatalf("counters = (%d,%d), want (0,1)", m.ConsecutiveFails, m.ConsecutiveOKs)
	}
	if m.StateReason != "" {
		t.Fatalf("reason = %q, want empty", m.StateReason)
	}
}

// TestNextState_HealthyOneFailSuspect — 1 SSH fail from healthy → suspect
// (below DownThreshold=3, must not jump to unreachable).
func TestNextState_HealthyOneFailSuspect(t *testing.T) {
	m := freshMetrics(model.NodeStateHealthy)
	changed := NextState(m, probeUnreach, model.DefaultHysteresis)
	assertChanged(t, changed, true)
	assertState(t, m, model.NodeStateSuspect)
	if m.ConsecutiveFails != 1 {
		t.Fatalf("fails = %d, want 1", m.ConsecutiveFails)
	}
	if m.StateReason != "ssh dial: timeout" {
		t.Fatalf("reason = %q", m.StateReason)
	}
}

// TestNextState_HealthyThreeSSHFailUnreachable — 3 consecutive SSH fails from
// healthy → unreachable (not down: SSH dial fails, not systemd).
func TestNextState_HealthyThreeSSHFailUnreachable(t *testing.T) {
	m := freshMetrics(model.NodeStateHealthy)
	for i := 0; i < 3; i++ {
		NextState(m, probeUnreach, model.DefaultHysteresis)
	}
	assertState(t, m, model.NodeStateUnreachable)
	if m.ConsecutiveFails != 3 {
		t.Fatalf("fails = %d, want 3", m.ConsecutiveFails)
	}
}

// TestNextState_HealthyThreeSystemdFailDown — 3 consecutive systemd fails
// (SSH ok, not running) from healthy → down.
func TestNextState_HealthyThreeSystemdFailDown(t *testing.T) {
	m := freshMetrics(model.NodeStateHealthy)
	for i := 0; i < 3; i++ {
		NextState(m, probeDown, model.DefaultHysteresis)
	}
	assertState(t, m, model.NodeStateDown)
}

// TestNextState_SuspectOneOkStaysSuspect — suspect + 1 ok → still suspect
// (need RecoverThreshold=2 consecutive oks).
func TestNextState_SuspectOneOkStaysSuspect(t *testing.T) {
	m := freshMetrics(model.NodeStateSuspect)
	changed := NextState(m, probeOK, model.DefaultHysteresis)
	assertChanged(t, changed, false)
	assertState(t, m, model.NodeStateSuspect)
	if m.ConsecutiveOKs != 1 {
		t.Fatalf("oks = %d, want 1", m.ConsecutiveOKs)
	}
}

// TestNextState_SuspectTwoOkHealthy — suspect + 2 oks → healthy.
func TestNextState_SuspectTwoOkHealthy(t *testing.T) {
	m := freshMetrics(model.NodeStateSuspect)
	NextState(m, probeOK, model.DefaultHysteresis)
	changed := NextState(m, probeOK, model.DefaultHysteresis)
	assertChanged(t, changed, true)
	assertState(t, m, model.NodeStateHealthy)
}

// TestNextState_DownTwoOkHealthy — down + 2 oks → healthy (recovery).
func TestNextState_DownTwoOkHealthy(t *testing.T) {
	m := freshMetrics(model.NodeStateDown)
	NextState(m, probeOK, model.DefaultHysteresis)
	changed := NextState(m, probeOK, model.DefaultHysteresis)
	assertChanged(t, changed, true)
	assertState(t, m, model.NodeStateHealthy)
}

// TestNextState_DownOneOkOneFailStaysDown — recovery interrupted by a fail
// resets the ok counter and keeps the node down (no premature recovery).
func TestNextState_DownOneOkOneFailStaysDown(t *testing.T) {
	m := freshMetrics(model.NodeStateDown)
	NextState(m, probeOK, model.DefaultHysteresis) // oks=1, still down
	if m.ConsecutiveOKs != 1 {
		t.Fatalf("oks = %d, want 1 after one ok", m.ConsecutiveOKs)
	}
	changed := NextState(m, probeDown, model.DefaultHysteresis) // ok reset, fail++
	assertChanged(t, changed, false)
	assertState(t, m, model.NodeStateDown)
	if m.ConsecutiveOKs != 0 {
		t.Fatalf("oks = %d, want 0 after fail", m.ConsecutiveOKs)
	}
}

// TestNextState_BlockedStickyOnFail — blocked + any fail → stays blocked.
// Probes never clear a block; only the unblock handler does.
func TestNextState_BlockedStickyOnFail(t *testing.T) {
	m := freshMetrics(model.NodeStateBlocked)
	changed := NextState(m, probeUnreach, model.DefaultHysteresis)
	assertChanged(t, changed, false)
	assertState(t, m, model.NodeStateBlocked)
}

// TestNextState_BlockedStickyOnOk — blocked + ok → stays blocked (recovery
// does not auto-clear an operator-marked block).
func TestNextState_BlockedStickyOnOk(t *testing.T) {
	m := freshMetrics(model.NodeStateBlocked)
	changed := NextState(m, probeOK, model.DefaultHysteresis)
	assertChanged(t, changed, false)
	assertState(t, m, model.NodeStateBlocked)
}

// TestNextState_UnknownOkHealthy — unknown + ok → healthy immediately (first
// good probe classifies a fresh node, no hysteresis needed for clearance).
func TestNextState_UnknownOkHealthy(t *testing.T) {
	m := freshMetrics(model.NodeStateUnknown)
	changed := NextState(m, probeOK, model.DefaultHysteresis)
	assertChanged(t, changed, true)
	assertState(t, m, model.NodeStateHealthy)
}

// TestNextState_UnknownThreeFailDown — unknown + 3 systemd fails → down
// (a fresh node whose service never comes up is classified, not held forever).
func TestNextState_UnknownThreeFailDown(t *testing.T) {
	m := freshMetrics(model.NodeStateUnknown)
	for i := 0; i < 3; i++ {
		NextState(m, probeDown, model.DefaultHysteresis)
	}
	assertState(t, m, model.NodeStateDown)
}

// TestNextState_EmptyStateDerivesSuspect — old store with empty State + a fail
// → suspect (back-compat: empty State is treated like healthy for the first
// fail, not like unknown which would hold).
func TestNextState_EmptyStateDerivesSuspect(t *testing.T) {
	m := freshMetrics("")
	changed := NextState(m, probeUnreach, model.DefaultHysteresis)
	assertChanged(t, changed, true)
	assertState(t, m, model.NodeStateSuspect)
}

// TestSetNodeState_ResetsCounters — SetNodeState (operator block/unblock)
// sets the state + reason + zeroes both counters.
func TestSetNodeState_ResetsCounters(t *testing.T) {
	m := &model.NodeMetrics{HostID: "n1", State: model.NodeStateHealthy, ConsecutiveFails: 5, ConsecutiveOKs: 3}
	SetNodeState(m, model.NodeStateBlocked, "operator marked")
	assertState(t, m, model.NodeStateBlocked)
	if m.StateReason != "operator marked" {
		t.Fatalf("reason = %q", m.StateReason)
	}
	if m.ConsecutiveFails != 0 || m.ConsecutiveOKs != 0 {
		t.Fatalf("counters = (%d,%d), want (0,0)", m.ConsecutiveFails, m.ConsecutiveOKs)
	}
	if m.StateChangedAt.IsZero() {
		t.Fatal("StateChangedAt not set")
	}
}

// TestClassifyProbe — maps GetStatus outcomes to ProbeOutcome correctly,
// including the shortErr truncation for the audit payload.
func TestClassifyProbe(t *testing.T) {
	// healthy: SSH ok, running
	p := classifyProbe(nil, &model.Status{Running: true})
	if !p.SSHOK || !p.Running || p.Reason != "" {
		t.Fatalf("healthy probe = %+v", p)
	}
	// down: SSH ok, not running
	p = classifyProbe(nil, &model.Status{Running: false})
	if !p.SSHOK || p.Running || p.Reason != "sing-box inactive" {
		t.Fatalf("down probe = %+v", p)
	}
	// unreachable: err set, status nil
	p = classifyProbe(errFail("ssh dial: i/o timeout"), nil)
	if p.SSHOK || p.Running || p.Reason != "ssh dial: ssh dial: i/o timeout" {
		t.Fatalf("unreachable probe = %+v", p)
	}
}

// TestShortErr_TruncatesMultiLine — only the first line is kept + capped.
func TestShortErr_TruncatesMultiLine(t *testing.T) {
	long := "first line of doom\nsecond line\nthird"
	got := shortErr(errFail(long))
	if got != "first line of doom" {
		t.Fatalf("shortErr = %q, want %q", got, "first line of doom")
	}
	huge := strings.Repeat("x", 200)
	got = shortErr(errFail(huge))
	if len(got) > 80 {
		t.Fatalf("shortErr len = %d, want <= 80", len(got))
	}
}

type errFail string

func (e errFail) Error() string { return string(e) }