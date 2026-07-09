package chain

// backup_crypto_test.go + backup_offsite_test.go — tests for the P2a offsite
// backup crypto (passphrase/scrypt/AES-GCM) and the PushOffsiteBackup core.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestEncryptDecryptBackup_Roundtrip — encrypt then decrypt returns the
// original plaintext, and the blob carries the ABBKP1 magic.
func TestEncryptDecryptBackup_Roundtrip(t *testing.T) {
	plain := []byte(`{"store": "angry-box full panel state"}`)
	blob, err := EncryptBackup(plain, "correct horse battery staple")
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}
	if !IsBackupBlob(blob) {
		t.Fatal("blob does not start with ABBKP1 magic")
	}
	if bytes.Equal(blob, plain) {
		t.Fatal("blob equals plaintext (not encrypted)")
	}
	got, err := DecryptBackup(blob, "correct horse battery staple")
	if err != nil {
		t.Fatalf("DecryptBackup: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, plain)
	}
}

// TestDecryptBackup_WrongPassphrase — a different passphrase fails (GCM auth).
func TestDecryptBackup_WrongPassphrase(t *testing.T) {
	blob, err := EncryptBackup([]byte("secret"), "right-pass")
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}
	if _, err := DecryptBackup(blob, "wrong-pass"); err == nil {
		t.Fatal("DecryptBackup with wrong passphrase: expected error, got nil")
	}
}

// TestDecryptBackup_BadMagic — non-ABBKP1 data is rejected with ErrBackupBlob.
func TestDecryptBackup_BadMagic(t *testing.T) {
	if _, err := DecryptBackup([]byte("ABENC1someotherdata"), "x"); err != ErrBackupBlob {
		t.Fatalf("expected ErrBackupBlob, got %v", err)
	}
	if _, err := DecryptBackup([]byte("plaintext json"), "x"); err != ErrBackupBlob {
		t.Fatalf("expected ErrBackupBlob for plaintext, got %v", err)
	}
}

// TestEncryptBackup_EmptyPassphrase — empty passphrase is refused.
func TestEncryptBackup_EmptyPassphrase(t *testing.T) {
	if _, err := EncryptBackup([]byte("x"), ""); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

// TestEncryptBackup_EmptyPlaintext — empty plaintext roundtrips (a valid edge
// case: an empty store should still produce a restorable blob).
func TestEncryptBackup_EmptyPlaintext(t *testing.T) {
	blob, err := EncryptBackup(nil, "pass")
	if err != nil {
		t.Fatalf("EncryptBackup(nil): %v", err)
	}
	got, err := DecryptBackup(blob, "pass")
	if err != nil {
		t.Fatalf("DecryptBackup: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty plaintext, got %q", got)
	}
}

// TestEncryptBackup_DifferentSalts — two encryptions of the same plaintext
// with the same passphrase produce different blobs (random salt + nonce).
func TestEncryptBackup_DifferentSalts(t *testing.T) {
	a, _ := EncryptBackup([]byte("same"), "pass")
	b, _ := EncryptBackup([]byte("same"), "pass")
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext are identical (salt/nonce not random)")
	}
}

// TestEncryptBackupWithParams_LowN — a lower scrypt N (2^12, faster/less memory)
// still roundtrips, and the blob decrypts with the default DecryptBackup (which
// reads N from the blob header, so cross-N decrypt works).
func TestEncryptBackupWithParams_LowN(t *testing.T) {
	plain := []byte("tunable scrypt cost")
	blob, err := EncryptBackupWithParams(plain, "pass", 1<<12, backupScryptR, backupScryptP)
	if err != nil {
		t.Fatalf("EncryptBackupWithParams: %v", err)
	}
	if !IsBackupBlob(blob) {
		t.Fatal("blob missing ABBKP1 magic")
	}
	got, err := DecryptBackup(blob, "pass")
	if err != nil {
		t.Fatalf("DecryptBackup: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch: %q vs %q", got, plain)
	}
}

// TestEncryptBackupWithParams_DefaultsOnZero — passing N/r/p <= 0 falls back to
// the package defaults (blob is equivalent to EncryptBackup output format).
func TestEncryptBackupWithParams_DefaultsOnZero(t *testing.T) {
	plain := []byte("defaults")
	blob, err := EncryptBackupWithParams(plain, "pass", 0, 0, 0)
	if err != nil {
		t.Fatalf("EncryptBackupWithParams: %v", err)
	}
	got, err := DecryptBackup(blob, "pass")
	if err != nil {
		t.Fatalf("DecryptBackup: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch: %q vs %q", got, plain)
	}
}

