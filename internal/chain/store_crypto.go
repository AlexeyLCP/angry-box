package chain

// store_crypto.go — optional at-rest encryption of store.json (CTO-review §6
// CRITICAL finding: SSH privkeys + AWG/Reality/TLS secrets were stored as
// plaintext JSON; stealing the file = compromising the whole VPS fleet).
//
// Design:
//   - A master-key file (32 random bytes, 0600) placed next to the store
//     (default: <storePath>.key) opts the store into encryption. If the key
//     file does not exist, the store is read/written as plaintext JSON
//     (legacy mode) — fully backward compatible, no migration required.
//   - When the key exists, writeStore encrypts the whole JSON payload with
//     AES-256-GCM and prepends a 6-byte magic header so readStore can
//     auto-detect encrypted vs plaintext. readStore checks the magic and
//     decrypts if present.
//   - This protects against backup-leak and disk-theft: a store.json backup
//     without the key file is unreadable. It does NOT protect against a
//     root-attacker on the orchestrator host (who can read the key file +
//     memory); that requires process isolation / HSM, out of scope.
//
// The key file is NOT generated automatically — the operator must explicitly
// create it (e.g. `head -c 32 /dev/urandom > /var/lib/angry-box/store.json.key
// && chmod 600 store.json.key && chown angry-box:angry-box store.json.key`),
// then restart the service. The next writeStore encrypts the store. This
// makes opt-in explicit and avoids surprising operators who rely on the
// store being human-readable for debugging.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// encMagic is the 6-byte header prepended to an encrypted store.json so
// readStore can auto-detect the format. "ABENC1" — Angry-Box Encrypted v1.
const encMagic = "ABENC1"

// masterKeySize is the required AES-256 key length in bytes.
const masterKeySize = 32

// ErrShortMasterKey is returned when the master-key file is present but
// shorter than the required 32 bytes.
var ErrShortMasterKey = errors.New("store: master key file must be 32 bytes")

// masterKeyPath returns the default master-key path: <storePath>.key next
// to the store. Operators can override by placing the key file there.
func masterKeyPath(storePath string) string {
	return storePath + ".key"
}

// loadMasterKey reads the 32-byte master key from keyPath. Returns nil key
// and nil error if the file does not exist (legacy plaintext mode). Returns
// an error if the file exists but is not 32 bytes.
func loadMasterKey(keyPath string) ([]byte, error) {
	data, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		return nil, nil // legacy plaintext mode
	}
	if err != nil {
		return nil, fmt.Errorf("store: read master key: %w", err)
	}
	if len(data) != masterKeySize {
		return nil, ErrShortMasterKey
	}
	return data, nil
}

// encryptStore encrypts data with AES-256-GCM and prepends encMagic so
// readStore can detect the format. Returns the ciphertext (magic + nonce +
// sealed). The nonce is randomly generated per write.
func encryptStore(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("store: encrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: encrypt gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("store: encrypt nonce: %w", err)
	}
	// Layout: magic || nonce || ciphertext+tag
	out := make([]byte, 0, len(encMagic)+len(nonce)+len(data)+gcm.Overhead())
	out = append(out, encMagic...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, data, nil)
	return out, nil
}

// decryptStore reverses encryptStore. Returns an error if data does not
// start with encMagic (so the caller can fall back to plaintext JSON).
func decryptStore(data, key []byte) ([]byte, error) {
	if len(data) < len(encMagic) || string(data[:len(encMagic)]) != encMagic {
		return nil, errors.New("store: not encrypted (missing magic header)")
	}
	rest := data[len(encMagic):]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("store: decrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: decrypt gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(rest) < ns {
		return nil, errors.New("store: decrypt: truncated (no nonce)")
	}
	nonce := rest[:ns]
	ct := rest[ns:]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("store: decrypt: %w", err)
	}
	return plain, nil
}

// isEncrypted reports whether data starts with the encMagic header.
func isEncrypted(data []byte) bool {
	return len(data) >= len(encMagic) && string(data[:len(encMagic)]) == encMagic
}

// GenerateMasterKey writes a fresh 32-byte random master key to keyPath with
// 0600 perms. Exposed so a CLI subcommand can create the key for the
// operator (or a future `angry-box init-key`).
func GenerateMasterKey(keyPath string) error {
	key := make([]byte, masterKeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("store: gen master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(key); err != nil {
		return err
	}
	return os.Chmod(keyPath, 0o600)
}