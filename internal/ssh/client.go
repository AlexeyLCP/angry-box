package ssh

import (
	"bytes"
	"crypto/ed25519"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostKeyManager is used to verify remote host keys.
type HostKeyManager interface {
	CheckHostKey(addr string, remoteKey ssh.PublicKey) error
}

var globalManager HostKeyManager

// SetHostKeyManager sets the global host key manager.
func SetHostKeyManager(m HostKeyManager) {
	globalManager = m
}

// HostKeyError indicates a problem with the remote host key (mismatch or untrusted).
type HostKeyError struct {
	RemoteFingerprint string
	Changed           bool
}

func (e *HostKeyError) Error() string {
	if e.Changed {
		return fmt.Sprintf("host key changed! new fingerprint: %s", e.RemoteFingerprint)
	}
	return fmt.Sprintf("host key untrusted: %s", e.RemoteFingerprint)
}

// Client wraps an SSH connection and provides convenience methods.
type Client struct {
	client *ssh.Client
}

// Connect establishes an SSH connection to host using key-based or password authentication.
func Connect(addr, user, keyPath string) (*Client, error) {
	var authMethod ssh.AuthMethod

	if strings.HasPrefix(keyPath, "password:") {
		pass := strings.TrimPrefix(keyPath, "password:")
		authMethod = ssh.Password(pass)
	} else {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("ssh: read key %q: %w", keyPath, err)
		}

		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("ssh: parse key: %w", err)
		}
		authMethod = ssh.PublicKeys(signer)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			authMethod,
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			if globalManager != nil {
				return globalManager.CheckHostKey(addr, key)
			}
			// Fallback: if no manager configured, refuse connection
			return fmt.Errorf("ssh host key verification failed: no HostKeyManager configured")
		},
		Timeout:         15 * time.Second,
	}

	// Ensure addr has a port; default to 22.
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "22")
	}

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh: dial %s: %w", addr, err)
	}

	return &Client{client: client}, nil
}

// Run executes a command on the remote host and returns stdout.
// Stderr is included in the error if the command fails.
func (c *Client) Run(cmd string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh: new session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(cmd); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("ssh: %s: %s", err, stderr.String())
		}
		return "", fmt.Errorf("ssh: %w", err)
	}

	return stdout.String(), nil
}

// Close terminates the SSH connection.
func (c *Client) Close() error {
	return c.client.Close()
}

// InstallPublicKey connects via password and adds the provided private key's corresponding public key to authorized_keys.
func InstallPublicKey(addr, user, password, privKeyPath string) error {
	client, err := Connect(addr, user, "password:"+password)
	if err != nil {
		return err
	}
	defer client.Close()

	key, err := os.ReadFile(privKeyPath)
	if err != nil {
		return fmt.Errorf("read priv key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return fmt.Errorf("parse priv key: %w", err)
	}

	pubKeyStr := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	pubKeyStr = strings.TrimSpace(pubKeyStr)

	// Add to remote authorized_keys
	cmd := fmt.Sprintf(`mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo "%s" >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`, pubKeyStr)
	_, err = client.Run(cmd)
	if err != nil {
		return fmt.Errorf("install pub key: %w", err)
	}

	return nil
}

// GenerateSSHKeypair generates a new ed25519 SSH keypair.
// Returns the PEM-encoded private key and the OpenSSH-formatted public key.
func GenerateSSHKeypair() (string, string, error) {
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", err
	}

	privBlock, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return "", "", err
	}
	privPEM := pem.EncodeToMemory(privBlock)

	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", "", err
	}
	pubBytes := ssh.MarshalAuthorizedKey(sshPubKey)

	return string(privPEM), string(pubBytes), nil
}
