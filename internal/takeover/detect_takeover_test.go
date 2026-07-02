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

// TestDetectVPN_ActiveSingBox verifies an active sing-box service is detected as
// the primary VPN.
func TestDetectVPN_ActiveSingBox(t *testing.T) {
	fake := newFakeSSH(
		// sing-box is-active -> active.
		fakeRule{substring: "is-active sing-box", out: "active"},
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

// TestDetectVPN_ConfigOnly verifies a config file present (no active service)
// is detected.
func TestDetectVPN_ConfigOnly(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "cat /etc/sing-box/config.json", out: `{"inbounds":[]}`},
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
// returns the rolled-back status without panicking.
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
	if res.Status != "rolled-back" {
		t.Errorf("Status: got %q, want rolled-back", res.Status)
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