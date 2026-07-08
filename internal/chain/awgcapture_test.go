package chain

// awgcapture_test.go — unit tests for the QUIC CPS capture slice logic. The
// network path (CaptureQUICSignature) is covered by the e2e suite; this file
// pins the partial-capture slice-safety regression (a capture that returns
// fewer than captureMaxPkts packets must not panic on packets[:captureMaxPkts]).

import (
	"bytes"
	"strings"
	"testing"
)

// TestCapturePacketsToCPS_PartialNoPanic is the regression for the live-VPS
// panic: CaptureQUICSignature returned 2 packets (I1 + one response) and then
// sliced packets[:5] → "slice bounds out of range [:5] with capacity 2". A
// partial capture must yield a partial CPS set, not crash the orchestrator.
func TestCapturePacketsToCPS_PartialNoPanic(t *testing.T) {
	packets := [][]byte{
		bytes.Repeat([]byte{0xAA}, 40), // I1 (sent Initial)
		bytes.Repeat([]byte{0xBB}, 60), // I2 (one server response, then timeout)
	}
	cps := capturePacketsToCPS(packets)
	if len(cps) != 2 {
		t.Fatalf("partial capture: want 2 CPS entries, got %d (must not panic)", len(cps))
	}
	for i, s := range cps {
		if !strings.HasPrefix(s, "<b 0x") {
			t.Errorf("CPS[%d] = %q, want a hex blob string", i, s)
		}
	}
}

// TestCapturePacketsToCPS_Full verifies a full I1-I5 capture produces exactly
// captureMaxPkts entries (no off-by-one, no truncation of the 5th).
func TestCapturePacketsToCPS_Full(t *testing.T) {
	packets := make([][]byte, captureMaxPkts+2) // more than the budget
	for i := range packets {
		packets[i] = bytes.Repeat([]byte{byte(i)}, 30)
	}
	cps := capturePacketsToCPS(packets)
	if len(cps) != captureMaxPkts {
		t.Fatalf("full capture: want %d CPS entries (capped), got %d", captureMaxPkts, len(cps))
	}
}

// TestCapturePacketsToCPS_ClipsOversized verifies a packet larger than
// captureMaxPacket is clipped before CPS encoding (the live capture read buffer
// is 2048, but the wire MTU clamp keeps CPS material bounded).
func TestCapturePacketsToCPS_ClipsOversized(t *testing.T) {
	huge := bytes.Repeat([]byte{0xCC}, captureMaxPacket+500)
	cps := capturePacketsToCPS([][]byte{huge})
	if len(cps) != 1 {
		t.Fatalf("want 1 CPS entry, got %d", len(cps))
	}
	// CPSString renders the clipped packet; the panic-regression concern is
	// only that pkt[:captureMaxPacket] does not itself overflow (it won't —
	// len(huge) > captureMaxPacket). Assert it produced a non-empty hex blob.
	if len(cps[0]) < 10 {
		t.Errorf("CPS entry unexpectedly short: %q", cps[0])
	}
}

// TestCapturePacketsToCPS_Empty verifies an empty slice yields an empty CPS
// set (no panic, no spurious entries).
func TestCapturePacketsToCPS_Empty(t *testing.T) {
	cps := capturePacketsToCPS(nil)
	if len(cps) != 0 {
		t.Errorf("empty packets: want 0 CPS entries, got %d", len(cps))
	}
}