// TestDecryptBackup_CrossN — a blob encrypted with N=2^14 decrypts correctly
// (DecryptBackup reads N from the header, so it works regardless of the N used).
func TestDecryptBackup_CrossN(t *testing.T) {
	plain := []byte("cross-N decrypt")
	blob, err := EncryptBackupWithParams(plain, "pass", 1<<14, backupScryptR, backupScryptP)
	if err != nil {
		t.Fatalf("EncryptBackupWithParams: %v", err)
	}
	got, err := DecryptBackup(blob, "pass")
	if err != nil {
		t.Fatalf("DecryptBackup cross-N: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch: %q vs %q", got, plain)
	}
}

// TestIsBackupBlob — the magic detector works on prefixes and non-prefixes.
func TestIsBackupBlob(t *testing.T) {
	if !IsBackupBlob([]byte("ABBKP1rest")) {
		t.Fatal("ABBKP1 prefix not detected")
	}
	if IsBackupBlob([]byte("ABENC1rest")) {
		t.Fatal("ABENC1 falsely detected as ABBKP1")
	}
	if IsBackupBlob([]byte("")) {
		t.Fatal("empty data falsely detected")
	}
}

// --- PushOffsiteBackup ---

// TestPushOffsiteBackup_OK — a full push exports+encrypts+uploads and stamps
// LastBackupAt. Uses the existing fakeConnector + fakeSSHClient (records the
// upload) so no real SSH happens.
func TestPushOffsiteBackup_OK(t *testing.T) {
	st := newTestStore(t)
	if err := st.SaveHost(&model.Host{ID: "src", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k"}); err != nil {
		t.Fatalf("SaveHost: %v", err)
	}
	cfg := &model.OffsiteBackupConfig{
		Host: "offsite.example:22", User: "backup", RemotePath: "/home/bk/ab.abbkp", Passphrase: "secretpass",
	}

	client := newFakeSSH() // no rules needed; UploadText just records
	conn := newFakeConnector(client)

	if err := PushOffsiteBackup(context.Background(), st, cfg, conn); err != nil {
		t.Fatalf("PushOffsiteBackup: %v", err)
	}
	uploads := client.Uploads()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(uploads))
	}
	u := uploads[0]
	if u.Path != "/home/bk/ab.abbkp" {
		t.Fatalf("upload path = %q", u.Path)
	}
	if u.Mode != 0o600 {
		t.Fatalf("upload mode = %o, want 600", u.Mode)
	}
	if !IsBackupBlob([]byte(u.Content)) {
		t.Fatal("uploaded content is not an ABBKP1 blob")
	}
	// The uploaded blob must decrypt (with the right passphrase) to the exported
	// store (which contains our host). This is the real end-to-end check: the
	// offsite blob is restorable.
	plain, err := DecryptBackup([]byte(u.Content), "secretpass")
	if err != nil {
		t.Fatalf("DecryptBackup of uploaded blob: %v", err)
	}
	if !strings.Contains(string(plain), `"src"`) {
		t.Fatalf("decrypted blob does not contain the saved host (got %q)", plain)
	}
	if cfg.LastBackupAt.IsZero() {
		t.Fatal("LastBackupAt not stamped")
	}
}

// TestPushOffsiteBackup_NoTarget — missing host/path is rejected before any
// SSH connection (cheap validation, no network attempt).
func TestPushOffsiteBackup_NoTarget(t *testing.T) {
	st := newTestStore(t)
	conn := newFakeConnector(newFakeSSH())
	cfg := &model.OffsiteBackupConfig{Host: "", RemotePath: "/p", Passphrase: "pass"}
	if err := PushOffsiteBackup(context.Background(), st, cfg, conn); err == nil {
		t.Fatal("expected error for empty host")
	}
}

// TestPushOffsiteBackup_NoPassphrase — empty passphrase is rejected.
func TestPushOffsiteBackup_NoPassphrase(t *testing.T) {
	st := newTestStore(t)
	conn := newFakeConnector(newFakeSSH())
	cfg := &model.OffsiteBackupConfig{Host: "h:22", RemotePath: "/p", Passphrase: ""}
	if err := PushOffsiteBackup(context.Background(), st, cfg, conn); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

// TestPushOffsiteBackup_ConnectFails — a failing connector surfaces the error
// and does NOT stamp LastBackupAt.
func TestPushOffsiteBackup_ConnectFails(t *testing.T) {
	st := newTestStore(t)
	cfg := &model.OffsiteBackupConfig{Host: "down.example:22", RemotePath: "/p", Passphrase: "pass"}
	conn := failingConnector(testErr("ssh: dial: connection refused"))
	if err := PushOffsiteBackup(context.Background(), st, cfg, conn); err == nil {
		t.Fatal("expected error from failing connector")
	}
	if !cfg.LastBackupAt.IsZero() {
		t.Fatal("LastBackupAt stamped despite a failed push")
	}
}

// testErr is a trivial error for tests (local to package chain to avoid an
// extra import).
type testErr string

func (e testErr) Error() string { return string(e) }