package singbox

// backend_ssh_test.go — exercises the singbox Backend's SSH-coupled methods
// (Deploy/DeployOpts/InstallAWGModule/ApplyConfig/Remove/GetStatus/Reload)
// against the fake SSH. No real network. CTO-review C3 phase 4.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// hostA is a generic test host.
var hostA = model.Host{ID: "h1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}

// TestBackend_DeployOpts_HappyPath verifies a deploy over a fake SSH succeeds
// when the binary is already the patched version (install skipped) and the
// service comes up.
func TestBackend_DeployOpts_HappyPath(t *testing.T) {
	fake := newFakeSSH(deployRules()...)
	b := New(&fakeConnector{client: fake})
	res, err := b.DeployWithOptions(context.Background(), hostA, model.DeployOptions{})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !res.Success {
		t.Errorf("res.Success=false, msg=%s", res.Message)
	}
	if !fake.Saw("systemctl daemon-reload") {
		t.Error("daemon-reload not run")
	}
	if !fake.Saw("systemctl enable") {
		t.Error("enable not run")
	}
}

// TestBackend_DeployOpts_ConnectFails verifies a connect failure surfaces.
func TestBackend_DeployOpts_ConnectFails(t *testing.T) {
	b := New(&fakeConnector{err: errors.New("dial: refused")})
	_, err := b.DeployWithOptions(context.Background(), hostA, model.DeployOptions{})
	if err == nil {
		t.Fatal("expected connect failure")
	}
	if !strings.Contains(err.Error(), "deploy") {
		t.Errorf("got %q, want deploy error", err.Error())
	}
}

// TestBackend_DeployOpts_NeedsInstall verifies the install path runs when the
// installed version is NOT the patched build (uname + curl install script).
func TestBackend_DeployOpts_NeedsInstall(t *testing.T) {
	rules := deployRules()
	// Report NOT_INSTALLED so isPatchedExtended is false -> installPatchedBinary runs.
	rules[0] = fakeRule{substring: "version", out: "NOT_INSTALLED"}
	// installPatchedBinary: uname -> x86_64, install script -> ok.
	rules = append(rules,
		fakeRule{substring: "uname -m", out: "x86_64"},
		fakeRule{substring: "sing-box.tar.gz", out: ""},
	)
	fake := newFakeSSH(rules...)
	b := New(&fakeConnector{client: fake})
	res, err := b.DeployWithOptions(context.Background(), hostA, model.DeployOptions{})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !res.Success {
		t.Errorf("res.Success=false, msg=%s", res.Message)
	}
	if !fake.Saw("uname -m") {
		t.Error("arch detection not run")
	}
	if !fake.Saw("sing-box.tar.gz") {
		t.Error("install script not run")
	}
}

// TestBackend_DeployOpts_MkdirFails verifies a mkdir failure aborts the deploy.
func TestBackend_DeployOpts_MkdirFails(t *testing.T) {
	rules := deployRules()
	rules[1] = fakeRule{substring: "mkdir -p", out: "", err: errAny}
	fake := newFakeSSH(rules...)
	b := New(&fakeConnector{client: fake})
	_, err := b.DeployWithOptions(context.Background(), hostA, model.DeployOptions{})
	if err == nil {
		t.Fatal("expected mkdir failure")
	}
	if !strings.Contains(err.Error(), "mkdir") {
		t.Errorf("got %q, want mkdir error", err.Error())
	}
}

// TestBackend_DeployOpts_ServiceNotActive verifies a failed health probe
// returns Success=false with the journal tail.
func TestBackend_DeployOpts_ServiceNotActive(t *testing.T) {
	rules := deployRules()
	rules[5] = fakeRule{substring: "is-active", out: "inactive"}
	rules[6] = fakeRule{substring: "journalctl", out: "FATAL: cannot bind 443"}
	fake := newFakeSSH(rules...)
	b := New(&fakeConnector{client: fake})
	res, err := b.DeployWithOptions(context.Background(), hostA, model.DeployOptions{})
	if err == nil {
		t.Fatal("expected service-not-active error")
	}
	if res == nil || res.Success {
		t.Error("expected Success=false result")
	}
	if !strings.Contains(res.Message, "bind 443") && !strings.Contains(err.Error(), "bind 443") {
		t.Errorf("error should carry journal tail, got msg=%q err=%v", res.Message, err)
	}
}

// TestBackend_Deploy_AliasForDeployOpts verifies Deploy (no options) delegates
// to DeployOpts with zero options.
func TestBackend_Deploy_AliasForDeployOpts(t *testing.T) {
	fake := newFakeSSH(deployRules()...)
	b := New(&fakeConnector{client: fake})
	res, err := b.Deploy(context.Background(), hostA)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !res.Success {
		t.Errorf("res.Success=false, msg=%s", res.Message)
	}
}

// TestBackend_GetStatus_Running verifies status reports Running=true when
// is-active prints "active".
func TestBackend_GetStatus_Running(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "is-active", out: "active"},
		fakeRule{substring: "version", out: "sing-box 1.2.3"},
		fakeRule{substring: "MainPID", out: "1234"},
		fakeRule{substring: "ActiveEnterTimestamp", out: "2026-01-01"},
	)
	b := New(&fakeConnector{client: fake})
	st, err := b.GetStatus(context.Background(), hostA)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !st.Running {
		t.Error("expected Running=true")
	}
	if st.PID != 1234 {
		t.Errorf("PID: got %d, want 1234", st.PID)
	}
	if st.Version == "" {
		t.Error("expected non-empty Version")
	}
}

