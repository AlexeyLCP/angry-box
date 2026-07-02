package takeover

// takeover_render_test.go — tests for renderTakeoverConfig, buildMinimalConfig-
// WithExtra, and rollbackToOldVPN. CTO-review C3 phase 5.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// stubBackend returns a controlled GenerateConfig result so renderTakeoverConfig
// can exercise its append-extra and minimal-config branches.
type stubBackend struct {
	cfg *model.Config
	err error
}

func (b *stubBackend) Deploy(context.Context, model.Host) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (b *stubBackend) DeployWithOptions(context.Context, model.Host, model.DeployOptions) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (b *stubBackend) InstallAWGModule(context.Context, model.Host) error { return nil }
func (b *stubBackend) ApplyConfig(context.Context, model.Host, model.ConfigType, model.ConfigParams) error {
	return nil
}
func (b *stubBackend) Remove(context.Context, model.Host) error                     { return nil }
func (b *stubBackend) GetStatus(context.Context, model.Host) (*model.Status, error) { return &model.Status{}, nil }
func (b *stubBackend) GenerateConfig(model.ConfigType, model.ConfigParams) (*model.Config, error) {
	return b.cfg, b.err
}
func (b *stubBackend) Reload(context.Context, model.Host) error { return nil }
func (b *stubBackend) Name() string                            { return "stub" }
func (b *stubBackend) Version() string                         { return "test" }

type stubFactory struct{ b ports.Backend }

func (f stubFactory) Create() ports.Backend { return f.b }

