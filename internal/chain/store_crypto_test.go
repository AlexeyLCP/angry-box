package chain

// store_crypto_test.go — tests for the optional at-rest encryption of
// store.json (CTO-review §6 CRITICAL: secrets were plaintext; key file opts
// into AES-256-GCM of the whole payload; absence of key = legacy plaintext).

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := make([]byte, masterKeySize)
	for i := range key {
		key[i] = byte(i) + 1
	}
	plain := []byte(`{"hosts":[{"id":"h1","addr":"1.1.1.1:22"}]}`)
	ct, err := encryptStore(plain, key)
	if err != nil {
		t.Fatalf("encryptStore: %v", err)
	}
	if !isEncrypted(ct) {
		t.Error("ciphertext missing ABENC1 magic header")
	}
	if bytes.Equal(plain, ct) {
		t.Error("ciphertext equals plaintext — not encrypted")
	}
	pt, err := decryptStore(ct, key)
	if err != nil {
		t.Fatalf("decryptStore: %v", err)
	}
	if !bytes.Equal(plain, pt) {
		t.Errorf("round-trip mismatch: want %q, got %q", plain, pt)
	}
}

func TestDecryptStore_WrongKey(t *testing.T) {
	key := make([]byte, masterKeySize)
	key[0] = 0x01
	ct, _ := encryptStore([]byte("secret"), key)
	wrong := make([]byte, masterKeySize)
	wrong[0] = 0x02
	if _, err := decryptStore(ct, wrong); err == nil {
		t.Error("decrypt with wrong key should fail (GCM auth tag)")
	}
}

func TestDecryptStore_PlaintextNoMagic(t *testing.T) {
	key := make([]byte, masterKeySize)
	if _, err := decryptStore([]byte(`{"a":1}`), key); err == nil {
		t.Error("decrypt of plaintext (no magic) should error")
	}
}

func TestStore_NoKey_PlaintextMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	st := NewStore(path)
	_ = st.SaveHost(&model.Host{ID: "h1", Addr: "1.1.1.1:22"})
	// No .key file → on-disk is plaintext JSON.
	data, _ := os.ReadFile(path)
	if isEncrypted(data) {
		t.Error("store written encrypted without a master key")
	}
	if !bytes.Contains(data, []byte(`"h1"`)) {
		t.Errorf("plaintext store missing host id; got %q", data)
	}
}

func TestStore_WithKey_EncryptedOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := GenerateMasterKey(masterKeyPath(path)); err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	st := NewStore(path)
	_ = st.SaveHost(&model.Host{ID: "h1", Addr: "1.1.1.1:22"})
	data, _ := os.ReadFile(path)
	if !isEncrypted(data) {
		t.Errorf("store not encrypted despite master key; got %q", data)
	}
	if bytes.Contains(data, []byte(`"h1"`)) {
		t.Error("encrypted store leaks plaintext host id on disk")
	}
	// Read back through the store API decrypts transparently.
	st2 := NewStore(path)
	h, err := st2.GetHost("h1")
	if err != nil {
		t.Fatalf("GetHost after decrypt: %v", err)
	}
	if h.Addr != "1.1.1.1:22" {
		t.Errorf("GetHost addr = %q, want 1.1.1.1:22", h.Addr)
	}
}

func TestStore_KeyDeleted_EncryptedStoreUnreadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := GenerateMasterKey(masterKeyPath(path)); err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	st := NewStore(path)
	_ = st.SaveHost(&model.Host{ID: "h1", Addr: "1.1.1.1:22"})
	// Operator deletes the key without decrypting first.
	if err := os.Remove(masterKeyPath(path)); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	st2 := NewStore(path)
	_, err := st2.GetHost("h1")
	if err == nil {
		t.Error("GetHost on encrypted store without key should fail")
	}
}

func TestStore_ShortKey_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	// Write a too-short key file.
	if err := os.WriteFile(masterKeyPath(path), []byte("short"), 0o600); err != nil {
		t.Fatalf("write short key: %v", err)
	}
	st := NewStore(path)
	err := st.SaveHost(&model.Host{ID: "h1", Addr: "1.1.1.1:22"})
	if err == nil {
		t.Error("SaveHost with a short master key should error")
	}
}