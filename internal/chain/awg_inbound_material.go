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
