package ssh

// pool.go — an SSH connection POOL that reuses connections across deploys
// (cross-deploy reuse). The v0.3.0 "intra-deploy collapse" already made one
// merged deploy open 1 connection instead of 3 (via the ClientBackend
// plumbing); this pool adds the second-order win — a node re-deployed by
// autoapply every ~5 min reuses the already-open connection instead of
// re-dialing (CTO-review §8 follow-up).
//
// Design (see docs/PATCHES-and-pool reasoning + AGENTS.md):
//
//   - poolConnector wraps an inner ports.SSHConnector and implements
//     ports.SSHConnector. It is the seam: callers (the composition root in
//     serveCmd) inject it where they used to pass the raw DefaultConnector.
//   - Key: (addr, user, keyPath) — the connection identity. keyPath may be a
//     key ID resolved by KeyResolver; on borrow the pool re-resolves it and
//     evicts the entry if the PEM changed (key rotation).
//   - Borrow: look up an idle entry; if present, Ping it (keepalive@openssh.com
//     via the optional ports.Pinger capability — a dead peer / restarted sshd /
//     NAT-timed-out connection fails here) and re-check the stored known-host
//     fingerprint (TOFU staleness — an operator-initiated key rotation is
//     caught cheaply without a fresh dial). On any check failure, evict + dial.
//   - pooledClient is a thin wrapper: Run/RunWithOutput/UploadText delegate
//     straight to the underlying client; Close() returns the client to the pool
//     (does NOT close it). This is what makes cross-deploy reuse work — the
//     caller's `defer client.Close()` returns the connection instead of
//     tearing it down.
//   - Idle eviction: a background sweeper (60s ticker) closes entries idle
//     longer than idleTTL (5 min). pool.Close() stops the sweeper and closes
//     every live entry (graceful shutdown).
//
// TOFU note: a pooled connection does NOT re-run the full HostKeyCallback
// (that only fires at dial time; x/crypto/ssh does not expose the negotiated
// host key post-dial). The pool accepts this staleness (an internal
// orchestrator trusts TOFU was correct at first dial) and mitigates it with
// the cheap stored-fingerprint re-check on borrow — which catches an operator
// editing the known-host entry, the common rotation path. A live MITM key
// change between pool-misses is the residual risk (bounded by the autoapply
// re-deploy interval); the v0.3.0 collapse (1 dial/deploy) already limits how
// long a stale connection lives. A future re-dial-on-Nth-borrow would bound it
// further.

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// poolDefaults — tuned for an orchestrator on a modest VPS.
const (
	defaultIdleTTL      = 5 * time.Minute
	defaultSweepEvery   = 60 * time.Second
	defaultPingTimeout  = 3 * time.Second
)

// poolEntry is one cached connection.
type poolEntry struct {
	client      ports.SSHClient
	lastUsed    time.Time
	// knownFingerprint is the host-key fingerprint observed at dial time (captured
	// via the HostKeyCallback closure). On borrow, the pool re-checks the stored
	// known-host fingerprint; if it changed, the entry is evicted (the operator
	// rotated the key). Empty when no HostKeyManager is wired (no re-check).
	knownFingerprint string
	// resolvedKey is the PEM bytes (or "password:..." literal) the connection
	// was dialed with. On borrow the pool re-resolves keyPath and evicts if the
	// PEM changed (key rotation under the same ID).
	resolvedKey string
	// inUse marks the entry as borrowed (Close returns it to idle). The sweeper
	// only evicts idle entries.
	inUse bool
}

// Pool is a cross-deploy SSH connection pool. Construct with NewPool and inject
// it as the ports.SSHConnector where the composition root used the raw
// DefaultConnector. Close it on graceful shutdown.
type Pool struct {
	inner     ports.SSHConnector
	keyRes    KeyResolver
	hostKey   HostKeyManager
	idleTTL   time.Duration
	pingTO    time.Duration

	mu      sync.Mutex
	entries map[string]*poolEntry // keyed by (addr|user|keyPath)

	stop chan struct{} // closed to stop the sweeper
	wg   sync.WaitGroup
}

// NewPool builds a pool wrapping inner. The keyResolver + hostKeyManager are
// the same globals wired in main.go (sshclient.SetKeyResolver / SetHostKeyManager);
// pass them so the pool can re-verify on borrow without touching package globals
// (testable). nil is fine — the pool then skips the corresponding re-check.
func NewPool(inner ports.SSHConnector, keyRes KeyResolver, hostKey HostKeyManager) *Pool {
	p := &Pool{
		inner:    inner,
		keyRes:   keyRes,
		hostKey:  hostKey,
		idleTTL:  defaultIdleTTL,
		pingTO:   defaultPingTimeout,
		entries:  make(map[string]*poolEntry),
		stop:     make(chan struct{}),
	}
	p.wg.Add(1)
	go p.sweeper()
	return p
}

