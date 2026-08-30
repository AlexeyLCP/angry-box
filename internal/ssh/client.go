package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// defaultRunTimeout is the per-command deadline for Run when no context is
// supplied. Long operations (apt-get install, make, curl of a large tarball)
// should pass their own context with a larger timeout via RunContext.
const defaultRunTimeout = 5 * time.Minute

// HostKeyManager is used to verify remote host keys. GetKnownHost lets the
// SSH connection pool re-check the stored fingerprint on borrow (TOFU
// staleness mitigation — an operator editing the known-host entry evicts the
// stale pooled connection; CTO-review §8 pool follow-up). Implementations
// (the chain Store) already provide both methods.
type HostKeyManager interface {
	CheckHostKey(addr string, remoteKey ssh.PublicKey) error
	GetKnownHost(addr string) (*model.KnownHost, error)
}

var (
	globalManager HostKeyManager
	managerMu     sync.Mutex
)

// SetHostKeyManager sets the global host key manager.
func SetHostKeyManager(m HostKeyManager) {
	managerMu.Lock()
	defer managerMu.Unlock()
	globalManager = m
}

func currentHostKeyManager() HostKeyManager {
	managerMu.Lock()
	defer managerMu.Unlock()
	return globalManager
}

// KeyResolver is used to retrieve SSH private keys from the database by ID.
type KeyResolver interface {
	ResolveKey(keyID string) (string, bool)
}

var globalKeyResolver KeyResolver

// SetKeyResolver sets the global SSH key resolver.
func SetKeyResolver(r KeyResolver) {
	globalKeyResolver = r
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
		var key []byte
		var err error

		if globalKeyResolver != nil {
			if data, ok := globalKeyResolver.ResolveKey(keyPath); ok {
				key = []byte(data)
			}
		}

		if len(key) == 0 {
			key, err = os.ReadFile(keyPath)
			if err != nil {
				return nil, fmt.Errorf("ssh: read key %q: %w", keyPath, err)
			}
		}

		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("ssh: parse key: %w", err)
		}
		authMethod = ssh.PublicKeys(signer)
	}

	mgr := currentHostKeyManager()
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			authMethod,
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			if mgr != nil {
				return mgr.CheckHostKey(hostname, key)
			}
			return fmt.Errorf("ssh host key verification failed: no HostKeyManager configured")
		},
		Timeout: 15 * time.Second,
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
// Stderr is included in the error message if the command fails.
//
// Note: on failure Run returns an empty string and discards stdout. Use
// RunWithOutput when you need both streams (e.g. to surface `sing-box check`
// diagnostics) — Run is kept for backwards compatibility with callers that
// only care about success/failure.
func (c *Client) Run(cmd string) (string, error) {
	stdout, _, _, err := c.runWithDeadline(context.Background(), cmd, defaultRunTimeout)
	return stdout, err
}

// RunWithOutput runs cmd and returns stdout, stderr, the exit code and error
// separately. stderr is NEVER discarded, even on success — this is what the
// deploy/install path needs to surface real diagnostics (sing-box check
// errors, systemctl/journalctl output) instead of opaque "exit status 1".
// exitCode is -1 if the command could not be started or the exit status could
// not be determined.
func (c *Client) RunWithOutput(ctx context.Context, cmd string, timeout time.Duration) (stdout, stderr string, exitCode int, err error) {
	return c.runWithDeadline(ctx, cmd, timeout)
}

