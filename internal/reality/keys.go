package reality

import (
	"fmt"
	"strings"

	"github.com/alexeylcp/lucx-core/internal/ssh"
)

// KeyPair holds a Reality X25519 key pair generated on a server.
type KeyPair struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

// GenerateKeys runs "xray x25519" on the server via SSH and returns the key pair.
// Supports both old format ("Private key:", "Public key:") and new format
// ("PrivateKey:", "Password (PublicKey):").
func GenerateKeys(client *ssh.Client) (*KeyPair, error) {
	out, err := client.Exec("xray x25519")
	if err != nil {
		return nil, fmt.Errorf("xray x25519: %w", err)
	}

	kp := &KeyPair{}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Private key:"):
			kp.PrivateKey = strings.TrimSpace(strings.TrimPrefix(line, "Private key:"))
		case strings.HasPrefix(line, "Public key:"):
			kp.PublicKey = strings.TrimSpace(strings.TrimPrefix(line, "Public key:"))
		case strings.HasPrefix(line, "PrivateKey:"):
			kp.PrivateKey = strings.TrimSpace(strings.TrimPrefix(line, "PrivateKey:"))
		case strings.HasPrefix(line, "Password (PublicKey):"):
			kp.PublicKey = strings.TrimSpace(strings.TrimPrefix(line, "Password (PublicKey):"))
		}
	}

	if kp.PrivateKey == "" || kp.PublicKey == "" {
		return nil, fmt.Errorf("xray x25519: failed to parse output: %s", out)
	}

	return kp, nil
}
