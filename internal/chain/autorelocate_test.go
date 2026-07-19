package chain

// autorelocate_test.go — P2b decision-layer tests: the double opt-in
// guardrails, cooldown, spare picking, and spare consumption. SSH-free (the
// deploy part lives in relocate.go and is covered by its own tests).

import (
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// seedAutoRelocate seeds: one active node "n1" (AutoRelocate per flag) and one
// spare "sp1" (with optional inbounds), plus global settings (enabled per
// flag, cooldown hours).
func seedAutoRelocate(t *testing.T, globalOn, nodeOn bool, cooldownHours int, lastRelocate time.Time, spareInbounds int) *Store {
	t.Helper()
	s := tempStore(t)
	seedHost(t, s, "n1", "10.0.0.1:22")
	seedHost(t, s, "sp1", "10.0.0.2:22")
	if err := s.SaveNodeInfo(&model.NodeInfo{
		Host:               model.Host{ID: "n1", Addr: "10.0.0.1:22", User: "root", KeyPath: "/k"},
		AutoRelocate:       nodeOn,
		LastAutoRelocateAt: lastRelocate,
	}); err != nil {
		t.Fatal(err)
	}
	spare := &model.NodeInfo{
		Host:  model.Host{ID: "sp1", Addr: "10.0.0.2:22", User: "root", KeyPath: "/k"},
		Spare: true,
	}
	for i := 0; i < spareInbounds; i++ {
		spare.Inbounds = append(spare.Inbounds, model.NodeInbound{Protocol: "awg", Port: 51820 + i})
	}
	if err := s.SaveNodeInfo(spare); err != nil {
		t.Fatal(err)
	}
	settings, _ := s.GetSettings()
	settings.AutoRelocate = &model.AutoRelocateConfig{Enabled: globalOn, CooldownHours: cooldownHours}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAutoRelocateDecision_Guardrails(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name         string
		globalOn     bool
		nodeOn       bool
		cooldownH    int
		lastRelocate time.Time
		wantReason   string
		wantOK       bool
	}{
		{"global off", false, true, 0, time.Time{}, "disabled-global", false},
		{"node off", true, false, 0, time.Time{}, "disabled-node", false},
		{"cooldown active (default 6h)", true, true, 0, now.Add(-2 * time.Hour), "cooldown", false},
		{"cooldown active (custom 24h)", true, true, 24, now.Add(-12 * time.Hour), "cooldown", false},
		{"cooldown expired (default)", true, true, 0, now.Add(-7 * time.Hour), "go", true},
		{"cooldown expired (custom)", true, true, 24, now.Add(-25 * time.Hour), "go", true},
		{"never relocated", true, true, 0, time.Time{}, "go", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := seedAutoRelocate(t, tc.globalOn, tc.nodeOn, tc.cooldownH, tc.lastRelocate, 0)
			spare, reason, ok := AutoRelocateDecision(s, "n1", now)
			if ok != tc.wantOK || reason != tc.wantReason {
				t.Errorf("AutoRelocateDecision = (%v, %q, %v), want (spare, %q, %v)", spare != nil, reason, ok, tc.wantReason, tc.wantOK)
			}
			if tc.wantOK && (spare == nil || spare.ID != "sp1") {
				t.Errorf("want spare sp1, got %+v", spare)
			}
		})
	}
}

func TestAutoRelocateDecision_NoSpare(t *testing.T) {
	s := seedAutoRelocate(t, true, true, 0, time.Time{}, 0)
	// Consume the only spare into a chain-less but user-carrying state.
	spare, _ := s.GetNodeInfo("sp1")
	spare.Inbounds = []model.NodeInbound{{Protocol: "awg", Port: 51820}}
	if err := s.SaveNodeInfo(spare); err != nil {
		t.Fatal(err)
	}
	_, reason, ok := AutoRelocateDecision(s, "n1", time.Now())
	if ok || reason != "no-spare" {
		t.Errorf("want no-spare, got (%q, %v)", reason, ok)
	}
}

func TestAutoRelocateDecision_NeverRelocatesSpareItself(t *testing.T) {
	s := seedAutoRelocate(t, true, true, 0, time.Time{}, 0)
	_, reason, ok := AutoRelocateDecision(s, "sp1", time.Now())
	if ok || reason != "is-spare" {
		t.Errorf("spare node must never be relocated itself, got (%q, %v)", reason, ok)
	}
}

func TestPickSpare_SkipsChainedAndBusy(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "10.0.0.1:22")
	seedHost(t, s, "sp-chain", "10.0.0.2:22")
	seedHost(t, s, "sp-busy", "10.0.0.3:22")
	seedHost(t, s, "sp-free", "10.0.0.4:22")
	// sp-chain: spare but referenced by a chain.
	if err := s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "sp-chain", Addr: "10.0.0.2:22"}, Spare: true}); err != nil {
		t.Fatal(err)
	}
	// sp-busy: spare but carries a user inbound.
	if err := s.SaveNodeInfo(&model.NodeInfo{
		Host:     model.Host{ID: "sp-busy", Addr: "10.0.0.3:22"},
		Spare:    true,
		Inbounds: []model.NodeInbound{{Protocol: "vless-reality", Port: 443}},
	}); err != nil {
		t.Fatal(err)
	}
	// sp-free: the only eligible one.
	if err := s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "sp-free", Addr: "10.0.0.4:22"}, Spare: true}); err != nil {
		t.Fatal(err)
	}
	// Chain referencing sp-chain.
	c := &model.Chain{
		Name:  "c1",
		Nodes: []model.ChainNode{{ID: "sp-chain", Port: 443}},
	}
	if err := s.SaveChain(c); err != nil {
		t.Fatal(err)
	}
	got, err := PickSpare(s, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "sp-free" {
		t.Errorf("want sp-free, got %+v", got)
	}
}

func TestConsumeSpare_RemovesNodeInfoMetricsAndHost(t *testing.T) {
	s := seedAutoRelocate(t, true, true, 0, time.Time{}, 0)
	if err := s.SaveMetrics(&model.NodeMetrics{HostID: "sp1", State: model.NodeStateHealthy}); err != nil {
		t.Fatal(err)
	}
	if err := ConsumeSpare(s, "sp1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetNodeInfo("sp1"); err == nil {
		t.Error("spare NodeInfo must be gone after ConsumeSpare")
	}
	if m, _ := s.GetMetrics("sp1"); m != nil && m.State == model.NodeStateHealthy {
		t.Error("spare Metrics must be gone after ConsumeSpare")
	}
	if _, err := s.GetHost("sp1"); err == nil {
		t.Error("spare Host must be gone after ConsumeSpare")
	}
	// Idempotent — second call must not fail.
	if err := ConsumeSpare(s, "sp1"); err != nil {
		t.Errorf("ConsumeSpare must be idempotent: %v", err)
	}
}
