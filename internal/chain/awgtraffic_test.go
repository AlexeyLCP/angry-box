package chain

import (
	"context"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestParseAWGTransfer(t *testing.T) {
	out := "PUB_A\t100\t200\nPUB_B\t0\t0\nmalformed\n"
	got := ParseAWGTransfer(out)
	if len(got) != 2 || got["PUB_A"] != [2]int64{100, 200} || got["PUB_B"] != [2]int64{0, 0} {
		t.Errorf("bad parse: %+v", got)
	}
}

func TestFoldAWGTraffic_AccumulatesAndResets(t *testing.T) {
	s := tempStore(t)
	u := &model.User{ID: "u1", Name: "alice", Active: true, AWGPublicKey: "PUB_A"}
	if err := s.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	m := &model.NodeMetrics{HostID: "n1"}

	// First observation — counts everything.
	FoldAWGTraffic(s, "n1", "awg0", map[string][2]int64{"PUB_A": {1000, 500}}, m)
	u2, _ := s.GetUser("u1")
	if u2.AWGRxBytes != 1000 || u2.AWGTxBytes != 500 {
		t.Fatalf("first fold: got %d/%d", u2.AWGRxBytes, u2.AWGTxBytes)
	}

	// Delta accumulation.
	FoldAWGTraffic(s, "n1", "awg0", map[string][2]int64{"PUB_A": {1600, 900}}, m)
	u2, _ = s.GetUser("u1")
	if u2.AWGRxBytes != 1600 || u2.AWGTxBytes != 900 {
		t.Fatalf("delta fold: got %d/%d want 1600/900", u2.AWGRxBytes, u2.AWGTxBytes)
	}

	// Counter reset (interface restart) — adds the current value, not negative.
	FoldAWGTraffic(s, "n1", "awg0", map[string][2]int64{"PUB_A": {100, 50}}, m)
	u2, _ = s.GetUser("u1")
	if u2.AWGRxBytes != 1700 || u2.AWGTxBytes != 950 {
		t.Fatalf("reset fold: got %d/%d want 1700/950", u2.AWGRxBytes, u2.AWGTxBytes)
	}

	// Unknown peer — tracked in snapshot, not folded.
	FoldAWGTraffic(s, "n1", "awg0", map[string][2]int64{"PUB_A": {100, 50}, "PUB_UNKNOWN": {999, 999}}, m)
	u2, _ = s.GetUser("u1")
	if u2.AWGRxBytes != 1700 {
		t.Fatalf("unknown peer must not fold: got %d", u2.AWGRxBytes)
	}
	if _, ok := m.AWGPeerTransfer["PUB_UNKNOWN"]; !ok {
		t.Error("unknown peer must still be tracked in the snapshot baseline")
	}
}

func TestSelfHealAWGRules(t *testing.T) {
	conf := "/etc/amnezia/amneziawg/awg0.conf"
	// Rules present → no-op.
	present := newFakeSSH(
		fakeRule{substring: "iptables -C FORWARD", out: ""},
	)
	healed, err := SelfHealAWGRules(context.Background(), present, "awg0", false)
	if err != nil || healed {
		t.Errorf("rules present: healed=%v err=%v, want false/nil", healed, err)
	}
	if strings.Contains(strings.Join(present.Commands(), "\n"), "PostUp") {
		t.Error("no PostUp re-run expected when rules are present")
	}

	// Rules missing → PostUp re-run + healed.
	missing := newFakeSSH(
		fakeRule{substring: "iptables -C FORWARD", err: errDiagFake, exit: 1},
	)
	healed, err = SelfHealAWGRules(context.Background(), missing, "awg0", false)
	if err != nil || !healed {
		t.Errorf("rules missing: healed=%v err=%v, want true/nil", healed, err)
	}
	joined := strings.Join(missing.Commands(), "\n")
	if !strings.Contains(joined, "sed -n 's/^PostUp = //p' "+conf+" | sh") {
		t.Errorf("heal must re-run the conf's PostUp lines:\n%s", joined)
	}
}