// poolKey is the connection-identity key.
func poolKey(addr, user, keyPath string) string {
	return addr + "|" + user + "|" + keyPath
}

// Connect returns a pooled SSH client for (addr, user, keyPath). On a cache hit
// the connection is verified (Ping + stored-fingerprint re-check + key-resolution
// re-check) and reused; on a miss or a failed check it dials fresh. The returned
// client's Close() returns the connection to the pool (it does not close it).
func (p *Pool) Connect(addr, user, keyPath string) (ports.SSHClient, error) {
	if p == nil || p.inner == nil {
		// Defensive: a nil pool is a passthrough to the inner (or no connector).
		return nil, fmt.Errorf("ssh pool: not initialized")
	}
	k := poolKey(addr, user, keyPath)

	// Re-resolve the key now (for both the dial-time record and the borrow-time
	// re-check). For password: keyPath the literal IS the identity.
	resolved := keyPath
	if p.keyRes != nil && !isPasswordKey(keyPath) {
		if data, ok := p.keyRes.ResolveKey(keyPath); ok {
			resolved = data
		}
	}

	p.mu.Lock()
	e, ok := p.entries[k]
	p.mu.Unlock()

	if ok {
		// Cache hit: verify before reuse.
		if p.entryUsable(e, addr, resolved) {
			p.mu.Lock()
			e.inUse = true
			e.lastUsed = time.Now()
			p.mu.Unlock()
			return &pooledClient{pool: p, key: k, inner: e.client}, nil
		}
		// Check failed — evict and dial fresh.
		p.evict(k, e)
	}

	// Dial fresh. Capture the observed fingerprint via a HostKeyCallback wrapper
	// so the pool can re-check the stored known-host fingerprint on borrow.
	var observedFp string
	client, err := p.dialWithFingerprintCapture(addr, user, keyPath, &observedFp)
	if err != nil {
		return nil, err
	}
	entry := &poolEntry{
		client:           client,
		lastUsed:         time.Now(),
		knownFingerprint: observedFp,
		resolvedKey:      resolved,
		inUse:            true,
	}
	p.mu.Lock()
	p.entries[k] = entry
	p.mu.Unlock()
	return &pooledClient{pool: p, key: k, inner: client}, nil
}

// entryUsable runs the borrow-time checks: Ping liveness + stored-fingerprint
// re-check + key-resolution re-check. Returns true if the entry can be reused.
func (p *Pool) entryUsable(e *poolEntry, addr, resolvedKey string) bool {
	if e == nil || e.client == nil {
		return false
	}
	// 1. Liveness via the optional Pinger (keepalive). If the client doesn't
	//    implement Pinger, skip (rely on the first Run to surface a dead conn).
	if pinger, ok := e.client.(ports.Pinger); ok {
		ctx, cancel := context.WithTimeout(context.Background(), p.pingTO)
		err := pinger.Ping(ctx)
		cancel()
		if err != nil {
			return false
		}
	}
	// 2. Key rotation: the resolved PEM changed under the same keyPath → evict.
	if resolvedKey != "" && e.resolvedKey != "" && resolvedKey != e.resolvedKey {
		return false
	}
	// 3. TOFU stored-fingerprint re-check: if a HostKeyManager is wired and we
	//    captured a fingerprint at dial time, re-check the stored known-host.
	//    An operator editing the known-host entry (rotation) evicts the stale
	//    connection. (Does NOT catch a live MITM key change — that needs a fresh
	//    dial; the residual risk is bounded by the autoapply re-deploy interval.)
	if p.hostKey != nil && e.knownFingerprint != "" {
		if kh, err := p.hostKey.GetKnownHost(addr); err == nil && kh != nil && kh.Fingerprint != "" {
			if kh.Fingerprint != e.knownFingerprint {
				return false
			}
		}
	}
	return true
}

// dialWithFingerprintCapture dials via the inner connector AND records the
// observed host-key fingerprint by temporarily installing a HostKeyCallback
// wrapper. Because the inner connector (realConnector) reads the package-global
// manager, we wrap at the manager level for the duration of this dial: install
// a forwarding manager that records the fingerprint then delegates to the real
// one. This avoids changing the inner connector's signature.
// ponytail: one capture-dial at a time. The global HostKeyManager wrap is not
// safe to overlap; serialize if first-dials ever contend.
var dialCaptureMu sync.Mutex

