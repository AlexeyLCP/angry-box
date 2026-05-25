package ssh

import (
	"bytes"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client wraps an SSH connection for on-demand operations.
// Connections are NEVER persistent. Connect -> execute -> Close.
type Client struct {
	conn *ssh.Client
	host string
}

// ConnectParams holds the parameters for establishing an SSH connection.
type ConnectParams struct {
	Host       string
	Port       int
	Username   string
	AuthMethod string // "password" or "key"
	Credential string // password string or private key PEM
	Timeout    time.Duration
}

// Connect establishes a new SSH connection and returns a Client.
// Caller MUST call Close() after use.
func Connect(params ConnectParams) (*Client, error) {
	var authMethods []ssh.AuthMethod
	if params.AuthMethod == "key" {
		signer, err := ssh.ParsePrivateKey([]byte(params.Credential))
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	} else {
		authMethods = append(authMethods, ssh.Password(params.Credential))
	}

	config := &ssh.ClientConfig{
		User:            params.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         params.Timeout,
	}

	addr := net.JoinHostPort(params.Host, fmt.Sprintf("%d", params.Port))
	conn, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return &Client{conn: conn, host: params.Host}, nil
}

// Exec runs a command on the remote host and returns stdout as a string.
// On error, stdout is returned along with the error for diagnostics.
func (c *Client) Exec(cmd string) (string, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Run(cmd); err != nil {
		return stdout.String() + stderr.String(), fmt.Errorf("run %q: %w", cmd, err)
	}
	return stdout.String() + stderr.String(), nil
}

// ReadFile reads a remote file and returns its content as a string.
func (c *Client) ReadFile(path string) (string, error) {
	return c.Exec("cat " + path)
}

// WriteFile writes content to a remote file using a heredoc.
func (c *Client) WriteFile(path, content string) error {
	cmd := fmt.Sprintf("tee %s > /dev/null << 'LUCX_EOF'\n%s\nLUCX_EOF", path, content)
	_, err := c.Exec(cmd)
	return err
}

// Host returns the hostname this client is connected to.
func (c *Client) Host() string { return c.host }

// Close terminates the SSH connection.
func (c *Client) Close() error { return c.conn.Close() }