// TestBackend_GetStatus_NotRunning verifies status reports Running=false when
// is-active prints "inactive".
func TestBackend_GetStatus_NotRunning(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "is-active", out: "inactive"},
	)
	b := New(&fakeConnector{client: fake})
	st, err := b.GetStatus(context.Background(), hostA)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if st.Running {
		t.Error("expected Running=false")
	}
}

// TestBackend_GetStatus_ConnectFails verifies a connect failure surfaces.
func TestBackend_GetStatus_ConnectFails(t *testing.T) {
	b := New(&fakeConnector{err: errors.New("no route")})
	_, err := b.GetStatus(context.Background(), hostA)
	if err == nil {
		t.Fatal("expected connect failure")
	}
}

// TestBackend_Remove verifies remove runs the cleanup script and returns nil.
func TestBackend_Remove(t *testing.T) {
	fake := newFakeSSH(fakeRule{substring: "", out: ""})
	b := New(&fakeConnector{client: fake})
	if err := b.Remove(context.Background(), hostA); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !fake.Saw("systemctl stop sing-box") {
		t.Error("stop not run")
	}
	if !fake.Saw("rm -f /usr/local/bin/sing-box") {
		t.Error("binary removal not run")
	}
}

// TestBackend_Remove_ConnectFails verifies a connect failure surfaces.
func TestBackend_Remove_ConnectFails(t *testing.T) {
	b := New(&fakeConnector{err: errors.New("no route")})
	if err := b.Remove(context.Background(), hostA); err == nil {
		t.Fatal("expected connect failure")
	}
}

// TestBackend_Reload_Ok verifies reload runs check + HUP.
func TestBackend_Reload_Ok(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "check", out: ""},
		fakeRule{substring: "systemctl kill", out: ""},
	)
	b := New(&fakeConnector{client: fake})
	if err := b.Reload(context.Background(), hostA); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !fake.Saw("check -c") {
		t.Error("config check not run before reload")
	}
}

// TestBackend_Reload_InvalidConfig verifies reload refuses when check fails.
func TestBackend_Reload_InvalidConfig(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "check", out: "", err: errAny},
	)
	b := New(&fakeConnector{client: fake})
	err := b.Reload(context.Background(), hostA)
	if err == nil {
		t.Fatal("expected reload refusal")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("got %q, want invalid-config error", err.Error())
	}
}

// TestBackend_ApplyConfig_HappyPath verifies an apply over fake SSH succeeds
// (backup + upload + check + restart + probe + cleanup).
func TestBackend_ApplyConfig_HappyPath(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "bak=", out: "/etc/sing-box/config.json.bak.1"},
		fakeRule{substring: "check", out: ""},
		fakeRule{substring: "systemctl restart", out: ""},
		fakeRule{substring: "is-active", out: "UP"},
		fakeRule{substring: "journalctl", out: ""},
		fakeRule{substring: "ls -t", out: ""},
	)
	b := New(&fakeConnector{client: fake})
	err := b.ApplyConfig(context.Background(), hostA, model.ConfigTransport, model.ConfigParams{Port: 443})
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if len(fake.uploads) == 0 {
		t.Error("expected the config to be uploaded")
	}
}

// TestBackend_ApplyConfig_CheckFails verifies a check failure triggers rollback.
func TestBackend_ApplyConfig_CheckFails(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "bak=", out: "/etc/sing-box/config.json.bak.1"},
		fakeRule{substring: "check", out: "", err: errAny},
		fakeRule{substring: "systemctl restart", out: ""}, // rollback restart
		fakeRule{substring: "is-active", out: "UP"},
	)
	b := New(&fakeConnector{client: fake})
	err := b.ApplyConfig(context.Background(), hostA, model.ConfigTransport, model.ConfigParams{Port: 443})
	if err == nil {
		t.Fatal("expected check failure")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("got %q, want rolled back", err.Error())
	}
}

// TestBackend_ApplyConfig_ConnectFails verifies a connect failure surfaces.
func TestBackend_ApplyConfig_ConnectFails(t *testing.T) {
	b := New(&fakeConnector{err: errors.New("no route")})
	err := b.ApplyConfig(context.Background(), hostA, model.ConfigTransport, model.ConfigParams{Port: 443})
	if err == nil {
		t.Fatal("expected connect failure")
	}
}

// TestBackend_Name verifies the backend identifier.
func TestBackend_Name(t *testing.T) {
	b := New(nil)
	if b.Name() != "sing-box" {
		t.Errorf("Name: got %q, want sing-box", b.Name())
	}
}

// TestBackend_Version verifies the version is non-empty.
func TestBackend_Version(t *testing.T) {
	b := New(nil)
	if b.Version() == "" {
		t.Error("Version is empty")
	}
}