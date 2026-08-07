package takeover

// detect_takeover_test.go — DetectVPN + Takeover tests against the fake SSH.
// No real network. CTO-review C3 phase 4.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// hostA is a generic test host.
var hostA = model.Host{ID: "h1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}

// newStore returns a fresh temp-backed chain.Store.
func newStore(t *testing.T) *chain.Store {
	t.Helper()
	return chain.NewStore(filepath.Join(t.TempDir(), "store.json"))
}

// noopBackend / noopFactory so Takeover's install path doesn't need the real
// singbox backend (which would SSH for Deploy).
type noopBackend struct{}

func (noopBackend) Deploy(context.Context, model.Host) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (noopBackend) DeployWithOptions(context.Context, model.Host, model.DeployOptions) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (noopBackend) InstallAWGModule(context.Context, model.Host) error { return nil }
func (noopBackend) InstallAWGModuleWithOptions(context.Context, model.Host, model.DeployOptions) error { return nil }
func (noopBackend) ApplyConfig(context.Context, model.Host, model.ConfigType, model.ConfigParams) error {
	return nil
}
func (noopBackend) Remove(context.Context, model.Host) error                     { return nil }
func (noopBackend) GetStatus(context.Context, model.Host) (*model.Status, error) { return &model.Status{}, nil }
func (noopBackend) GenerateConfig(model.ConfigType, model.ConfigParams) (*model.Config, error) {
	return &model.Config{}, nil
}
func (noopBackend) Reload(context.Context, model.Host) error { return nil }
func (noopBackend) Name() string                            { return "fake" }
func (noopBackend) Version() string                         { return "test" }

type noopFactory struct{}

func (noopFactory) Create() ports.Backend { return noopBackend{} }

// TestDetectVPN_None verifies a clean node (nothing active/enabled, no configs)
// returns DetectedNone with no error.
func TestDetectVPN_None(t *testing.T) {
	// Empty catch-all rule returns "" for every probe -> nothing matches.
	fake := newFakeSSH(fakeRule{substring: "", out: ""})
	det, err := DetectVPN(context.Background(), hostA, false, &fakeConnector{client: fake})
	if err != nil {
		t.Fatalf("DetectVPN: %v", err)
	}
	if det.Type != DetectedNone {
		t.Errorf("Type: got %q, want none", det.Type)
	}
}

// TestDetectVPN_ConnectFails verifies a connect failure surfaces.
func TestDetectVPN_ConnectFails(t *testing.T) {
	_, err := DetectVPN(context.Background(), hostA, false, &fakeConnector{err: errors.New("dial: refused")})
	if err == nil {
		t.Fatal("expected connect failure")
	}
	if !contains(err.Error(), "detect") {
		t.Errorf("got %q, want detect error", err.Error())
	}
}

// TestDetectVPN_ActiveSingBox verifies an active sing-box service with a real
// convertible inbound is detected as the primary VPN.
func TestDetectVPN_ActiveSingBox(t *testing.T) {
	realCfg := `{"inbounds":[{"type":"vless","listen_port":443,"users":[{"uuid":"11111111-1111-1111-1111-111111111111"}],"tls":{"enabled":true,"server_name":"example.com","reality":{"enabled":true,"private_key":"x","short_id":["abcd"]}}}]}`
	fake := newFakeSSH(
		// sing-box is-active -> active.
		fakeRule{substring: "is-active sing-box", out: "active"},
		fakeRule{substring: "cat /etc/sing-box/config.json", out: realCfg},
		// everything else (is-enabled, other services, cat) returns "".
		fakeRule{substring: "", out: ""},
	)
	det, err := DetectVPN(context.Background(), hostA, false, &fakeConnector{client: fake})
	if err != nil {
		t.Fatalf("DetectVPN: %v", err)
	}
	if det.Type != DetectedSingBox {
		t.Errorf("Type: got %q, want sing-box", det.Type)
	}
	if !det.IsActive {
		t.Error("expected IsActive=true")
	}
	if det.ServiceName != "sing-box" {
		t.Errorf("ServiceName: got %q, want sing-box", det.ServiceName)
	}
}

// TestDetectVPN_EmptySingBoxScaffold_Skipped verifies the angry-box Deploy
// minimal config (inbounds:[]) is NOT treated as a takeover target — the
// self-takeover loop when "deploy sing-box" + "detect VPN" are both ticked.
func TestDetectVPN_EmptySingBoxScaffold_Skipped(t *testing.T) {
	// Deploy's exact scaffold shape.
	minimal := `{"log":{"level":"info"},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`
	fake := newFakeSSH(
		fakeRule{substring: "is-active sing-box", out: "active"},
		fakeRule{substring: "is-enabled sing-box", out: "enabled"},
		fakeRule{substring: "cat /etc/sing-box/config.json", out: minimal},
		fakeRule{substring: "", out: ""},
	)
	det, err := DetectVPN(context.Background(), hostA, false, &fakeConnector{client: fake})
	if err != nil {
		t.Fatalf("DetectVPN: %v", err)
	}
	if det.Type != DetectedNone {
		t.Errorf("Type: got %q, want none (empty scaffold skipped)", det.Type)
	}
	if det.Note == "" || !contains(det.Note, "empty") {
		t.Errorf("Note should explain empty scaffold, got %q", det.Note)
	}
}

// TestDetectVPN_EmptySingBox_PrefersXray verifies that when sing-box is only a
// scaffold, a foreign xray service is still selected as primary.
func TestDetectVPN_EmptySingBox_PrefersXray(t *testing.T) {
	minimal := `{"inbounds":[]}`
	xrayCfg := `{"inbounds":[{"protocol":"vless","port":443,"settings":{"clients":[{"id":"11111111-1111-1111-1111-111111111111"}]},"streamSettings":{"security":"reality","realitySettings":{"privateKey":"x","shortIds":["ab"]}}}]}`
	fake := newFakeSSH(
		fakeRule{substring: "is-active sing-box", out: "active"},
		fakeRule{substring: "is-active xray", out: "active"},
		fakeRule{substring: "cat /etc/sing-box/config.json", out: minimal},
		fakeRule{substring: "cat /usr/local/etc/xray/config.json", out: xrayCfg},
		fakeRule{substring: "", out: ""},
	)
	det, err := DetectVPN(context.Background(), hostA, false, &fakeConnector{client: fake})
	if err != nil {
		t.Fatalf("DetectVPN: %v", err)
	}
	if det.Type != DetectedXray {
		t.Errorf("Type: got %q, want xray (empty sing-box skipped)", det.Type)
	}
}

// TestDetectVPN_ConfigOnly verifies a config file with a convertible inbound
// (no active service) is detected.
func TestDetectVPN_ConfigOnly(t *testing.T) {
	realCfg := `{"inbounds":[{"type":"shadowsocks","listen_port":8388,"method":"aes-128-gcm","password":"p"}]}`
	fake := newFakeSSH(
		fakeRule{substring: "cat /etc/sing-box/config.json", out: realCfg},
		fakeRule{substring: "", out: ""},
	)
	det, err := DetectVPN(context.Background(), hostA, false, &fakeConnector{client: fake})
	if err != nil {
		t.Fatalf("DetectVPN: %v", err)
	}
	if det.Type != DetectedSingBox {
		t.Errorf("Type: got %q, want sing-box (config-only)", det.Type)
	}
	if det.ConfigPath != "/etc/sing-box/config.json" {
		t.Errorf("ConfigPath: got %q", det.ConfigPath)
	}
}

// TestTakeover_Nothing verifies Takeover with a DetectedNone detection returns
// the "nothing" status without SSH.
func TestTakeover_Nothing(t *testing.T) {
	st := newStore(t)
	res, err := Takeover(context.Background(), st, noopFactory{}, hostA, false, &Detection{Type: DetectedNone})
	if err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	if res.Status != "nothing" {
		t.Errorf("Status: got %q, want nothing", res.Status)
	}
}

// TestTakeover_NilDetection verifies a nil detection is treated as nothing.
func TestTakeover_NilDetection(t *testing.T) {
	st := newStore(t)
	res, err := Takeover(context.Background(), st, noopFactory{}, hostA, false, nil)
	if err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	if res.Status != "nothing" {
		t.Errorf("Status: got %q, want nothing", res.Status)
	}
}

// TestTakeover_ConvertFails verifies a convert failure (bad config content)
// returns Status "failed" (nothing was cut over — not a rollback).
func TestTakeover_ConvertFails(t *testing.T) {
	st := newStore(t)
	det := &Detection{
		Type:          DetectedSingBox,
		ServiceName:   "sing-box",
		IsActive:      true,
		ConfigPath:    "/etc/sing-box/config.json",
		ConfigContent: "{not valid json", // Convert will fail to parse
	}
	res, err := Takeover(context.Background(), st, noopFactory{}, hostA, false, det, &fakeConnector{client: newFakeSSH(fakeRule{substring: "", out: ""})})
	if err == nil {
		t.Fatal("expected convert failure")
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.Status != "failed" {
		t.Errorf("Status: got %q, want failed", res.Status)
	}
}

// TestTakeover_EmptySingBoxNothing verifies an empty/minimal sing-box config
// maps to Status "nothing" (no error, no rolled-back claim) — the self-takeover
// loop the capture form used to hit when deploy+detect were both ticked.
func TestTakeover_EmptySingBoxNothing(t *testing.T) {
	st := newStore(t)
	det := &Detection{
		Type:          DetectedSingBox,
		ServiceName:   "sing-box",
		IsActive:      true,
		ConfigPath:    "/etc/sing-box/config.json",
		ConfigContent: `{"log":{"level":"info"},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`,
	}
	res, err := Takeover(context.Background(), st, noopFactory{}, hostA, false, det)
	if err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	if res.Status != "nothing" {
		t.Errorf("Status: got %q, want nothing", res.Status)
	}
	if !contains(res.Message, "no convertible") {
		t.Errorf("Message: got %q, want no convertible", res.Message)
	}
}

// TestSingBoxConfigConvertible covers the empty-scaffold / TUN-only / real
// inbound cases used by DetectVPN filtering.
func TestSingBoxConfigConvertible(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want bool
	}{
		{"empty json object", `{}`, false},
		{"empty inbounds", `{"inbounds":[]}`, false},
		{"deploy scaffold", `{"log":{"level":"info"},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`, false},
		{"tun only", `{"inbounds":[{"type":"tun","tag":"tun-in"}]}`, false},
		{"vless", `{"inbounds":[{"type":"vless","listen_port":443}]}`, true},
		{"shadowsocks", `{"inbounds":[{"type":"shadowsocks"}]}`, true},
		{"unparseable", `{not json`, true}, // let Convert surface parse error
		{"blank", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := singBoxConfigConvertible(tc.cfg); got != tc.want {
				t.Errorf("singBoxConfigConvertible(%q)=%v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// contains is a tiny helper (the takeover package's own contains is for user
// lists). Repeated here to avoid exporting.
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}