package chain

import (
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// ─── Utility dependency gating ───────────────────────────────────────────────
// Mirrors frozen.go's deny-list pattern, inverted: instead of blocking
// protocols outright, RequiredUtilities declares what a protocol/feature needs
// and ValidateUtilityDeps refuses the action until the node has it (the UI
// then offers to install the missing utilities).

// protocolUtilityDeps lists the utilities an inbound protocol requires.
// Protocols in this map also need TLSDomain (they cannot run on a bare IP).
// Protocols absent from the map keep legacy direct-port behaviour.
var protocolUtilityDeps = map[string][]string{
	// TLS-terminating protocols need the cert machinery + the router that
	// fronts them on 443.
	"naive":       {model.UtilityCaddy, model.UtilityACME},
	"trusttunnel": {model.UtilityCaddy, model.UtilityACME},
}

// RequiredUtilitiesForProtocol returns the utility names a protocol needs on
// a caddy-mode node (nil = none). Exposed for the UI (install suggestions).
func RequiredUtilitiesForProtocol(proto string) []string {
	return protocolUtilityDeps[proto]
}

// ValidateUtilityDeps checks that the node satisfies the utility requirements
// of a new inbound/feature. TLS-terminating protocols (naive/trusttunnel)
// always need a TLSDomain + caddy/acme — they do not work on a bare IP.
// Other protocols keep legacy direct-port behaviour when TLSDomain is empty.
//
// Two failure classes (rule #6, no silent failures):
//   - missing utilities → "install X, Y first" (UI shows an install action);
//   - hard incompatibilities that utilities can NOT fix (mtproxy insists on
//     owning 443 with FakeTLS and cannot sit behind the SNI router) → a clear
//     refusal.
func ValidateUtilityDeps(info *model.NodeInfo, proto string, port int) error {
	req := RequiredUtilitiesForProtocol(proto)
	if len(req) > 0 {
		if info == nil || info.TLSDomain == "" {
			return fmt.Errorf("protocol %q needs a TLS domain on this node — open Utilities, set a domain, add DNS A-records, then Install all and Issue/renew certificate", proto)
		}
	} else if info == nil || info.TLSDomain == "" {
		return nil
	}
	if proto == "mtproxy" && caddyOwnedPorts[port] {
		return fmt.Errorf("MTProxy cannot share port %d with the caddy utility on this node — pick a free port (e.g. 8443) or remove the caddy utility", port)
	}
	if len(req) == 0 {
		return nil
	}
	var missing []string
	for _, name := range req {
		if !info.UtilityInstalled(name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("protocol %q needs TLS utilities on this node (%v) — open the node's Utilities and run the TLS quick start: set a domain, add the DNS A-records, Install all, then Issue/renew certificate", proto, missing)
	}
	return nil
}

// MissingUtilities returns the required-but-not-installed utility names for a
// feature on a caddy-mode node (empty = satisfied or not caddy mode). Used by
// the UI to render the "install missing utilities" action.
func MissingUtilities(info *model.NodeInfo, proto string) []string {
	req := RequiredUtilitiesForProtocol(proto)
	if len(req) == 0 {
		return nil
	}
	if info == nil {
		return append([]string(nil), req...)
	}
	var missing []string
	for _, name := range req {
		if !info.UtilityInstalled(name) {
			missing = append(missing, name)
		}
	}
	return missing
}