// runWithDeadline is the single implementation behind Run/RunContext/RunWithOutput.
func (c *Client) runWithDeadline(ctx context.Context, cmd string, timeout time.Duration) (stdout, stderr string, exitCode int, err error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("ssh: new session: %w", err)
	}
	defer session.Close()

	var outBuf, errBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	// Compose the effective context: caller's ctx, plus an internal timeout
	// if one was given. The internal timer ensures long apt/make commands do
	// not hang forever even when the caller's ctx has no deadline.
	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = session.Run(cmd)
	}()
	select {
	case <-done:
	case <-runCtx.Done():
		// Forcefully terminate the remote command if the deadline fired.
		_ = session.Signal(ssh.SIGKILL)
		<-done
		runErr = fmt.Errorf("ssh: command timed out: %w", runCtx.Err())
	}

	exitCode = 0 // success unless proven otherwise
	if runErr != nil {
		if ee, ok := runErr.(*ssh.ExitError); ok {
			exitCode = ee.ExitStatus()
		} else {
			exitCode = -1 // non-exit error (timeout, transport) — unknown status
		}
	}

	if runErr != nil {
		if errBuf.Len() > 0 {
			return outBuf.String(), errBuf.String(), exitCode, fmt.Errorf("ssh: %s: %s", runErr, errBuf.String())
		}
		return outBuf.String(), "", exitCode, fmt.Errorf("ssh: %w", runErr)
	}
	return outBuf.String(), errBuf.String(), exitCode, nil
}

// UploadText writes content to remotePath over stdin via `cat > path && chmod`.
// This avoids heredocs (which can truncate on delimiter collision and break on
// binary/cert content) and avoids interpolating file content into a shell
// command string (no injection / quoting issues). mode is the file mode bits
// to chmod on the remote file (e.g. 0o644).
func (c *Client) UploadText(ctx context.Context, content, remotePath string, mode os.FileMode) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: new session: %w", err)
	}
	defer session.Close()

	q, err := QuotePOSIX(remotePath)
	if err != nil {
		return fmt.Errorf("ssh: upload path: %w", err)
	}
	cmd := fmt.Sprintf("cat > %s && chmod %o %s", q, mode, q)
	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("ssh: stdin pipe: %w", err)
	}

	// IMPORTANT: start the remote command (cat) BEFORE writing to stdin. The
	// previous order (Write → Close → Run) deadlocked for content larger than
	// the pipe buffer, and produced empty files for small content (cat read an
	// already-closed stdin). Start drains stdin into the file as we write.
	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("ssh: start: %w", err)
	}

	runCtx := ctx
	if _, ok := runCtx.Deadline(); !ok {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, defaultRunTimeout)
		defer cancel()
	}

	done := make(chan error, 1)
	go func() {
		_, werr := io.WriteString(stdin, content)
		_ = stdin.Close()
		done <- session.Wait()
		_ = werr // best-effort; a write failure surfaces as Wait failure
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("ssh: upload %s: %w", remotePath, err)
		}
		return nil
	case <-runCtx.Done():
		_ = session.Signal(ssh.SIGKILL)
		<-done
		return fmt.Errorf("ssh: upload %s timed out: %w", remotePath, runCtx.Err())
	}
}

// RemoteForward opens a remote port-forward (the ssh -R equivalent): the SSH
// server listens on remoteAddr (e.g. "127.0.0.1:8900") and every accepted
// connection is bridged to localAddr (e.g. "127.0.0.1:9080") on THIS machine.
// It blocks until ctx is cancelled or the listener dies; the caller runs it in
// a goroutine with a reconnect loop. This is how the panel relay exposes the
// orchestrator UI through a public node when the orchestrator sits behind NAT.
func (c *Client) RemoteForward(ctx context.Context, remoteAddr, localAddr string) error {
	listener, err := c.client.Listen("tcp", remoteAddr)
	if err != nil {
		return fmt.Errorf("ssh: remote forward %s: %w", remoteAddr, err)
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		remote, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // normal shutdown
			}
			return fmt.Errorf("ssh: remote forward accept: %w", err)
		}
		go bridgeConn(ctx, remote, localAddr)
	}
}

