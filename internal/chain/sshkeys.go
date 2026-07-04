package chain

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// DeriveKeyFingerprint parses a PEM-encoded private key and returns the last
// 8 characters of its SHA256 fingerprint, prefixed with an ellipsis
// (e.g. "…ab12cd34"). Computed once when an SSHKeyEntry is created so the
// dropdown and Settings render without re-parsing PEM per render.
func DeriveKeyFingerprint(privPEM string) (string, error) {
	privPEM = strings.TrimSpace(privPEM)
	if privPEM == "" {
		return "", fmt.Errorf("empty key data")
	}
	signer, err := ssh.ParsePrivateKey([]byte(privPEM))
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	full := ssh.FingerprintSHA256(signer.PublicKey()) // "SHA256:abcd...wxyz"
	// Strip the "SHA256:" prefix, keep the last 8 hex/base64 chars.
	hex := strings.TrimPrefix(full, "SHA256:")
	if len(hex) < 8 {
		return "", fmt.Errorf("fingerprint too short: %q", full)
	}
	return "…" + hex[len(hex)-8:], nil
}