package chain

// backup_crypto.go — passphrase-based encryption for offsite backups (P2a).
//
// This is a SEPARATE crypto path from store_crypto.go (at-rest AES-256-GCM
// with a raw 32-byte master-key file). The offsite backup MUST NOT use the
// master key: the master-key file never leaves the host (AGENTS.md security
// boundary — stealing it + a backup would compromise the whole fleet). Instead
// the offsite blob is encrypted with a key derived from an operator-chosen
// PASSPHRASE via scrypt, so the blob is restorable on a different orchestrator
// (or from cold storage) with only the passphrase, never the master key.
//
// Format (backupBlobMagic = "ABBKP1", Angry-Box Backup Passphrase v1):
//
//	magic(6) || salt(16) || N(4 BE) || r(2 BE) || p(2 BE) || nonce(12) || ct+tag
//
// scrypt params default N=2^16, r=8, p=1 (~64 MB, ~100 ms per op) — fits a
// periodic backup (default every 6h). The params are stored in the blob so a
// future tune (lower N for weak boxes, higher N for paranoia) does not break
// old blobs: DecryptBackup reads N/r/p from the header. Salt is random per
// EncryptBackup so two blobs of the same store under the same passphrase
// differ (no ciphertext-equality leak).
//
// Mirrors the GCM layout of encryptStore/decryptStore (store_crypto.go:75/98)
// but with a different magic + a scrypt-derived key instead of a raw key.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

// backupBlobMagic is the 6-byte header identifying a passphrase-encrypted
// offsite backup blob (distinct from the at-rest "ABENC1").
const backupBlobMagic = "ABBKP1"

// Default scrypt parameters. N=2^16 keeps memory ~64MB and ~100ms on a modern
// CPU — acceptable for a periodic (6h) backup. Stored per-blob so they are
// tunable without breaking old blobs.
const (
	backupScryptN = 1 << 16 // 65536
	backupScryptR = 8
	backupScryptP = 1
	backupKeyLen  = 32 // AES-256
	backupSaltLen = 16
	backupNonceLen = 12 // GCM standard
)

// ErrBackupBlob is a sentinel for a malformed/unrecognized backup blob.
var ErrBackupBlob = errors.New("backup: not a passphrase-encrypted blob (bad magic)")

// EncryptBackup encrypts plaintext with a passphrase-derived AES-256-GCM key
// using the default scrypt parameters (N=2^16, r=8, p=1 — ~64MB/~100ms). It is
// a thin wrapper around EncryptBackupWithParams preserving the original
// signature so the existing callers/tests do not change. For a tunable memory
// cost (e.g. a weak orchestrator), call EncryptBackupWithParams directly.
func EncryptBackup(plaintext []byte, passphrase string) ([]byte, error) {
	return EncryptBackupWithParams(plaintext, passphrase, backupScryptN, backupScryptR, backupScryptP)
}

// EncryptBackupWithParams is the parameterized variant: N/r/p are the scrypt
// cost params, stored in the blob header so DecryptBackup reads them back
// (blobs encrypted with different N decrypt uniformly). N must be a power of 2
// >= 2 (scrypt requirement); the package default (backupScryptN) is recommended.
// A lower N trades memory/brute-force resistance for speed — operator's choice.
// The passphrase never leaves the caller; only the derived key (via a random
// salt stored in the blob) is used. Returns the self-describing ABBKP1 blob.
func EncryptBackupWithParams(plaintext []byte, passphrase string, N, r, p int) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("backup: passphrase is empty")
	}
	if N <= 0 {
		N = backupScryptN
	}
	if r <= 0 {
		r = backupScryptR
	}
	if p <= 0 {
		p = backupScryptP
	}
	salt := make([]byte, backupSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("backup: salt: %w", err)
	}
	key, err := deriveBackupKey(passphrase, salt, N, r, p)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: gcm: %w", err)
	}
	nonce := make([]byte, backupNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("backup: nonce: %w", err)
	}

	// Layout: magic || salt || N || r || p || nonce || ct+tag
	out := make([]byte, 0, len(backupBlobMagic)+backupSaltLen+8+backupNonceLen+len(plaintext)+gcm.Overhead())
	out = append(out, backupBlobMagic...)
	out = append(out, salt...)
	out = appendUint32BE(out, uint32(N))
	out = appendUint16BE(out, uint16(r))
	out = appendUint16BE(out, uint16(p))
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return out, nil
}

// DecryptBackup reverses EncryptBackup. Returns an error if the blob is not an
// ABBKP1 blob, the passphrase is wrong, or the blob is truncated/corrupted.
// The wrong-passphrase case surfaces as a GCM Open error (auth tag mismatch).
func DecryptBackup(blob []byte, passphrase string) ([]byte, error) {
	if len(blob) < len(backupBlobMagic) || string(blob[:len(backupBlobMagic)]) != backupBlobMagic {
		return nil, ErrBackupBlob
	}
	rest := blob[len(backupBlobMagic):]
	// salt(16) || N(4) || r(2) || p(2) = 24 bytes header after magic.
	if len(rest) < backupSaltLen+8+backupNonceLen {
		return nil, errors.New("backup: truncated blob (header)")
	}
	salt := rest[:backupSaltLen]
	rest = rest[backupSaltLen:]
	N := binary.BigEndian.Uint32(rest[:4])
	r := binary.BigEndian.Uint16(rest[4:6])
	p := binary.BigEndian.Uint16(rest[6:8])
	rest = rest[8:]
	nonce := rest[:backupNonceLen]
	ct := rest[backupNonceLen:]

	key, err := deriveBackupKey(passphrase, salt, int(N), int(r), int(p))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: gcm: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("backup: decrypt (wrong passphrase or corrupted): %w", err)
	}
	return plain, nil
}

// deriveBackupKey derives a 32-byte AES-256 key from a passphrase + salt via
// scrypt. Wraps scrypt.Key with the package's chosen params.
func deriveBackupKey(passphrase string, salt []byte, N, r, p int) ([]byte, error) {
	return scrypt.Key([]byte(passphrase), salt, N, r, p, backupKeyLen)
}

// appendUint32BE appends a big-endian uint32 to out.
func appendUint32BE(out []byte, v uint32) []byte {
	return append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// appendUint16BE appends a big-endian uint16 to out.
func appendUint16BE(out []byte, v uint16) []byte {
	return append(out, byte(v>>8), byte(v))
}

// IsBackupBlob reports whether data starts with the ABBKP1 magic header — used
// by the restore path to decide whether to decrypt (passphrase blob) or pass
// through to the existing plaintext ImportStore/ImportNode detection.
func IsBackupBlob(data []byte) bool {
	return len(data) >= len(backupBlobMagic) && string(data[:len(backupBlobMagic)]) == backupBlobMagic
}