// bridgeConn copies data between the remote-forwarded connection and a fresh
// dial to localAddr. Both directions run concurrently; the pair closes when
// either side ends or ctx is cancelled.
func bridgeConn(ctx context.Context, remote net.Conn, localAddr string) {
	defer remote.Close()
	d := net.Dialer{}
	local, err := d.DialContext(ctx, "tcp", localAddr)
	if err != nil {
		return
	}
	defer local.Close()
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(local, remote)
	go cp(remote, local)
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// DownloadFile fetches a remote file's bytes (base64-piped through an SSH
// session — binary-safe, no SFTP subsystem needed). Used to pull small files
// like the 3x-ui/lucx-ui panel SQLite DB into the orchestrator for parsing.
func (c *Client) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh: session: %w", err)
	}
	defer session.Close()
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	var out bytes.Buffer
	session.Stdout = &out
	go func() {
		q, qerr := QuotePOSIX(remotePath)
		if qerr != nil {
			done <- qerr
			return
		}
		done <- session.Run(fmt.Sprintf("base64 -w0 %s 2>/dev/null || base64 %s", q, q))
	}()
	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("ssh: download %s: %w", remotePath, err)
		}
	case <-runCtx.Done():
		_ = session.Signal(ssh.SIGKILL)
		<-done
		return nil, fmt.Errorf("ssh: download %s timed out: %w", remotePath, runCtx.Err())
	}
	raw, err := base64Decode(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("ssh: download %s: decode: %w", remotePath, err)
	}
	return raw, nil
}

// base64Decode tolerates the newline-wrapped output of busybox/base64 variants.
func base64Decode(b []byte) ([]byte, error) {
	clean := bytes.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' {
			return -1
		}
		return r
	}, b)
	return base64.StdEncoding.DecodeString(string(clean))
}

// Close terminates the SSH connection.
func (c *Client) Close() error {
	return c.client.Close()
}

// Ping verifies the underlying SSH connection is still alive by sending an
// OpenSSH keepalive global request and waiting for a reply. It is the cheap,
// session-free liveness probe the SSH connection pool uses on borrow (a dead
// peer / restarted sshd / NAT-timed-out connection fails here, triggering an
// eviction + fresh dial which then re-runs the full TOFU host-key check). A
// short ctx deadline bounds the probe cost.
func (c *Client) Ping(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		// SendGlobalRequest with wantReply=true blocks until the server replies
		// (or the connection drops). openssh-3.x+ replies to keepalive@openssh.com.
		_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// InstallPublicKey connects via password and adds the provided private key's corresponding public key to authorized_keys.
func InstallPublicKey(addr, user, password, privKeyPath string) error {
	client, err := Connect(addr, user, "password:"+password)
	if err != nil {
		return err
	}
	defer client.Close()

	var key []byte
	var readErr error

	if globalKeyResolver != nil {
		if data, ok := globalKeyResolver.ResolveKey(privKeyPath); ok {
			key = []byte(data)
		}
	}

	if len(key) == 0 {
		key, readErr = os.ReadFile(privKeyPath)
		if readErr != nil {
			return fmt.Errorf("read priv key: %w", readErr)
		}
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return fmt.Errorf("parse priv key: %w", err)
	}

	pubKeyStr := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	pubKeyStr = strings.TrimSpace(pubKeyStr)

	qpub, err := QuotePOSIX(pubKeyStr)
	if err != nil {
		return fmt.Errorf("install pub key: %w", err)
	}
	cmd := fmt.Sprintf(`mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo %s >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`, qpub)
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

// Compile-time check: the concrete Client satisfies the port so the adapter
// needs no wrapper methods — it is a zero-cost implementation of ports.SSHClient.
var _ ports.SSHClient = (*Client)(nil)

// realConnector is the production SSHConnector backed by Connect above.
// It is the default used by the composition root; tests inject a fake.
type realConnector struct{}

// Connect returns a real SSH connection as a ports.SSHClient.
func (realConnector) Connect(addr, user, keyPath string) (ports.SSHClient, error) {
	c, err := Connect(addr, user, keyPath)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// DefaultConnector is the production SSHConnector. Callers that want the real
// network behaviour pass this (or NewApplier/factory default to it); tests pass
// a fake instead.
var DefaultConnector ports.SSHConnector = realConnector{}
