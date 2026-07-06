package ports

import (
	"context"
	"os"
	"time"
)

// SSHClient is the subset of the SSH client surface that callers in chain,
// backend and takeover actually use. Declaring it here (instead of passing the
// concrete *ssh.Client) lets tests inject a fake that records and replays
// remote commands without opening a real network connection — the foundation
// of the C3 test-infrastructure effort.
type SSHClient interface {
	// Run executes cmd and returns stdout. On failure stdout is empty and the
	// error carries the (non-discarded) stderr where available.
	Run(cmd string) (string, error)

	// RunWithOutput runs cmd and returns stdout, stderr, the exit code and error
	// separately. stderr is never discarded. exitCode is -1 when the command
	// could not be started or its exit status could not be determined.
	RunWithOutput(ctx context.Context, cmd string, timeout time.Duration) (stdout, stderr string, exitCode int, err error)

	// UploadText writes content to remotePath over stdin (no heredoc, no shell
	// interpolation of the payload) and chmods it to mode.
	UploadText(ctx context.Context, content, remotePath string, mode os.FileMode) error

	// Close terminates the underlying SSH connection.
	Close() error
}

// SSHConnector opens an SSH connection to a host. It mirrors the package-level
// ssh.Connect(addr, user, keyPath) shape but returns the abstract SSHClient so
// callers depend on the port, not the concrete adapter.
type SSHConnector interface {
	Connect(addr, user, keyPath string) (SSHClient, error)
}

// Pinger is an OPTIONAL capability an SSHClient may implement: a cheap liveness
// probe used by the SSH connection pool on borrow (keepalive@openssh.com global
// request). The pool type-asserts the cached client to Pinger; if the client
// does not implement it, the pool skips the probe and reuses the connection
// unconditionally (relying on the first Run to surface a dead connection).
type Pinger interface {
	Ping(ctx context.Context) error
}