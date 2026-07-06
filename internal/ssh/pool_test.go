package ssh

// pool_test.go — tests for the SSH connection pool (CTO-review §8 follow-up).

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// fakePooledClient is a fake ports.SSHClient that implements Pinger (so the
// pool's liveness probe runs). pingErr, when non-nil, makes Ping fail (simulates
// a dead/stale connection). closed is set when Close is called on the underlying
// client (the pool's wrapper Close returns-to-pool instead, so closed tracks the
// REAL close the pool does on eviction/shutdown).
type fakePooledClient struct {
	id      int
	pingErr error
	closed  atomic.Int32
}

func (c *fakePooledClient) Run(cmd string) (string, error) { return "", nil }
func (c *fakePooledClient) RunWithOutput(ctx context.Context, cmd string, timeout time.Duration) (string, string, int, error) {
	return "", "", 0, nil
}
func (c *fakePooledClient) UploadText(ctx context.Context, content, remotePath string, mode os.FileMode) error {
	return nil
}
func (c *fakePooledClient) Close() error { c.closed.Add(1); return nil }
func (c *fakePooledClient) Ping(ctx context.Context) error {
	if c.pingErr != nil {
		return c.pingErr
	}
	return nil
}

// countingConnector is a ports.SSHConnector that counts dials and hands out
// fakePooledClients (a fresh one per dial, so the test can assert how many
// real dials happened — pool reuse = fewer dials).
type countingConnector struct {
	mu     sync.Mutex
	dials  atomic.Int32
	nextID int
	clients []*fakePooledClient
	// pingErrForNew, when non-nil, sets the ping error on every newly-dialed
	// client (to simulate a connection that goes stale).
	pingErrForNew error
}

func (c *countingConnector) Connect(addr, user, keyPath string) (ports.SSHClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dials.Add(1)
	// Mimic the real ssh.Dial handshake: invoke the global HostKeyCallback
	// (which the pool wraps with fingerprintCapturingManager during a dial) so
	// the pool captures a fingerprint. Use a deterministic fake ed25519 key per
	// (addr) so the fingerprint is stable across dials to the same host.
	if cb := globalManager; cb != nil {
		// Generate the fake server key from hostOnly(addr) (strip port) so the
		// fingerprint is stable regardless of the port in addr — the pool's
		// borrow-time GetKnownHost(addr) and the dial-time capture must agree
		// on a fingerprint keyed by the host identity.
		pub := fakeServerPubKey(hostOnly(addr))
		_ = cb.CheckHostKey(hostOnly(addr), pub)
	}
	cl := &fakePooledClient{id: c.nextID, pingErr: c.pingErrForNew}
	c.nextID++
	c.clients = append(c.clients, cl)
	return cl, nil
}

// fakeServerPubKey derives a deterministic ssh.PublicKey from addr (so a host
// keeps the same fingerprint across dials until the test flips the stored fp).
// Uses sha256(addr) as the ed25519 seed (32 bytes, exactly what NewKeyFromSeed
// NOT deterministic across calls (it consumes entropy beyond the seed).
func fakeServerPubKey(addr string) ssh.PublicKey {
	h := sha256.Sum256([]byte(addr))
	edpub := ed25519.NewKeyFromSeed(h[:])
	pub, _ := ssh.NewPublicKey(edpub.Public())
	return pub
}

// poolFakeHostKeyMgr is a minimal HostKeyManager for the TOFU re-check test.
type poolFakeHostKeyMgr struct {
	mu       sync.Mutex
	store    map[string]string // addr → fingerprint
	checkErr error
}

func (m *poolFakeHostKeyMgr) CheckHostKey(addr string, remoteKey ssh.PublicKey) error {
	return m.checkErr
}
func (m *poolFakeHostKeyMgr) GetKnownHost(addr string) (*model.KnownHost, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fp, ok := m.store[addr]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &model.KnownHost{Addr: addr, Fingerprint: fp}, nil
}
func (m *poolFakeHostKeyMgr) setFingerprint(addr, fp string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		m.store = map[string]string{}
	}
	m.store[addr] = fp
}

// TestPool_ReusesConnection verifies a second Connect to the same (addr,user,
// key) reuses the pooled connection (1 dial, not 2).
func TestPool_ReusesConnection(t *testing.T) {
	conn := &countingConnector{}
	p := NewPool(conn, nil, nil)
	defer p.Close()

	c1, err := p.Connect("1.1.1.1:22", "root", "/k")
	if err != nil {
		t.Fatal(err)
	}
	if got := conn.dials.Load(); got != 1 {
		t.Fatalf("first connect: dials=%d, want 1", got)
	}
	if err := c1.Close(); err != nil {
		t.Fatal(err) // returns to pool
	}

	c2, err := p.Connect("1.1.1.1:22", "root", "/k")
	if err != nil {
		t.Fatal(err)
	}
	if got := conn.dials.Load(); got != 1 {
		t.Errorf("second connect (reuse): dials=%d, want 1 (pooled)", got)
	}
	_ = c2.Close()
}

