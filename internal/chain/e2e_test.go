//go:build e2e

package chain_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
	"github.com/alexeylcp/angry-box/internal/takeover"
)

func TestMain(m *testing.M) {
	store := chain.NewStore(filepath.Join(os.TempDir(), "angry-box-e2e-store.json"))
	sshclient.SetHostKeyManager(store)
	sshclient.SetKeyResolver(store)
	os.Exit(m.Run())
}

// ─── Read-only / parallel-safe tests (no e2eHeavy lock) ───────────────────────

func TestE2E_SSHConnect(t *testing.T) {
	t.Parallel()
	for _, srv := range e2eServers {
		t.Run(srv.Role, func(t *testing.T) {
			t.Parallel()
			client, err := sshclient.Connect(srv.Addr, srv.User, sshKeyPath(srv.KeyFile))
			if err != nil {
				t.Fatalf("Connect(%s): %v", srv.Addr, err)
			}
			defer client.Close()
			out, err := client.Run("hostname")
			if err != nil {
				t.Fatalf("hostname: %v", err)
			}
			out = strings.TrimSpace(out)
			t.Logf("%s (%s) hostname=%s", srv.ID, srv.Role, out)
			if out == "" {
				t.Error("empty hostname")
			}
		})
	}
}

func TestE2E_SSHCommand_SingBoxVersion(t *testing.T) {
	t.Parallel()
	for _, srv := range e2eServers {
		t.Run(srv.Role, func(t *testing.T) {
			t.Parallel()
			client, err := sshclient.Connect(srv.Addr, srv.User, sshKeyPath(srv.KeyFile))
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			defer client.Close()
			out, err := client.Run("sing-box version 2>/dev/null | head -1")
			if err != nil {
				t.Fatalf("version: %v", err)
			}
			out = strings.TrimSpace(out)
			t.Logf("%s sing-box: %s", srv.Role, out)
			if !strings.Contains(out, "sing-box") {
				t.Errorf("unexpected version output: %s", out)
			}
		})
	}
}

func TestE2E_KnownHostsRoundTrip(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.SaveKnownHost(&model.KnownHost{Addr: "e2e-test-host:22", Fingerprint: "SHA256:abc123", Trusted: true})
	kh, err := s.GetKnownHost("e2e-test-host")
	if err != nil {
		t.Fatalf("GetKnownHost: %v", err)
	}
	if kh.Fingerprint != "SHA256:abc123" {
		t.Errorf("fingerprint = %s", kh.Fingerprint)
	}
}

func TestE2E_KnownHosts_Normalization(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.SaveKnownHost(&model.KnownHost{Addr: "10.0.0.1:22", Fingerprint: "SHA256:xyz", Trusted: true})
	kh, err := s.GetKnownHost("10.0.0.1")
	if err != nil {
		t.Fatalf("GetKnownHost (no port): %v", err)
	}
	if kh.Fingerprint != "SHA256:xyz" {
		t.Errorf("fingerprint = %s", kh.Fingerprint)
	}
}

func TestE2E_WireGuardKeypair(t *testing.T) {
	t.Parallel()
	priv, pub, err := chain.GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair: %v", err)
	}
	if priv == "" || pub == "" || priv == pub {
		t.Fatalf("bad keypair: priv=%q pub=%q", priv, pub)
	}
	uuid, pass := chain.GenerateStableTUICUserCreds()
	if uuid == "" || pass == "" {
		t.Fatal("empty TUIC creds")
	}
}

func TestE2E_StoreRealPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	s1 := chain.NewStore(path)
	s1.SaveHost(&model.Host{ID: "persist", Addr: "10.0.0.1:22", User: "root"})
	s2 := chain.NewStore(path)
	h, err := s2.GetHost("persist")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if h.Addr != "10.0.0.1:22" {
		t.Errorf("addr = %s", h.Addr)
	}
}

func TestE2E_Takeover_DetectNone(t *testing.T) {
	t.Parallel()
	host := e2eHost(e2eRoleEntry)
	det, err := takeover.DetectVPN(e2eContext(t, 30*time.Second), host, true)
	if err != nil {
		t.Fatalf("DetectVPN: %v", err)
	}
	t.Logf("clean entry detection: %s", det.Type)
	// Entry may already have sing-box from a prior heavy run — only assert detect works.
	if det.Type == takeover.DetectedNone {
		t.Log("entry node clean (no VPN)")
	}
}

func TestE2E_ImportAWG_NoAWG(t *testing.T) {
	t.Parallel()
	host := e2eHost(e2eRoleEntry)
	info := &model.NodeInfo{Host: host}
	res, err := chain.ImportAWGConfigs(host, true, info)
	if err != nil {
		t.Fatalf("ImportAWGConfigs: %v", err)
	}
	if res.ServerConfig != nil || len(res.Imported) > 0 {
		t.Errorf("expected no import on clean node: %+v", res)
	}
}

func TestE2E_Presets_Loaded(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"russia_2026", "pro_2026", "xhttp_max_stealth_2026", "china_2026"} {
		if _, ok := chain.GetPreset(name); !ok {
			t.Errorf("preset %q not loaded", name)
		}
	}
}