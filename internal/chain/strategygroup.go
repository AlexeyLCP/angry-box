package chain

// strategygroup.go — renders the sing-box group outbound that distributes
// traffic across a multi-node chain level (v2 levels model), and the chain
// topology validation that goes with it.
//
// Strategy mapping (model.Strategy → sing-box outbound):
//   - "" / fallback → the patched sing-box-extended "fallback" outbound
//     (per-connection round-robin + blacklist_timeout) — the DEFAULT and the
//     production-verified path (dns.idoctor.mom runs it in prod).
//   - urltest → native urltest group (gstatic 204 probes). Explicit opt-in
//     only: probing through transit hops is flaky (see the merged_config.go
//     note), so it is never the default.
//   - failover → urltest with a tight interval and tolerance 0 (sing-box has
//     no dedicated failover type; this is the documented approximation).
//   - selector → native selector group pinned to the first member (manual /
//     API switching later; deterministic default now).

import (
	"encoding/json"
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// urlTestProbeURL is the connectivity probe urltest groups use.
const urlTestProbeURL = "https://www.gstatic.com/generate_204"

// buildStrategyGroupOutbound wraps member outbound tags in a group outbound
// per the level strategy. members must be non-empty and in a stable order
// (selector pins Default to the first member).
func buildStrategyGroupOutbound(strategy model.Strategy, tag string, members []string) (json.RawMessage, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("strategy group %q: no members", tag)
	}
	switch strategy {
	case "", model.StrategyFallback:
		return json.Marshal(config.FallbackOutbound{
			Type:             "fallback",
			Tag:              tag,
			Outbounds:        members,
			BlacklistTimeout: "30s",
		})
	case model.StrategyURLTest:
		return json.Marshal(config.StrategyOutbound{
			Type:      "urltest",
			Tag:       tag,
			Outbounds: members,
			URL:       urlTestProbeURL,
			Interval:  "3m",
			Tolerance: 50,
		})
	case model.StrategyFailover:
		// sing-box has no dedicated failover type; urltest with a tight
		// interval and zero tolerance is the documented approximation (probes
		// pick the first healthy member; zero tolerance switches as soon as
		// the current one degrades).
		return json.Marshal(config.StrategyOutbound{
			Type:      "urltest",
			Tag:       tag,
			Outbounds: members,
			URL:       urlTestProbeURL,
			Interval:  "1m",
			Tolerance: 0,
		})
	case model.StrategySelector:
		return json.Marshal(config.StrategyOutbound{
			Type:      "selector",
			Tag:       tag,
			Outbounds: members,
			Default:   members[0],
		})
	default:
		return nil, fmt.Errorf("strategy group %q: unsupported strategy %q (want fallback/urltest/failover/selector)", tag, strategy)
	}
}

// levelGroupTag is the tag of the strategy group wrapping the outbounds from
// the level containing nodeID toward the downstream level (index downstream
// = levelIndex+1). Referenced by route rules and the AWG TUN overlay forward.
func levelGroupTag(chainName string, downstreamLevel int) string {
	return fmt.Sprintf("ch-%s-grp-l%d", chainName, downstreamLevel)
}

// effectiveGroupStrategy resolves the strategy for a group: the level's own
// strategy, defaulting to StrategyFallback (round-robin — the verified path).
func effectiveGroupStrategy(s model.Strategy) model.Strategy {
	if s == "" {
		return model.StrategyFallback
	}
	return s
}

// ValidateChainTopology rejects levelized chains the render layer cannot
// express. Loud failures here (before any SSH work) instead of a silently
// misrendered config on the node:
//   - every level must hold at least one node;
//   - AWG inter-node transport requires SINGLE-NODE levels (the userspace WG
//     transit endpoint is point-to-point; multi-peer endpoints for grouped
//     levels are a follow-up — the AWG multi-exit kernel balancer is a
//     separate mechanism and unaffected).
//
// Exported as ValidateChainTopology for the web layer (chain form validation);
// validateChainTopology is the package-internal alias.
func ValidateChainTopology(c *model.Chain) error {
	if !c.IsLevelized() {
		return nil
	}
	for i, lv := range c.Levels {
		if len(lv.Nodes) == 0 {
			return fmt.Errorf("chain %q: level %d is empty", c.Name, i)
		}
	}
	if c.Transport == model.TransportAWG {
		for i, lv := range c.Levels {
			if len(lv.Nodes) != 1 {
				return fmt.Errorf("chain %q: AWG inter-node transport requires single-node levels (level %d has %d nodes); use XHTTP/Reality for grouped levels, or the AWG exit balancer for multi-exit", c.Name, i, len(lv.Nodes))
			}
		}
	}
	return nil
}

// validateChainTopology is the package-internal alias of ValidateChainTopology.
func validateChainTopology(c *model.Chain) error { return ValidateChainTopology(c) }
