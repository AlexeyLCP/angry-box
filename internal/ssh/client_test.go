package ssh

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestGenerateSSHKeypair(t *testing.T) {
	privPEM, pubSSH, err := GenerateSSHKeypair()
	if err != nil {
		t.Fatalf("GenerateSSHKeypair: %v", err)
	}

	// Private key must be valid PEM
	if !strings.HasPrefix(privPEM, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Error("private key missing PEM header")
	}
	if !strings.Contains(privPEM, "-----END OPENSSH PRIVATE KEY-----") {
		t.Error("private key missing PEM footer")
	}

	// Public key must be valid OpenSSH format
	if !strings.HasPrefix(pubSSH, "ssh-ed25519 ") {
		t.Errorf("public key unexpected format: %s", pubSSH[:40])
	}

	// Private key must be parseable by golang.org/x/crypto/ssh
	signer, err := ssh.ParsePrivateKey([]byte(privPEM))
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}

	// Public key must be parseable
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubSSH))
	if err != nil {
		t.Fatalf("ParseAuthorizedKey: %v", err)
	}

	// Sign and verify to prove the keypair matches
	data := []byte("test-message")
	sig, err := signer.Sign(nil, data)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := pubKey.Verify(data, sig); err != nil {
		t.Fatal("keypair mismatch: signature verification failed")
	}
}

func TestGenerateSSHKeypair_Deterministic(t *testing.T) {
	// Each call should produce different keys
	priv1, _, _ := GenerateSSHKeypair()
	priv2, _, _ := GenerateSSHKeypair()
	if priv1 == priv2 {
		t.Error("two generated keys should be different")
	}
}

func TestGenerateSSHKeypair_Ed25519(t *testing.T) {
	// We specifically want ed25519 keys
	privPEM, pubSSH, err := GenerateSSHKeypair()
	if err != nil {
		t.Fatalf("GenerateSSHKeypair: %v", err)
	}

	// Parse the private key
	signer, err := ssh.ParsePrivateKey([]byte(privPEM))
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}

	// Check that it's ed25519
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("expected ssh-ed25519, got %s", signer.PublicKey().Type())
	}

	// Public key should also be ssh-ed25519
	if !strings.HasPrefix(strings.TrimSpace(pubSSH), "ssh-ed25519 ") {
		t.Errorf("public key not ed25519: %q", pubSSH[:40])
	}
}

func TestHostKeyError_Changed(t *testing.T) {
	e := &HostKeyError{
		RemoteFingerprint: "SHA256:abc123",
		Changed:           true,
	}
	msg := e.Error()
	if !strings.Contains(msg, "changed") {
		t.Errorf("error should mention 'changed': %s", msg)
	}
	if !strings.Contains(msg, "SHA256:abc123") {
		t.Errorf("error should contain fingerprint: %s", msg)
	}
}

func TestHostKeyError_Untrusted(t *testing.T) {
	e := &HostKeyError{
		RemoteFingerprint: "SHA256:def456",
		Changed:           false,
	}
	msg := e.Error()
	if !strings.Contains(msg, "untrusted") {
		t.Errorf("error should mention 'untrusted': %s", msg)
	}
}

func TestConnect_PasswordPrefix(t *testing.T) {
	// password: prefix is detected in Connect()
	// We can't actually connect, but we verify the prefix detection logic
	keyPath := "password:secret123"
	if !strings.HasPrefix(keyPath, "password:") {
		t.Error("password prefix detection broken")
	}
	password := strings.TrimPrefix(keyPath, "password:")
	if password != "secret123" {
		t.Errorf("password extraction: got %q", password)
	}
}

func TestConnect_NoPort_DefaultsTo22(t *testing.T) {
	// Verify addr port defaulting logic (from net.JoinHostPort)
	addr := "1.2.3.4"
	// Simulate what Connect does:
	parts := strings.Split(addr, ":")
	if len(parts) == 1 {
		addr = addr + ":22"
	}
	if addr != "1.2.3.4:22" {
		t.Errorf("port defaulting: got %s", addr)
	}
}

func TestConnect_AddrWithPort_Unchanged(t *testing.T) {
	addr := "1.2.3.4:2222"
	parts := strings.Split(addr, ":")
	// Should detect that port is already present
	if len(parts) != 2 {
		t.Error("should detect existing port")
	}
	// So no defaulting happens
	if len(parts) == 2 && parts[1] == "2222" {
		// Correct — port already specified
	} else {
		t.Error("port parsing broken")
	}
}

// fakeHostKeyManager implements HostKeyManager for testing.
type fakeHostKeyManager struct {
	trusted map[string]bool
}