// TestRenderTakeoverConfig_NoExtra verifies a config with native inbounds and no
// extra is returned as-is.
func TestRenderTakeoverConfig_NoExtra(t *testing.T) {
	f := stubFactory{b: &stubBackend{cfg: &model.Config{Content: `{"inbounds":[{"type":"direct"}],"outbounds":[]}`}}}
	out, err := renderTakeoverConfig(f, []model.NodeInbound{{Protocol: "vless", Port: 8443}}, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != `{"inbounds":[{"type":"direct"}],"outbounds":[]}` {
		t.Errorf("got %q, want the config as-is", out)
	}
}

// TestRenderTakeoverConfig_AppendsExtra verifies extra raw inbounds are appended
// to the rendered config's inbounds array.
func TestRenderTakeoverConfig_AppendsExtra(t *testing.T) {
	base := `{"inbounds":[{"type":"direct","tag":"d"}],"outbounds":[]}`
	f := stubFactory{b: &stubBackend{cfg: &model.Config{Content: base}}}
	extra := []json.RawMessage{json.RawMessage(`{"type":"trojan","tag":"trojan-in"}`)}
	out, err := renderTakeoverConfig(f, []model.NodeInbound{{Protocol: "vless", Port: 8443}}, extra)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var sc struct {
		Inbounds []json.RawMessage `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(out), &sc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(sc.Inbounds) != 2 {
		t.Fatalf("inbounds: got %d, want 2 (native + extra)", len(sc.Inbounds))
	}
	if !contains(string(sc.Inbounds[1]), "trojan-in") {
		t.Errorf("extra not appended: %s", sc.Inbounds[1])
	}
}

// TestRenderTakeoverConfig_NoInboundsWithExtra verifies the minimal-config branch
// when GenerateConfig fails AND there are no native inbounds but extra exists.
func TestRenderTakeoverConfig_NoInboundsWithExtra(t *testing.T) {
	f := stubFactory{b: &stubBackend{cfg: nil, err: errors.New("no native inbounds")}}
	extra := []json.RawMessage{json.RawMessage(`{"type":"trojan","tag":"trojan-in"}`)}
	out, err := renderTakeoverConfig(f, nil, extra)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var sc struct {
		Inbounds  []json.RawMessage `json:"inbounds"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(out), &sc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(sc.Inbounds) != 1 {
		t.Fatalf("inbounds: got %d, want 1 (the extra)", len(sc.Inbounds))
	}
	if len(sc.Outbounds) != 1 {
		t.Fatalf("outbounds: got %d, want 1 (direct)", len(sc.Outbounds))
	}
}

// TestRenderTakeoverConfig_GenFailsWithNative verifies a GenerateConfig failure
// with native inbounds (and no extra) propagates the error.
func TestRenderTakeoverConfig_GenFailsWithNative(t *testing.T) {
	f := stubFactory{b: &stubBackend{cfg: nil, err: errors.New("gen boom")}}
	_, err := renderTakeoverConfig(f, []model.NodeInbound{{Protocol: "vless"}}, nil)
	if err == nil {
		t.Fatal("expected gen error")
	}
	if !contains(err.Error(), "gen boom") {
		t.Errorf("got %q, want gen boom", err.Error())
	}
}

// TestBuildMinimalConfigWithExtra verifies the minimal config is valid JSON with
// the extra inbounds + a direct outbound.
func TestBuildMinimalConfigWithExtra(t *testing.T) {
	extra := []json.RawMessage{json.RawMessage(`{"type":"vmess","tag":"v"}`)}
	out, err := buildMinimalConfigWithExtra(extra)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var sc struct {
		Log       json.RawMessage   `json:"log"`
		Inbounds  []json.RawMessage `json:"inbounds"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(out), &sc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(sc.Inbounds) != 1 {
		t.Errorf("inbounds: got %d, want 1", len(sc.Inbounds))
	}
	if len(sc.Outbounds) != 1 {
		t.Errorf("outbounds: got %d, want 1", len(sc.Outbounds))
	}
	if !contains(string(sc.Outbounds[0]), "direct") {
		t.Errorf("outbound should be direct, got %s", sc.Outbounds[0])
	}
}

// TestRollbackToOldVPN_AWG verifies AWG detection is a no-op rollback (returns nil
// without touching SSH).
func TestRollbackToOldVPN_AWG(t *testing.T) {
	fake := newFakeSSH()
	err := rollbackToOldVPN(context.Background(), fake, &Detection{Type: DetectedAWG}, &model.TakeoverState{}, false)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if len(fake.commands) != 0 {
		t.Errorf("AWG rollback should be a no-op, got commands: %v", fake.commands)
	}
}

// TestRollbackToOldVPN_NoServiceName verifies a missing service name is rejected.
func TestRollbackToOldVPN_NoServiceName(t *testing.T) {
	fake := newFakeSSH()
	err := rollbackToOldVPN(context.Background(), fake, &Detection{Type: DetectedSingBox}, &model.TakeoverState{}, false)
	if err == nil {
		t.Fatal("expected no-service-name error")
	}
}

// TestRollbackToOldVPN_RestoresAndProbes verifies the rollback restores the old
// config backup and re-enables + probes the old service.
func TestRollbackToOldVPN_RestoresAndProbes(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "is-active", out: "UP"}, // ProbeServiceUp success
		fakeRule{substring: "", out: ""},
	)
	state := &model.TakeoverState{
		OldConfigBackup: "/tmp/bak/config.json.bak",
		OldConfigPath:   "/etc/sing-box/config.json",
		OldEnabled:      true,
	}
	err := rollbackToOldVPN(context.Background(), fake, &Detection{Type: DetectedSingBox, ServiceName: "sing-box"}, state, false)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !fake.Saw("cp /tmp/bak/config.json.bak") {
		t.Error("rollback did not restore the config backup")
	}
	if !fake.Saw("systemctl enable sing-box") {
		t.Error("rollback did not re-enable the old service")
	}
}

// TestRollbackToOldVPN_ProbeFails verifies a failed probe surfaces the error.
func TestRollbackToOldVPN_ProbeFails(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "is-active", out: "DOWN"}, // never UP -> probe fails
		fakeRule{substring: "journalctl", out: "FATAL: boom"},
		fakeRule{substring: "", out: ""},
	)
	state := &model.TakeoverState{OldEnabled: true}
	err := rollbackToOldVPN(context.Background(), fake, &Detection{Type: DetectedSingBox, ServiceName: "sing-box"}, state, false)
	if err == nil {
		t.Fatal("expected probe failure")
	}
	if !contains(err.Error(), "did not come back") {
		t.Errorf("got %q, want did-not-come-back", err.Error())
	}
}