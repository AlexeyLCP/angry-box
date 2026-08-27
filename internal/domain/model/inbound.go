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
	Protocol string `json:"protocol"`              // "awg" | "vless-reality" | "mtproxy" | "naive" | "mieru" | "trusttunnel"
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

	// ── AWG protocol version selector (AGENTS.md #5/#10 revision). The
	// canonical picker for AWG 1.5 / 2.0 / 3.0 — see awg_version.go for the
	// full version taxonomy + EffectiveAWGVersion reconciliation. Empty
	// defaults to "2" (the current default kernel+CPS path); "3" opts into
	// header protection (AWG3Mode below). Legacy stores without this field
	// keep their AWG3Mode bool and resolve through EffectiveAWGVersion, so
	// the migration is non-destructive.
	AWGVersion string `json:"awg_version,omitempty"`

	// ── AWG 3.0 obfuscation mode (legacy toggle, AGENTS #5). Kept as a
	// backward-compat alias for AWGVersion=="3": pre-version-field stores
	// (v0.8.10) set AWG3Mode=true and resolve to "3" via EffectiveAWGVersion.
	// New code should write AWGVersion instead. When effective version is 3
	// the user-facing AWG entry is rendered as a userspace sing-box
	// `type:"awg"` endpoint (amneziaawg-go feat/awg3) carrying the AWG3 fields
	// below IN ADDITION to the classic amnezia Jc/S1-S4/H1-H4/I1-I5 (which
	// still come from the preset + CPS material). The kernel amnezia module
	// did NOT parse AWG3 fields historically (AGENTS #10/#11) — native HPK
	// landed in the kernel module on 2026-07-30 (PR #192); the kernel-render
	// path is slice 2.
	//
	// AWG3 material is generated ONCE per profile (EnsureProfileAWGMaterial /
	// EnsureInboundAWGMaterial) and persisted here as the shared source of
	// truth (same pattern as AWGCPSI1/AWGH1) so a redeploy reuses it and
	// existing clients are not re-keyed. AWG3HeaderProtectionKey is the raw
	// hex of 32 random bytes (sing-box endpoint.go decodes base64 from JSON
	// and converts to hex for the amneziaawg-go UAPI; we persist hex to match
	// the §30 spike). ContentPaddingAddition / RekeyAfterTime are "lo-hi"
	// UintRange strings (seconds for RekeyAfterTime). S1-S4 must be >= 12
	// when HeaderProtectionKey is set (HeaderCipherNonceSize=12) — enforced
	// at emit time (BuildAmneziaSection / userspace builder raises them).
	AWG3Mode                 bool   `json:"awg3_mode,omitempty"`
	AWG3HeaderProtectionKey   string `json:"awg3_header_protection_key,omitempty"`
	AWG3ContentPaddingAddition string `json:"awg3_content_padding_addition,omitempty"`
	AWG3RekeyAfterTime        string `json:"awg3_rekey_after_time,omitempty"`

	// MieruTransport is "TCP" or "UDP" (fork validation). Empty = TCP.
	MieruTransport string `json:"mieru_transport,omitempty"`

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