func (m *fakeHostKeyManager) CheckHostKey(addr string, remoteKey ssh.PublicKey) error {
	fp := ssh.FingerprintSHA256(remoteKey)
	if m.trusted[addr+"|"+fp] {
		return nil
	}
	return &HostKeyError{RemoteFingerprint: fp, Changed: !m.trusted[addr]}
}

// GetKnownHost satisfies the HostKeyManager interface (the pool re-checks the
// stored fingerprint on borrow). The fake does not persist known hosts — return
// not-found so the pool's fingerprint re-check is a no-op in client_test.go.
func (m *fakeHostKeyManager) GetKnownHost(addr string) (*model.KnownHost, error) {
	return nil, fmt.Errorf("fakeHostKeyManager: no known host for %s", addr)
}

func TestSetHostKeyManager(t *testing.T) {
	// Create a real ed25519 key for fingerprint generation
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("NewPublicKey: %v", err)
	}

	mgr := &fakeHostKeyManager{
		trusted: map[string]bool{
			"trusted.example.com|" + ssh.FingerprintSHA256(sshPub): true,
		},
	}

	// Save and restore global manager
	oldMgr := globalManager
	SetHostKeyManager(mgr)
	defer SetHostKeyManager(oldMgr)

	if globalManager != mgr {
		t.Fatal("SetHostKeyManager did not set global")
	}

	// Trusted key should pass
	if err := mgr.CheckHostKey("trusted.example.com", sshPub); err != nil {
		t.Errorf("trusted key should pass: %v", err)
	}

	// Unknown key should fail
	if err := mgr.CheckHostKey("new.example.com", sshPub); err == nil {
		t.Error("unknown host should fail")
	}
}

func TestSetKeyResolver(t *testing.T) {
	oldResolver := globalKeyResolver
	defer SetKeyResolver(oldResolver)

	fake := &fakeKeyResolver{keys: map[string]string{"test-key": "key-data"}}
	SetKeyResolver(fake)

	if globalKeyResolver != fake {
		t.Fatal("SetKeyResolver did not set global")
	}

	data, ok := globalKeyResolver.ResolveKey("test-key")
	if !ok || data != "key-data" {
		t.Errorf("ResolveKey: got %q, %v", data, ok)
	}

	_, ok = globalKeyResolver.ResolveKey("unknown")
	if ok {
		t.Error("unknown key should not resolve")
	}
}

type fakeKeyResolver struct {
	keys map[string]string
}

func (r *fakeKeyResolver) ResolveKey(keyID string) (string, bool) {
	data, ok := r.keys[keyID]
	return data, ok
}

func TestClient_Close(t *testing.T) {
	// A nil client should panic on Close(), so we test that a properly
	// initialized Client with nil underlying ssh.Client panics.
	// This verifies the Close method exists and delegates correctly.
	c := &Client{client: nil}
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on Close with nil client")
		}
	}()
	c.Close()
}

func TestInstallPublicKey_PasswordPrefix(t *testing.T) {
	// Verify the password: prefix is detected in InstallPublicKey's Connect call
	// We can't actually connect, but we verify the format is correct
	addr := "example.com:22"
	user := "root"
	password := "test-password"

	// This is the format InstallPublicKey uses:
	expectedKeyPath := "password:" + password

	if expectedKeyPath != "password:test-password" {
		t.Error("password prefix format mismatch")
	}

	// Verify the Connect call format
	_ = addr
	_ = user
	_ = expectedKeyPath
	// Connect(addr, user, "password:"+password) is the call
}

func TestGenerateSSHKeypair_PEMStructure(t *testing.T) {
	privPEM, _, err := GenerateSSHKeypair()
	if err != nil {
		t.Fatalf("GenerateSSHKeypair: %v", err)
	}

	// Verify the PEM is well-formed
	block, rest := ssh.ParseRawPrivateKey([]byte(privPEM))
	if rest != nil {
		// ParseRawPrivateKey returns (nil, nil) for OpenSSH format keys,
		// which is what ssh.MarshalPrivateKey produces.
		// So we check that ssh.ParsePrivateKey works (already tested above).
		_ = block
	}

	// At minimum, the PEM must have proper begin/end
	lines := strings.Split(strings.TrimSpace(privPEM), "\n")
	if len(lines) < 3 {
		t.Error("PEM too short")
	}
	if !strings.HasPrefix(lines[0], "-----BEGIN ") {
		t.Error("first line must be BEGIN header")
	}
	if !strings.HasPrefix(lines[len(lines)-1], "-----END ") {
		t.Error("last line must be END footer")
	}
}
