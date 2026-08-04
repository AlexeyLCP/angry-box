package chain

// awg_inbound_material.go — persisted AWG obfuscation material for STANDALONE
// AWG inbounds (model.NodeInbound), mirroring the chain-side persistence on
// model.Chain (EnsureChainAWGMaterial / ChainAWGObfsMaterial). Until now the
// standalone path rendered with nil material: H1-H4 fell back to the preset's
// degenerate zero-width "N-N" ranges (header-junk randomization off —
// fingerprintable, a stealth regression) and I1-I5 were fresh-random on every
// render. The material is generated once (proper quadrant H ranges + CPS
// packets for the preset's level/mimicry) and persisted on the inbound, so
// server and client configs render identical values across redeploys and
// re-renders. Live QUIC capture stays chain-only (it needs a capture domain;
// standalone uses synthesized packets).

import (
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// EnsureInboundAWGMaterial generates and persists the AWG obfuscation material
// on a standalone AWG inbound when missing or stale (level/mimicry changed via
// a preset switch). Idempotent: a valid cache is kept (Rule 5 — server and
// client configs must stay stable across redeploys). Callers must persist the
// inbound (SaveNodeInfo) after calling.
func EnsureInboundAWGMaterial(ib *model.NodeInbound, preset ConnectionPreset) {
	level := 0
	mimicry := "none"
	if preset.CPSLevel > 0 {
		level = preset.CPSLevel
		mimicry = preset.AWGMimicry
	} else if preset.AWG != nil && preset.AWG.CPSLevel > 0 {
		level = preset.AWG.CPSLevel
		mimicry = preset.AWG.Mimicry
	}
	if level <= 0 {
		// Plain amnezia CPS is off, but AWG 3.0 mode is independent of CPS —
		// generate its material (HPK/CPM/RAT) here even with level 0.
		ensureInboundAWG3Material(ib)
		return // plain WireGuard — no CPS material to persist
	}
	if ib.AWGCPSI1 != "" && ib.AWGH1 != "" &&
		ib.AWGCPSLevel == level && ib.AWGCPSMimicry == mimicry {
		ensureInboundAWG3Material(ib)
		return // cache valid
	}
	mat := GenerateAWGObfsMaterialForVersion(level, mimicry, ib.EffectiveAWGVersion())
	strs := CPSMaterialStrings(mat)
	ib.AWGCPSLevel = level
	ib.AWGCPSMimicry = mimicry
	ib.AWGCPSI1 = strs[0]
	ib.AWGCPSI2 = strs[1]
	ib.AWGCPSI3 = strs[2]
	ib.AWGCPSI4 = strs[3]
	ib.AWGCPSI5 = strs[4]
	ib.AWGH1 = mat.H1
	ib.AWGH2 = mat.H2
	ib.AWGH3 = mat.H3
	ib.AWGH4 = mat.H4
	ensureInboundAWG3Material(ib)
}

// ensureInboundAWG3Material generates + persists the AWG 3.0 fields (header
// protection key + content-padding range + rekey-after-time range) on a
// standalone inbound when AWG3Mode is on and the key is missing. Idempotent: an
// existing HeaderProtectionKey is kept (Rule 5 — never re-key on redeploy, or
// every existing client's handshake breaks). Turning AWG3Mode off leaves stale
// fields in place (harmless — they are only emitted when AWG3Mode is on); a
// clean re-enable after an off cycle reuses the old key, which is safe.
func ensureInboundAWG3Material(ib *model.NodeInbound) {
	if ib.EffectiveAWGVersion() != model.AWGVersion3 {
		return
	}
	if ib.AWG3HeaderProtectionKey != "" {
		return // cache valid — reuse the persisted key
	}
	awg3 := GenerateAWG3Material()
	ib.AWG3HeaderProtectionKey = awg3.HeaderProtectionKey
	ib.AWG3ContentPaddingAddition = awg3.ContentPaddingAddition
	ib.AWG3RekeyAfterTime = awg3.RekeyAfterTime
}

// InboundAWGObfsMaterial reconstructs the persisted AWGObfsMaterial from a
// standalone AWG inbound. Returns nil when the inbound has neither CPS nor
// AWG3 material (plain WG / not yet populated), so callers can pass it
// straight to BuildAWGAmnezia and get the no-CPS path.
func InboundAWGObfsMaterial(ib *model.NodeInbound) *AWGObfsMaterial {
	isV3 := ib.EffectiveAWGVersion() == model.AWGVersion3
	if ib.AWGCPSI1 == "" && !isV3 {
		return nil
	}
	m := &AWGObfsMaterial{
		MimicryProfile: ib.AWGCPSMimicry,
		CPSLevel:       ib.AWGCPSLevel,
		H1:             ib.AWGH1,
		H2:             ib.AWGH2,
		H3:             ib.AWGH3,
		H4:             ib.AWGH4,
	}
	if ib.AWGCPSI1 != "" {
		strs := []string{ib.AWGCPSI1, ib.AWGCPSI2, ib.AWGCPSI3, ib.AWGCPSI4, ib.AWGCPSI5}
		m.I1 = cpsStringToBytes(strs[0])
		m.I2 = cpsStringToBytes(strs[1])
		m.I3 = cpsStringToBytes(strs[2])
		m.I4 = cpsStringToBytes(strs[3])
		m.I5 = cpsStringToBytes(strs[4])
	}
	if isV3 {
		m.AWG3Mode = true
		m.HeaderProtectionKey = ib.AWG3HeaderProtectionKey
		m.ContentPaddingAddition = ib.AWG3ContentPaddingAddition
		m.RekeyAfterTime = ib.AWG3RekeyAfterTime
	}
	return m
}

// ResolveStandaloneAWGPreset resolves the obfuscation preset for a standalone
// AWG inbound: ib.Obfuscation when set (a preset name), else the panel default.
// Both the deploy render and the client-conf render must use this same
// resolution or server and client diverge (previously the client side always
// used the default preset — a silent mismatch on custom-preset inbounds).
func ResolveStandaloneAWGPreset(ib *model.NodeInbound) ConnectionPreset {
	if ib.Obfuscation != "" {
		if p, ok := GetPreset(ib.Obfuscation); ok {
			return resolveAWGPresetForVersion(p, ib.EffectiveAWGVersion())
		}
	}
	// No explicit preset → the per-version default (v3 inbound must not fall
	// back to a v2 preset, which would lack the HPK S1-S4>=12 contract).
	if name := defaultPresetForAWGVersion(ib.EffectiveAWGVersion()); name != "" {
		if p, ok := GetPreset(name); ok {
			return p
		}
	}
	return GetDefaultPreset()
}

// ResolveChainEntryPreset resolves the obfuscation preset for a chain ENTRY
// whose listener is a materialized profile inbound. It is the SINGLE resolver
// that all three render paths must use — the server-side kernel conf
// (renderChainEntryAWGConf), the server-side AWG3 userspace endpoint
// (buildChainRoleInOut), and the client .conf (RenderClientAWGConf) — or the
// two sides emit different amnezia parameters and no client can handshake.
//
// Resolution: the inbound's own preset wins ONLY when it names one
// (ib.Obfuscation != ""); otherwise the CHAIN's preset is kept. The non-empty
// guard is essential: falling back to ResolveStandaloneAWGPreset for an inbound
// with an empty Obfuscation would silently drop a custom chain preset to the
// panel default and break every already-connected kernel client.
//
// Live regression this fixes (PROGRESS §39): the server rendered S1=15 from the
// chain preset while the client .conf rendered S1=115 from the profile preset.
func ResolveChainEntryPreset(chainPreset ConnectionPreset, ib *model.NodeInbound) ConnectionPreset {
	version := ""
	if ib != nil {
		version = ib.EffectiveAWGVersion()
	}
	if ib == nil || ib.Obfuscation == "" {
		return resolveAWGPresetForVersion(chainPreset, version)
	}
	if p, ok := GetPreset(ib.Obfuscation); ok {
		return resolveAWGPresetForVersion(p, version)
	}
	return resolveAWGPresetForVersion(chainPreset, version)
}

// resolveAWGPresetForVersion enforces the preset↔AWG-version contract: if the
// selected preset's AWG section is incompatible with the inbound's effective
// AWG version (PresetSupportsAWGVersion), it is replaced by the per-version
// default. This keeps a v3 inbound from silently rendering a v2 preset (whose
// S1-S4 may be < 12 → HPK rejected) and vice versa. The compatibility check is
// a no-op for non-AWG presets (Reality/XHTTP ignore version entirely).
func resolveAWGPresetForVersion(preset ConnectionPreset, version string) ConnectionPreset {
	if preset.AWG == nil {
		return preset
	}
	if PresetSupportsAWGVersion(preset, version) {
		return preset
	}
	if name := defaultPresetForAWGVersion(version); name != "" {
		if dp, ok := GetPreset(name); ok {
			return dp
		}
	}
	return preset
}

// ─── Profile-level live QUIC capture ─────────────────────────────────────────
//
// A profile deployed on N nodes mimics ONE domain — the live-captured CPS
// signature is captured ONCE per profile+domain and shared by every
// materialized inbound (Rule 5: a re-deploy never re-captures; a domain
// change re-captures; a failed domain is not re-dialed on every deploy).
// Synthesized CPS stays per-node (fleet diversity) when no capture domain is
// set.

// EnsureProfileAWGMaterial ensures the profile's shared live-capture material:
// when AWGCPSCaptureDomain is set and the cache is stale, dials the domain
// (real QUIC Initial), stores the captured I1-I5 + generated H1-H4 on the
// profile. Returns true when the profile changed and must be persisted.
// No-op (false) for profiles without a capture domain — the per-node
// synthesized path (EnsureInboundAWGMaterial) covers those.
//
// AWG 3.0 material (HPK/CPM/RAT) is independent of CPS capture: it is generated
// here when AWG3Mode is on and the key is missing, even with no capture domain.
// The return value is true if EITHER the CPS capture or the AWG3 key changed.
//
// Field semantics (mirrors the chain): AWGCPSMimicry is the REQUEST override
// ("" = from preset; never rewritten by this function). CapturedDomain marks
// a successful capture (domain change re-captures); CaptureFailedDomain marks
// a failed one (suppresses re-dialing the same flaky domain on every deploy).
func EnsureProfileAWGMaterial(prof *model.InboundProfile, preset ConnectionPreset) bool {
	changed := false
	if prof.EffectiveAWGVersion() == model.AWGVersion3 && prof.AWG3HeaderProtectionKey == "" {
		awg3 := GenerateAWG3Material()
		prof.AWG3HeaderProtectionKey = awg3.HeaderProtectionKey
		prof.AWG3ContentPaddingAddition = awg3.ContentPaddingAddition
		prof.AWG3RekeyAfterTime = awg3.RekeyAfterTime
		changed = true
	}
	if prof.AWGCPSCaptureDomain == "" {
		return changed
	}
	level := 0
	mimicry := "none"
	if preset.CPSLevel > 0 {
		level = preset.CPSLevel
		mimicry = preset.AWGMimicry
	} else if preset.AWG != nil && preset.AWG.CPSLevel > 0 {
		level = preset.AWG.CPSLevel
		mimicry = preset.AWG.Mimicry
	}
	// Profile-level request override wins over the preset (the UI's select).
	if prof.AWGCPSMimicry != "" {
		mimicry = prof.AWGCPSMimicry
		if mimicry != "none" && level == 0 {
			level = 2 // an explicit mimicry mode implies CPS on
		}
	}
	if level <= 0 {
		return changed
	}

	cacheValid := prof.AWGCPSI1 != "" && prof.AWGCPSLevel == level
	if mimicry == mimicryQuicLive {
		success := prof.AWGCPSCapturedDomain != "" && prof.AWGCPSCapturedDomain == prof.AWGCPSCaptureDomain
		failed := prof.AWGCPSCaptureFailedDomain != "" && prof.AWGCPSCaptureFailedDomain == prof.AWGCPSCaptureDomain
		cacheValid = cacheValid && (success || failed)
	}
	if cacheValid {
		return changed
	}

	if mimicry == mimicryQuicLive {
		res := CaptureQUICSignature(prof.AWGCPSCaptureDomain, 0)
		if res.OK && len(res.Packets) >= 5 {
			prof.AWGCPSLevel = level
			prof.AWGCPSCapturedDomain = prof.AWGCPSCaptureDomain
			prof.AWGCPSCaptureFailedDomain = ""
			prof.AWGCPSI1 = res.Packets[0]
			prof.AWGCPSI2 = res.Packets[1]
			prof.AWGCPSI3 = res.Packets[2]
			prof.AWGCPSI4 = res.Packets[3]
			prof.AWGCPSI5 = res.Packets[4]
			mat := GenerateAWGObfsMaterialForVersion(level, "quic", prof.EffectiveAWGVersion())
			prof.AWGH1 = mat.H1
			prof.AWGH2 = mat.H2
			prof.AWGH3 = mat.H3
			prof.AWGH4 = mat.H4
			return true
		}
		// Capture failed — mark the domain (no re-dial next time) and fall
		// through to the synthesized shared material.
		prof.AWGCPSCaptureFailedDomain = prof.AWGCPSCaptureDomain
		mimicry = "quic"
	}
	// Synthesized shared material (also the fallback for a failed capture).
	mat := GenerateAWGObfsMaterialForVersion(level, mimicry, prof.EffectiveAWGVersion())
	strs := CPSMaterialStrings(mat)
	prof.AWGCPSLevel = level
	prof.AWGCPSCapturedDomain = ""
	if mimicry != "quic" {
		prof.AWGCPSCaptureFailedDomain = ""
	}
	prof.AWGCPSI1 = strs[0]
	prof.AWGCPSI2 = strs[1]
	prof.AWGCPSI3 = strs[2]
	prof.AWGCPSI4 = strs[3]
	prof.AWGCPSI5 = strs[4]
	prof.AWGH1 = mat.H1
	prof.AWGH2 = mat.H2
	prof.AWGH3 = mat.H3
	prof.AWGH4 = mat.H4
	return true
}

// applyProfileAWGMaterial copies the profile's shared material onto a
// materialized inbound. Returns true when the inbound has profile-backed
// material (the caller must NOT run the per-node synthesized ensure); false
// when the profile has none (no capture domain + no AWG3 — per-node path
// applies). AWG3 material is profile-backed (shared across nodes) and is
// copied whenever the profile has it, independent of CPS capture.
func applyProfileAWGMaterial(ib *model.NodeInbound, prof *model.InboundProfile) bool {
	ib.AWGVersion = prof.AWGVersion
	ib.AWG3Mode = prof.AWG3Mode
	ib.AWG3HeaderProtectionKey = prof.AWG3HeaderProtectionKey
	ib.AWG3ContentPaddingAddition = prof.AWG3ContentPaddingAddition
	ib.AWG3RekeyAfterTime = prof.AWG3RekeyAfterTime
	if prof.AWGCPSI1 == "" {
		// No shared CPS material, but AWG3 may still be profile-backed.
		return prof.AWG3Mode || prof.AWGVersion == model.AWGVersion3
	}
	ib.AWGCPSLevel = prof.AWGCPSLevel
	ib.AWGCPSMimicry = prof.AWGCPSMimicry
	ib.AWGCPSI1 = prof.AWGCPSI1
	ib.AWGCPSI2 = prof.AWGCPSI2
	ib.AWGCPSI3 = prof.AWGCPSI3
	ib.AWGCPSI4 = prof.AWGCPSI4
	ib.AWGCPSI5 = prof.AWGCPSI5
	ib.AWGH1 = prof.AWGH1
	ib.AWGH2 = prof.AWGH2
	ib.AWGH3 = prof.AWGH3
	ib.AWGH4 = prof.AWGH4
	return true
}

// ApplyProfileMaterialToInbound is the single entry point used by every
// materialization path (profile deploy, chain entry, client-conf render):
// the profile's shared material when present, else the per-node synthesized
// ensure. The profile itself must already have run EnsureProfileAWGMaterial
// (capture + AWG3 key generation happen at profile save / deploy time, not
// here).
func ApplyProfileMaterialToInbound(ib *model.NodeInbound, prof *model.InboundProfile, preset ConnectionPreset) {
	if prof != nil && applyProfileAWGMaterial(ib, prof) {
		return
	}
	EnsureInboundAWGMaterial(ib, preset)
}