// TestPool_DistinctKeysDialSeparately verifies different (addr/user/key) get
// separate connections (no false reuse).
func TestPool_DistinctKeysDialSeparately(t *testing.T) {
	conn := &countingConnector{}
	p := NewPool(conn, nil, nil)
	defer p.Close()

	c1, _ := p.Connect("1.1.1.1:22", "root", "/k1")
	c2, _ := p.Connect("2.2.2.2:22", "root", "/k2")
	_ = c1.Close()
	_ = c2.Close()
	if got := conn.dials.Load(); got != 2 {
		t.Errorf("distinct keys: dials=%d, want 2", got)
	}
}

// TestPool_EvictsOnPingFail verifies a cached connection whose Ping fails (dead
// peer) is evicted and a fresh dial happens on the next Connect.
func TestPool_EvictsOnPingFail(t *testing.T) {
	conn := &countingConnector{}
	p := NewPool(conn, nil, nil)
	defer p.Close()

	c1, _ := p.Connect("1.1.1.1:22", "root", "/k")
	_ = c1.Close()
	if dials := conn.dials.Load(); dials != 1 {
		t.Fatalf("setup: dials=%d, want 1", dials)
	}

	// Make the pooled client's Ping fail (simulates the peer dying).
	conn.mu.Lock()
	conn.clients[0].pingErr = fmt.Errorf("connection reset")
	conn.mu.Unlock()

	c2, _ := p.Connect("1.1.1.1:22", "root", "/k")
	if dials := conn.dials.Load(); dials != 2 {
		t.Errorf("ping-fail should evict + re-dial: dials=%d, want 2", dials)
	}
	// The stale client must have been really closed (evicted).
	conn.mu.Lock()
	staleClosed := conn.clients[0].closed.Load()
	conn.mu.Unlock()
	if staleClosed == 0 {
		t.Error("evicted (ping-fail) connection was not really closed")
	}
	_ = c2.Close()
}

// TestPool_EvictsOnHostKeyFingerprintChange verifies the TOFU stored-fingerprint
// re-check: if the stored known-host fingerprint changed (operator rotated the
// key), the pooled connection is evicted + re-dialed (catches the common
// rotation path without a fresh dial every borrow).
func TestPool_EvictsOnHostKeyFingerprintChange(t *testing.T) {
	conn := &countingConnector{}
	// Seed the stored fingerprint with the REAL fingerprint the fake connector
	// will produce for this addr (so the first borrow's re-check passes → reuse).
	// Then flip it to simulate the operator rotating the key.
	dialFp := ssh.FingerprintSHA256(fakeServerPubKey("1.1.1.1"))
	mgr := &poolFakeHostKeyMgr{store: map[string]string{"1.1.1.1:22": dialFp}}
	p := NewPool(conn, nil, mgr)
	defer p.Close()

	c1, _ := p.Connect("1.1.1.1:22", "root", "/k")
	_ = c1.Close()
	// First borrow: stored fp == dial-time fp → reuse (no re-dial).
	c2, _ := p.Connect("1.1.1.1:22", "root", "/k")
	if dials := conn.dials.Load(); dials != 1 {
		t.Errorf("first reuse (fingerprint unchanged): dials=%d, want 1", dials)
	}
	_ = c2.Close()

	// Operator rotates the key — stored fingerprint changes to something else.
	mgr.setFingerprint("1.1.1.1:22", "SHA256:rotated-by-operator")
	c3, _ := p.Connect("1.1.1.1:22", "root", "/k")
	if dials := conn.dials.Load(); dials != 2 {
		t.Errorf("fingerprint changed: should evict + re-dial, got dials=%d, want 2", dials)
	}
	_ = c3.Close()
}

// TestPool_CloseTearsDown verifies pool.Close() closes all live connections
// (graceful shutdown).
func TestPool_CloseTearsDown(t *testing.T) {
	conn := &countingConnector{}
	p := NewPool(conn, nil, nil)

	c1, _ := p.Connect("1.1.1.1:22", "root", "/k")
	_ = c1.Close() // returned to pool (idle, NOT closed)
	conn.mu.Lock()
	client := conn.clients[0]
	conn.mu.Unlock()
	if client.closed.Load() != 0 {
		t.Fatal("idle pooled connection should not be closed before pool.Close")
	}

	p.Close()
	if client.closed.Load() != 1 {
		t.Error("pool.Close should close the idle connection")
	}
}

// TestPool_IdleEviction verifies an entry idle longer than idleTTL is closed by
// the sweeper. We use a tiny idleTTL to keep the test fast.
func TestPool_IdleEviction(t *testing.T) {
	conn := &countingConnector{}
	p := NewPool(conn, nil, nil)
	p.idleTTL = 100 * time.Millisecond
	defer p.Close()

	c1, _ := p.Connect("1.1.1.1:22", "root", "/k")
	_ = c1.Close() // idle
	conn.mu.Lock()
	client := conn.clients[0]
	conn.mu.Unlock()

	// Wait > idleTTL + a sweeper tick. The sweeper runs every defaultSweepEvery
	// (60s) — too slow for a test. Trigger a manual sweep instead.
	time.Sleep(150 * time.Millisecond)
	p.sweepOnce()
	if client.closed.Load() != 1 {
		t.Error("idle entry older than idleTTL should be closed by sweep")
	}
	// Next connect must re-dial.
	c2, _ := p.Connect("1.1.1.1:22", "root", "/k")
	if dials := conn.dials.Load(); dials != 2 {
		t.Errorf("after idle eviction: dials=%d, want 2 (re-dial)", dials)
	}
	_ = c2.Close()
}