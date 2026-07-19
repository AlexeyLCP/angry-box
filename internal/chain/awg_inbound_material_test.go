package chain

// awg_inbound_material_test.go — persisted obfs material for standalone AWG
// inbounds: proper quadrant H1-H4 (not the preset's degenerate "N-N"),
// idempotent generation, server↔client identity.

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestEnsureInboundAWGMaterial_GeneratesProperQuadrants(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820}
	preset := GetDefaultPreset()
	EnsureInboundAWGMaterial(ib, preset)
	if ib.AWGCPSI1 == "" || ib.AWGH1 == "" {
		t.Fatal("material not generated for a CPS preset")
	}
	// H ranges must be proper "lo-hi" with width, not degenerate "N-N".
	for i, h := range []string{ib.AWGH1, ib.AWGH2, ib.AWGH3, ib.AWGH4} {
		parts := strings.Split(h, "-")
		if len(parts) != 2 || parts[0] == parts[1] {
			t.Errorf("H%d = %q is degenerate (want a proper quadrant range)", i+1, h)
		}
	}
	// H ranges must not overlap (quadrant rule).
	if ib.AWGH1 == ib.AWGH2 || ib.AWGH1 == ib.AWGH3 {
		t.Error("H ranges must be distinct")
	}
}

func TestEnsureInboundAWGMaterial_Idempotent(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820}
	preset := GetDefaultPreset()
	EnsureInboundAWGMaterial(ib, preset)
	h1, i1 := ib.AWGH1, ib.AWGCPSI1
	EnsureInboundAWGMaterial(ib, preset)
	if ib.AWGH1 != h1 || ib.AWGCPSI1 != i1 {
		t.Error("material must be stable across re-ensure (Rule 5)")
	}
}

func TestEnsureInboundAWGMaterial_InvalidatesOnPresetChange(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820}
	preset := GetDefaultPreset()
	EnsureInboundAWGMaterial(ib, preset)
	// A preset with a different level/mimicry must regenerate.
	other := preset
	other.CPSLevel = preset.CPSLevel + 1
	i1 := ib.AWGCPSI1
	EnsureInboundAWGMaterial(ib, other)
	if ib.AWGCPSI1 == i1 {
		t.Error("preset change must invalidate the cached material")
	}
	if ib.AWGCPSLevel != other.CPSLevel {
		t.Errorf("level must follow the new preset: got %d want %d", ib.AWGCPSLevel, other.CPSLevel)
	}
}

func TestEnsureInboundAWGMaterial_PlainWGNoop(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820}
	EnsureInboundAWGMaterial(ib, ConnectionPreset{}) // level 0 → plain WG
	if ib.AWGCPSI1 != "" || ib.AWGH1 != "" {
		t.Error("level-0 preset must not generate material")
	}
}

func TestInboundAWGObfsMaterial_RoundTrip(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820}
	if InboundAWGObfsMaterial(ib) != nil {
		t.Error("empty inbound must yield nil material")
	}
	preset := GetDefaultPreset()
	EnsureInboundAWGMaterial(ib, preset)
	mat := InboundAWGObfsMaterial(ib)
	if mat == nil || mat.H1 != ib.AWGH1 || mat.H4 != ib.AWGH4 {
		t.Fatalf("round-trip mismatch: %+v vs %+v", mat, ib)
	}
	if len(mat.I1) == 0 {
		t.Error("I1 bytes must be reconstructed from the persisted string")
	}
}

// TestRenderStandaloneAWGConf_UsesPersistedMaterial pins that the server conf
// carries the inbound's persisted H ranges (not the degenerate "1984-1984"
// fallback) — and no I1-I5 (kernel 6.12 setconf rejects them, dc72ca3).
func TestRenderStandaloneAWGConf_UsesPersistedMaterial(t *testing.T) {
	ib := &model.NodeInbound{
		Protocol: "awg", Port: 51820, Tag: "sa-0-awg",
		ServerPrivKey: "SRVPRIV",
	}
	preset := GetDefaultPreset()
	EnsureInboundAWGMaterial(ib, preset)
	f := renderStandaloneAWGConf(ib, "sa-0-awg", nil, "awg0")
	if !strings.Contains(f.Content, "H1 = "+ib.AWGH1) {
		t.Errorf("server conf must carry the persisted H1 %q:\n%s", ib.AWGH1, f.Content)
	}
	if strings.Contains(f.Content, "1984-1984") {
		t.Error("server conf must NOT contain the degenerate 1984-1984 range")
	}
	for _, banned := range []string{"I1 =", "I5 ="} {
		if strings.Contains(f.Content, banned) {
			t.Errorf("server conf must NOT contain %q (setconf 6.12 rejects)", banned)
		}
	}
}