func (p *Pool) dialWithFingerprintCapture(addr, user, keyPath string, fpOut *string) (ports.SSHClient, error) {
	if p.hostKey == nil {
		return p.inner.Connect(addr, user, keyPath)
	}
	dialCaptureMu.Lock()
	defer dialCaptureMu.Unlock()
	prev := currentHostKeyManager()
	SetHostKeyManager(&fingerprintCapturingManager{delegate: prev, addr: hostOnly(addr), fpOut: fpOut})
	defer SetHostKeyManager(prev)
	return p.inner.Connect(addr, user, keyPath)
}

// evict closes the entry and removes it from the map. Caller must NOT hold p.mu.
func (p *Pool) evict(k string, e *poolEntry) {
	if e == nil {
		return
	}
	p.mu.Lock()
	if cur, ok := p.entries[k]; ok && cur == e {
		delete(p.entries, k)
	}
	p.mu.Unlock()
	_ = e.client.Close()
}

// release returns a borrowed connection to the idle pool (called by
// pooledClient.Close). If the entry was evicted concurrently, close the client.
func (p *Pool) release(k string, client ports.SSHClient) {
	p.mu.Lock()
	e, ok := p.entries[k]
	if ok && e.client == client {
		e.inUse = false
		e.lastUsed = time.Now()
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	// Entry was evicted (sweeper or a failed re-check) — close the orphan.
	_ = client.Close()
}

// sweeper periodically closes idle entries older than idleTTL.
func (p *Pool) sweeper() {
	defer p.wg.Done()
	t := time.NewTicker(defaultSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.sweepOnce()
		}
	}
}

func (p *Pool) sweepOnce() {
	now := time.Now()
	var toClose []ports.SSHClient
	p.mu.Lock()
	for k, e := range p.entries {
		if e.inUse {
			continue
		}
		if now.Sub(e.lastUsed) > p.idleTTL {
			toClose = append(toClose, e.client)
			delete(p.entries, k)
		}
	}
	p.mu.Unlock()
	for _, c := range toClose {
		_ = c.Close()
	}
}

// Close stops the sweeper and closes every live entry. Call on graceful
// shutdown (after in-flight deploys finish — see main.go gracefulShutdown).
func (p *Pool) Close() {
	close(p.stop)
	p.wg.Wait()
	p.mu.Lock()
	entries := p.entries
	p.entries = make(map[string]*poolEntry)
	p.mu.Unlock()
	for _, e := range entries {
		_ = e.client.Close()
	}
}

// pooledClient is a thin wrapper: delegates to the underlying client, but
// Close() returns the connection to the pool instead of closing it.
type pooledClient struct {
	pool *Pool
	key  string
	inner ports.SSHClient
}

func (c *pooledClient) Run(cmd string) (string, error) { return c.inner.Run(cmd) }

func (c *pooledClient) RunWithOutput(ctx context.Context, cmd string, timeout time.Duration) (string, string, int, error) {
	return c.inner.RunWithOutput(ctx, cmd, timeout)
}

func (c *pooledClient) UploadText(ctx context.Context, content, remotePath string, mode os.FileMode) error {
	return c.inner.UploadText(ctx, content, remotePath, mode)
}

func (c *pooledClient) Close() error {
	c.pool.release(c.key, c.inner)
	return nil
}

// fingerprintCapturingManager wraps a HostKeyManager to record the observed
// fingerprint during a single dial (used by dialWithFingerprintCapture).
type fingerprintCapturingManager struct {
	delegate HostKeyManager
	addr     string
	fpOut    *string
}

func (m *fingerprintCapturingManager) CheckHostKey(addr string, remoteKey ssh.PublicKey) error {
	if m.fpOut != nil {
		*m.fpOut = ssh.FingerprintSHA256(remoteKey)
	}
	if m.delegate != nil {
		return m.delegate.CheckHostKey(addr, remoteKey)
	}
	return fmt.Errorf("ssh: no host-key manager")
}

func (m *fingerprintCapturingManager) GetKnownHost(addr string) (*model.KnownHost, error) {
	if m.delegate != nil {
		return m.delegate.GetKnownHost(addr)
	}
	return nil, fmt.Errorf("no host-key manager")
}

// isPasswordKey reports whether keyPath is a password literal ("password:...").
func isPasswordKey(keyPath string) bool {
	return len(keyPath) > len("password:") && keyPath[:len("password:")] == "password:"
}

// hostOnly strips the port from an addr for known-host matching.
func hostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}