package model

import "time"

// Utility names — installable node-side components managed over SSH by the
// orchestrator. Together they form the node's "spinal cord": no node-local
// daemon state, no local config UI — every artifact is rendered by the
// orchestrator and pushed through the existing apply pipeline (so installs
// inherit TOFU, sudo handling and rollback).
const (
	// UtilityCaddy is the layer4 SNI router that owns 80/443: TLS sites +
	// static subscription files + panel-relay proxying on its own domains,
	// raw-TCP passthrough to sing-box inbounds on protocol subdomains, and
	// DEFAULT passthrough to the Reality inbound (which needs no cert).
	UtilityCaddy = "caddy"
	// UtilityACME is the acme.sh client issuing the node's SAN certificate
	// (HTTP-01 through the caddy webroot). Its renewal hook reloads sing-box
	// so path-based TLS inbounds pick up the rotated cert.
	UtilityACME = "acme"
	// UtilityFakesite is the camouflage static site served by caddy on the
	// node's primary domain.
	UtilityFakesite = "fakesite"
	// UtilitySub marks the pushed per-user subscription static files
	// (sub.b64 / sub.txt / clash.yaml / vpn.txt / page.html per user).
	UtilitySub = "sub"
)

// UtilityState tracks one installable utility on a node. The orchestrator is
// the only writer; a node keeps no local state beyond the installed files.
type UtilityState struct {
	Name        string    `json:"name"`
	Installed   bool      `json:"installed"`
	Version     string    `json:"version,omitempty"`
	InstalledAt time.Time `json:"installed_at,omitempty"`
	// Revision is the store revision (Store.GetRevision) at which the last
	// artifact of this utility was pushed to the node. Comparing it with the
	// current store revision tells whether the node's copy is stale — the
	// "last config wins" rule: a fresh apply always carries the highest
	// revision, there is no merge between nodes.
	Revision int64 `json:"revision,omitempty"`
	// Status is "" (unknown), "ok", or "error: <msg>" from the last operation.
	Status string `json:"status,omitempty"`
}

// AllUtilities returns the catalog of known utilities in install order (caddy
// must precede the acme HTTP-01 webroot and the sub/fakesite file serving).
func AllUtilities() []string {
	return []string{UtilityCaddy, UtilityACME, UtilityFakesite, UtilitySub}
}

// FindUtility returns the named utility state from a slice, or nil.
func FindUtility(list []*UtilityState, name string) *UtilityState {
	for _, u := range list {
		if u != nil && u.Name == name {
			return u
		}
	}
	return nil
}

// UtilityInstalled reports whether the named utility is installed on the node.
func (n *NodeInfo) UtilityInstalled(name string) bool {
	if n == nil {
		return false
	}
	u := FindUtility(n.Utilities, name)
	return u != nil && u.Installed
}
