package model

import "time"

// InboundProfile is a first-class, node-independent description of a
// user-facing listener (protocol + parameters). A profile is MATERIALIZED
// onto nodes as NodeInfo.Inbounds[] entries carrying ProfileID — the
// materialized per-node NodeInbound (with its own generated server creds,
// AWG subnet, CPS material) is the only place credentials live; the profile
// itself holds no secrets.
//
// Source of truth for "which nodes is this profile deployed on" is
// NodeInbound.ProfileID (NOT a field here) — compute it via
// store.ProfileNodes. This avoids a two-way association that would drift.
//
// Chains reference profiles per node via ChainNode.InboundRef: on the entry
// level it is the user-facing listener clients connect to; on transit/exit
// levels it parametrizes the chain-generated transport listener (protocol,
// port, obfuscation preset from the profile; credentials stay the chain's
// persisted transit keys).
type InboundProfile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`              // "awg" | "vless-reality" | "mtproxy" (frozen protocols rejected)
	Port     int    `json:"port"`                  // default listen port for new materializations
	Obfuscation string `json:"obfuscation,omitempty"` // preset name (AWG) / notes
	// AWGCPSCaptureDomain enables the live QUIC signature capture for AWG
	// profiles (moved from Chain.AWGCPSCaptureDomain): dial domain:443 over
	// UDP, send a real QUIC Initial, capture server responses as CPS I1-I5.
	// Empty = synthesized CPS packets (default).
	AWGCPSCaptureDomain string `json:"awg_cps_capture_domain,omitempty"`
	// AWGCPSMimicry is the mimicry override: "" = from the preset, "quic-live"
	// = live capture against AWGCPSCaptureDomain. Other explicit modes
	// (quic/sip/dns/none) force the synthesized generator's mode.
	AWGCPSMimicry string `json:"awg_cps_mimicry,omitempty"`

	// ── Shared live-capture material (one capture per profile+domain, shared
	// by every materialized inbound — all nodes of one profile mimic the same
	// domain). Populated by chain.EnsureProfileAWGMaterial when
	// AWGCPSCaptureDomain is set; AWGCPSCapturedDomain records which domain the
	// I1-I5 came from (a domain change re-captures), AWGCPSCaptureFailedDomain
	// suppresses re-dialing a flaky domain on every deploy.
	AWGCPSLevel          int    `json:"awg_cps_level,omitempty"`
	AWGCPSI1             string `json:"awg_cps_i1,omitempty"`
	AWGCPSI2             string `json:"awg_cps_i2,omitempty"`
	AWGCPSI3             string `json:"awg_cps_i3,omitempty"`
	AWGCPSI4             string `json:"awg_cps_i4,omitempty"`
	AWGCPSI5             string `json:"awg_cps_i5,omitempty"`
	AWGH1                string `json:"awg_h1,omitempty"`
	AWGH2                string `json:"awg_h2,omitempty"`
	AWGH3                string `json:"awg_h3,omitempty"`
	AWGH4                string `json:"awg_h4,omitempty"`
	AWGCPSCapturedDomain string `json:"awg_cps_captured_domain,omitempty"`
	AWGCPSCaptureFailedDomain string `json:"awg_cps_capture_failed_domain,omitempty"`

	CreatedAt          time.Time `json:"created_at"`
}

// ChainLevel is one ordered hop level of a chain. A level holds one or more
// nodes (a group); traffic reaching the level is distributed across the group
// per Strategy. Levels replace the flat Chain.Nodes list as the authoring
// model — Chain.Nodes is kept only for reading pre-v2 stores (migration).
type ChainLevel struct {
	ID string `json:"id"`
	// Strategy picks the sing-box group type wrapping the per-node outbounds
	// of the NEXT level. Only meaningful when the next level has >1 node.
	// Empty with a multi-node next level defaults to StrategyFallback
	// (round-robin — the patched, production-verified path). urltest is an
	// explicit opt-in (server-side probes through transit hops are flaky, see
	// merged_config.go note).
	Strategy Strategy `json:"strategy,omitempty"`
	Nodes    []ChainNode `json:"nodes"`
}
