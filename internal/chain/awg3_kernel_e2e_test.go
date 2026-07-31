//go:build e2eawg3

// awg3_kernel_e2e_test.go — live E2E for the kernel-AWG3 render path on n1
// (144.31.224.212, our dedicated test VPS, root@, key ~/.ssh/id_ed25519 — NOT the
// prod e2eServers in e2e_helpers_test.go which must never be touched).
//
// Verifies the contract from PROGRESS §43 / AGENTS #5 revision: the Go
// RenderServerAWGConf output, with AWG3 material, is accepted by the amnezia-box
// kernel module (PR #192) + amneziaawg-tools v3.0 on n1 — awg-quick up applies
// HPK/CPM/RAT and `awg show` confirms them. This exercises the EXACT byte output
// the orchestrator ships, not a hand-written conf.
//
// Run (from a machine with SSH to n1, after the n1 kernel module + tools are
// staged at >= 3.0 — see PROGRESS §43 staging steps):
//
//	go test ./internal/chain/ -tags e2eawg3 -run TestE2EAWG3_KernelConf -v -timeout 120s
package chain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// TestMain wires the store-backed HostKeyManager (TOFU) so sshclient.Connect to
// n1 succeeds — the e2e tag's TestMain is not compiled under -tags e2eawg3, so
// this test binary needs its own. The store is a temp file; n1's host key is
// captured on first connect (TOFU accept-new semantics via the store manager).
func TestMain(m *testing.M) {
	store := chain.NewStore(filepath.Join(os.TempDir(), "angry-box-awg3-e2e-store.json"))
	sshclient.SetHostKeyManager(store)
	sshclient.SetKeyResolver(store)
	os.Exit(m.Run())
}

// n1 is the single dedicated test VPS (AGENTS.md Test Servers). Hardcoded rather
// than reusing the prod-GCloud e2eServers, which are off-limits for any deploy.
const (
	n1Addr = "144.31.224.212:22"
	n1User = "root"
	n1Key  = "id_ed25519"
)

func n1Client(t *testing.T) *sshclient.Client {
	t.Helper()
	home, _ := os.UserHomeDir()
	client, err := sshclient.Connect(n1Addr, n1User, filepath.Join(home, ".ssh", n1Key))
	if err != nil {
		t.Skipf("n1 unreachable (%v) — kernel-AWG3 E2E needs SSH to %s", err, n1Addr)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// TestE2EAWG3_KernelConf renders a server awg0.conf through the orchestrator's
// kernel-AWG3 path (RenderServerAWGConf with AWG3 material) and proves n1's
// awg-quick accepts it with HPK applied. This is the live gate for slice 2: if
// it passes, the orchestrator-generated conf is kernel-valid end-to-end.
func TestE2EAWG3_KernelConf(t *testing.T) {
	client := n1Client(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// A server keypair + a peer (client) keypair, generated on n1 so the keys
	// are real (the kernel validates key formats). Reuse `awg genkey`.
	mustRun := func(label string) string {
		t.Helper()
		out, err := client.Run("awg genkey")
		if err != nil || strings.TrimSpace(out) == "" {
			t.Fatalf("awg genkey (%s): %v out=%q", label, err, out)
		}
		return strings.TrimSpace(out)
	}
	serverPriv := mustRun("server priv")
	peerPriv := mustRun("peer priv")
	peerPub, err := client.Run("echo " + peerPriv + " | awg pubkey")
	if err != nil {
		t.Fatalf("awg pubkey: %v", err)
	}
	peerPub = strings.TrimSpace(peerPub)

	// AWG3 material: a hex HPK (32 bytes). awg3HPKHexToBase64 is internal, so
	// derive the base64 form on n1 via `echo <hex> | xxd -r -p | base64` to match
	// exactly what writeAWG3ConfLines emits.
	hpkHex := strings.Repeat("ab", 32) // 32-byte deterministic key
	hpkB64Out, err := client.Run("printf " + hpkHex + " | xxd -r -p | base64 | tr -d '\\n'")
	if err != nil {
		t.Fatalf("hex→base64 on n1: %v", err)
	}
	hpkB64 := strings.TrimSpace(hpkB64Out)

	// Render the server conf through the orchestrator's kernel-AWG3 path.
	conf := chain.RenderServerAWGConf(chain.AWGServerConfParams{
		ServerPrivateKey: serverPriv,
		ListenPort:       51842,
		TunnelAddress:    "10.8.3.1/24",
		// Amnezia block is REQUIRED for a HPK conf: header protection needs
		// S1-S4 >= 12 (HeaderCipherNonceSize) — without it the kernel rejects
		// HPK with "Invalid argument" (S1 must be more then 12). Mirrors the
		// awg3 presets (S1-S4=24, H=12/13/14/15 unique). BuildAWGAmnezia needs
		// an *AWGPreset; inline an AmneziaOptions directly here for the test.
		Amnezia: &config.AmneziaOptions{
			JC: 120, JMIN: 50, JMAX: 1000,
			S1: 24, S2: 24, S3: 24, S4: 24,
			H1: "12", H2: "13", H3: "14", H4: "15",
		},
		AWG3: &chain.AWGObfsMaterial{
			AWG3Mode:               true,
			HeaderProtectionKey:    hpkHex,
			ContentPaddingAddition: "1-16",
			RekeyAfterTime:         "90-110",
		},
		Peers: []chain.AWGServerPeer{{PublicKey: peerPub, AllowedIPs: "10.8.3.2/32"}},
	})
	t.Logf("rendered conf:\n%s", conf)

	// Ship the conf to n1 and bring it up as a dedicated test unit (not awg0 —
	// avoid clashing with any leftover state; awg-quick@<name> works for any
	// interface name).
	const unit = "awg3e2e"
	const confPath = "/etc/amnezia/amneziawg/" + unit + ".conf"
	if err := client.UploadText(ctx, conf, confPath, 0o600); err != nil {
		t.Fatalf("upload conf: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.Run("awg-quick down " + unit + " 2>/dev/null; rm -f " + confPath)
	})

	// awg-quick up — this is the real gate. The new kernel module + tools must
	// accept the HPK-bearing conf without "Invalid argument" or "Line
	// unrecognized".
	out, err := client.Run("awg-quick up " + unit + " 2>&1")
	if err != nil {
		t.Fatalf("awg-quick up failed (kernel-AWG3 conf rejected):\n%s\nerr: %v", out, err)
	}
	t.Logf("awg-quick up:\n%s", out)

	// Verify HPK/CPM/RAT were applied to the running interface.
	show, err := client.Run("awg show " + unit + " 2>&1")
	if err != nil {
		t.Fatalf("awg show: %v\n%s", err, show)
	}
	t.Logf("awg show:\n%s", show)
	if !strings.Contains(show, "header protection key: "+hpkB64) {
		t.Errorf("HPK not applied: awg show has no 'header protection key: %s'\n%s", hpkB64, show)
	}
	if !strings.Contains(show, "content padding addition: 1-16") {
		t.Errorf("ContentPaddingAddition not applied\n%s", show)
	}
	if !strings.Contains(show, "rekey after time: 90-110") {
		t.Errorf("RekeyAfterTime not applied\n%s", show)
	}
}
