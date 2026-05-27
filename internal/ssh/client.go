package ssh

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client wraps an SSH connection and provides convenience methods.
type Client struct {
	client *ssh.Client
}

// Connect establishes an SSH connection to host using key-based authentication.
func Connect(addr, user, keyPath string) (*Client, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("ssh: read key %q: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("ssh: parse key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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
