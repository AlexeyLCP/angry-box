package model

// AWG protocol versions (AGENTS.md #5/#10 revision). Before the AWGVersion field
// existed the only signal was the AWG3Mode bool toggle; that stays as a legacy
// alias so old stores keep working. AWGVersion is the canonical selector going
// forward — it lets the operator explicitly pick AWG 1.5 / 2.0 / 3.0 instead of
// the binary "AWG3 on/off" of v0.8.10.
//
// Version taxonomy (verified against amnezia-vpn/amneziawg-linux-kernel-module +
// amnezia.org/blog/amneziawg-2-0-available-for-self-hosted, 2026-07-31):
//
//   - AWGVersion1x ("1.5") — legacy AmneziaWG 1.x: Jc/Jmin/Jmax + S1-S2 +
//     FIXED-value H1-H4. No S3-S4, no I1-I5 (CPS), no header protection. The
//     kernel path renders H1-H4 degenerate ("N-N") and omits CPS — the
//     fingerprintable but maximally-compatible baseline. A 1.x client cannot
//     talk to a 2.0/3.0 server when the new fields are active.
//   - AWGVersion2 — AmneziaWG 2.0: adds S3-S4, I1-I5 (CPS signature packets),
//     RANGE-based H1-H4 (quadrant material), Itime. This is the current default
//     kernel path (awg-quick + sing-box TUN-overlay + CPS via PostUp on the
//     Linux initiator). Public since 2025.
//   - AWGVersion3 — AmneziaWG 3.0: adds header protection (HeaderProtectionKey),
//     ContentPaddingAddition, RekeyAfterTime. Header protection applies fast
//     encryption to low-entropy header fields (partially overlapping the role
//     of H1-H4 packet-type markers, so awg3 presets minimize H1-H4 as redundant
//     and rely on HPK for the primary stealth). The amnezia-box kernel module
//     gained NATIVE HPK support on 2026-07-30 (PR #192, merged to master; Sx>=12
//     validation landed 2026-07-31). Slice 1 still renders AWG3 userspace
//     (sing-box type:"awg" endpoint, as in v0.8.10) — the kernel-render path +
//     deps bump is slice 2 (AGENTS.md revision). S1-S4 must be >= 12 when HPK
//     is set (HeaderCipherNonceSize=12).
const (
	AWGVersion1x = "1.5"
	AWGVersion2  = "2"
	AWGVersion3  = "3"
)

// EffectiveAWGVersion returns the canonical AWG protocol version for an
// InboundProfile, reconciling the new AWGVersion field with the legacy AWG3Mode
// bool. AWG3Mode==true always wins ("3") so a pre-version-field store keeps its
// userspace-AWG3 behavior; otherwise AWGVersion is returned, defaulting to "2"
// (the current default kernel+CPS path) when empty.
func (p InboundProfile) EffectiveAWGVersion() string {
	return effectiveAWGVersion(p.AWG3Mode, p.AWGVersion)
}

// EffectiveAWGVersion for NodeInbound mirrors InboundProfile: the materialized
// per-node copy carries the same AWG3Mode/AWGVersion fields (copied by
// ApplyProfileMaterialToInbound), so both resolve identically.
func (ib NodeInbound) EffectiveAWGVersion() string {
	return effectiveAWGVersion(ib.AWG3Mode, ib.AWGVersion)
}

// effectiveAWGVersion is the shared reconciliation used by both InboundProfile
// and NodeInbound. Legacy AWG3Mode bool is honored as "3" (the v0.8.10 toggle);
// an unrecognized/empty version falls back to "2".
func effectiveAWGVersion(awg3Mode bool, version string) string {
	if awg3Mode || version == AWGVersion3 {
		return AWGVersion3
	}
	switch version {
	case AWGVersion1x, AWGVersion2:
		return version
	default:
		return AWGVersion2
	}
}

// IsKnownAWGVersion reports whether v is one of the canonical version constants.
// Used by the preset resolver + UI validation to reject bogus values early.
func IsKnownAWGVersion(v string) bool {
	switch v {
	case AWGVersion1x, AWGVersion2, AWGVersion3:
		return true
	default:
		return false
	}
}
