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
		return // plain WireGuard — no material to persist
	}
	if ib.AWGCPSI1 != "" && ib.AWGH1 != "" &&
		ib.AWGCPSLevel == level && ib.AWGCPSMimicry == mimicry {
		return // cache valid
	}
	mat := GenerateAWGObfsMaterial(level, mimicry)
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
}

// InboundAWGObfsMaterial reconstructs the persisted AWGObfsMaterial from a
// standalone AWG inbound. Returns nil when the inbound has no CPS material
// (plain WG / not yet populated), so callers can pass it straight to
// BuildAWGAmnezia and get the no-CPS path.
func InboundAWGObfsMaterial(ib *model.NodeInbound) *AWGObfsMaterial {
	if ib.AWGCPSI1 == "" {
		return nil
	}
	strs := []string{ib.AWGCPSI1, ib.AWGCPSI2, ib.AWGCPSI3, ib.AWGCPSI4, ib.AWGCPSI5}
	return &AWGObfsMaterial{
		I1:             cpsStringToBytes(strs[0]),
		I2:             cpsStringToBytes(strs[1]),
		I3:             cpsStringToBytes(strs[2]),
		I4:             cpsStringToBytes(strs[3]),
		I5:             cpsStringToBytes(strs[4]),
		H1:             ib.AWGH1,
		H2:             ib.AWGH2,
		H3:             ib.AWGH3,
		H4:             ib.AWGH4,
		MimicryProfile: ib.AWGCPSMimicry,
		CPSLevel:       ib.AWGCPSLevel,
	}
}

// ResolveStandaloneAWGPreset resolves the obfuscation preset for a standalone
// AWG inbound: ib.Obfuscation when set (a preset name), else the panel default.
// Both the deploy render and the client-conf render must use this same
// resolution or server and client diverge (previously the client side always
// used the default preset — a silent mismatch on custom-preset inbounds).
func ResolveStandaloneAWGPreset(ib *model.NodeInbound) ConnectionPreset {
	if ib.Obfuscation != "" {
		if p, ok := GetPreset(ib.Obfuscation); ok {
			return p
		}
	}
	return GetDefaultPreset()
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
// Field semantics (mirrors the chain): AWGCPSMimicry is the REQUEST override
// ("" = from preset; never rewritten by this function). CapturedDomain marks
// a successful capture (domain change re-captures); CaptureFailedDomain marks
// a failed one (suppresses re-dialing the same flaky domain on every deploy).
func EnsureProfileAWGMaterial(prof *model.InboundProfile, preset ConnectionPreset) bool {
	if prof.AWGCPSCaptureDomain == "" {
		return false
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
		return false
	}

	cacheValid := prof.AWGCPSI1 != "" && prof.AWGCPSLevel == level
	if mimicry == mimicryQuicLive {
		success := prof.AWGCPSCapturedDomain != "" && prof.AWGCPSCapturedDomain == prof.AWGCPSCaptureDomain
		failed := prof.AWGCPSCaptureFailedDomain != "" && prof.AWGCPSCaptureFailedDomain == prof.AWGCPSCaptureDomain
		cacheValid = cacheValid && (success || failed)
	}
	if cacheValid {
		return false
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
			mat := GenerateAWGObfsMaterial(level, "quic")
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
	mat := GenerateAWGObfsMaterial(level, mimicry)
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
// when the profile has none (no capture domain — per-node path applies).
func applyProfileAWGMaterial(ib *model.NodeInbound, prof *model.InboundProfile) bool {
	if prof.AWGCPSI1 == "" {
		return false
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
// (capture happens at profile save / deploy time, not here).
func ApplyProfileMaterialToInbound(ib *model.NodeInbound, prof *model.InboundProfile, preset ConnectionPreset) {
	if prof != nil && applyProfileAWGMaterial(ib, prof) {
		return
	}
	EnsureInboundAWGMaterial(ib, preset)
}
