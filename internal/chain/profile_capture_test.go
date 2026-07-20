package chain

// profile_capture_test.go — profile-level live QUIC capture material
// (EnsureProfileAWGMaterial + ApplyProfileMaterialToInbound). The live dial
// itself is NOT exercised here (network); cache validity and the failure
// marker are covered by pre-seeded state, mirroring the chain-side tests.

import (
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestEnsureProfileAWGMaterial_NoDomain(t *testing.T) {
	prof := &model.InboundProfile{ID: "p1", Protocol: "awg"}
	if EnsureProfileAWGMaterial(prof, GetDefaultPreset()) {
		t.Error("no capture domain → no profile material (per-node synthesized path)")
	}
	if prof.AWGCPSI1 != "" {
		t.Error("material populated without a capture domain")
	}
}

func TestEnsureProfileAWGMaterial_SynthesizedWithDomain(t *testing.T) {
	// Domain set but mimicry override is non-live ("quic") → synthesized shared
	// material, no dial.
	prof := &model.InboundProfile{
		ID: "p1", Protocol: "awg",
		AWGCPSCaptureDomain: "example.com",
		AWGCPSMimicry:       "quic",
	}
	preset := GetDefaultPreset()
	if preset.CPSLevel <= 0 && (preset.AWG == nil || preset.AWG.CPSLevel <= 0) {
		t.Skip("default preset has no CPS")
	}
	if !EnsureProfileAWGMaterial(prof, preset) {
		t.Fatal("expected shared synthesized material")
	}
	if prof.AWGCPSI1 == "" || prof.AWGH1 == "" {
		t.Error("material not populated")
	}
	if prof.AWGCPSCapturedDomain != "" {
		t.Error("synthesized path must not record a captured domain")
	}
}

func TestEnsureProfileAWGMaterial_FailureMarkerSkipsRedial(t *testing.T) {
	// The operator REQUESTS quic-live (AWGCPSMimicry — the request field), but
	// a prior capture for this same domain failed; the synthesized fallback
	// material is persisted. The failed-domain marker must suppress a re-dial.
	preset := GetDefaultPreset()
	level := preset.CPSLevel
	if level <= 0 && preset.AWG != nil {
		level = preset.AWG.CPSLevel
	}
	if level <= 0 {
		t.Skip("default preset has no CPS")
	}
	prof := &model.InboundProfile{
		ID: "p1", Protocol: "awg",
		AWGCPSCaptureDomain:       "flaky.example.com",
		AWGCPSMimicry:             "quic-live", // the request
		AWGCPSLevel:               level,
		AWGCPSI1:                  "<b 0x01>",
		AWGH1:                     "5-1000",
		AWGCPSCaptureFailedDomain: "flaky.example.com",
	}
	if EnsureProfileAWGMaterial(prof, preset) {
		t.Error("failed-domain marker did not suppress re-dial")
	}
	if prof.AWGCPSI1 != "<b 0x01>" {
		t.Error("persisted fallback material clobbered")
	}
	if prof.AWGCPSMimicry != "quic-live" {
		t.Error("request field rewritten by ensure (must stay the operator's request)")
	}
}

func TestApplyProfileMaterialToInbound(t *testing.T) {
	prof := &model.InboundProfile{
		ID: "p1", Protocol: "awg",
		AWGCPSLevel: 2, AWGCPSMimicry: "quic-live",
		AWGCPSI1: "<b 0x01>", AWGCPSI2: "<b 0x02>",
		AWGH1: "5-1000", AWGH4: "3001-4000",
	}
	ib := &model.NodeInbound{Protocol: "awg"}
	preset := GetDefaultPreset()
	ApplyProfileMaterialToInbound(ib, prof, preset)
	if ib.AWGCPSI1 != "<b 0x01>" || ib.AWGCPSI2 != "<b 0x02>" || ib.AWGH1 != "5-1000" || ib.AWGH4 != "3001-4000" {
		t.Errorf("profile material not copied: %+v", ib)
	}
	if ib.AWGCPSMimicry != "quic-live" || ib.AWGCPSLevel != 2 {
		t.Errorf("level/mimicry: %+v", ib)
	}

	// No profile material → per-node synthesized ensure runs instead.
	ib2 := &model.NodeInbound{Protocol: "awg"}
	ApplyProfileMaterialToInbound(ib2, nil, preset)
	if preset.CPSLevel > 0 || (preset.AWG != nil && preset.AWG.CPSLevel > 0) {
		if ib2.AWGCPSI1 == "" {
			t.Error("per-node synthesized material expected for ad-hoc inbound")
		}
	}
}
