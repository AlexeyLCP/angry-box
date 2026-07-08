package web

// handlers_health_test.go — covers the metrics-loop state machine integration
// (collectAllMetrics → chain.NextState + audit-on-transition) and the operator
// mark/clear-blocked handlers. Uses the real ServeMux via newTestServer with a
// custom factory whose GetStatus is controllable, so no real SSH dial happens.

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// statusBackend is a ports.Backend whose GetStatus is controllable per test.
// All other methods delegate to noopBackend (success no-ops). Used to simulate
// healthy / down / unreachable nodes in collectAllMetrics tests.
type statusBackend struct {
	noopBackend
	status *model.Status
	err    error
}

func (b statusBackend) GetStatus(_ context.Context, _ model.Host) (*model.Status, error) {
	return b.status, b.err
}

// statusFactory returns a fixed statusBackend.
type statusFactory struct{ b statusBackend }

func (f statusFactory) Create() ports.Backend { return f.b }

// seedHost saves a host so collectAllMetrics iterates over it.
func seedHost(t *testing.T, ts *testServer, id string) {
	t.Helper()
	if err := ts.srv.store().SaveHost(&model.Host{ID: id, Addr: "10.0.0.1:22", User: "root", KeyPath: "/k"}); err != nil {
		t.Fatalf("SaveHost %s: %v", id, err)
	}
}

// metricsState reads the persisted NodeMetrics.State for a host, or "" if none.
func metricsState(t *testing.T, ts *testServer, id string) string {
	t.Helper()
	m, err := ts.srv.store().GetMetrics(id)
	if err != nil {
		return ""
	}
	return m.State
}

// auditHealthCount counts "health" audit entries (action == "health").
func auditHealthCount(t *testing.T, ts *testServer) int {
	t.Helper()
	logs, err := ts.srv.store().ListAuditLogs(0)
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	n := 0
	for _, l := range logs {
		if l.Action == "health" || l.Action == "blocked" || l.Action == "unblocked" {
			n++
		}
	}
	return n
}

// TestCollectAllMetrics_HealthyProbe — fresh host + healthy GetStatus →
// State=healthy (first classification from unknown is NOT audited, oldState=="").
func TestCollectAllMetrics_HealthyProbe(t *testing.T) {
	ts := newTestServerWithFactory(t, statusFactory{b: statusBackend{status: &model.Status{Running: true}}})
	seedHost(t, ts, "n1")

	ts.srv.collectAllMetrics()

	if s := metricsState(t, ts, "n1"); s != model.NodeStateHealthy {
		t.Fatalf("state = %q, want healthy", s)
	}
	if n := auditHealthCount(t, ts); n != 0 {
		t.Fatalf("audit health entries = %d, want 0 (first classification not audited)", n)
	}
}

// TestCollectAllMetrics_DownTransitionAudited — healthy node → 3 systemd-inactive
// probes → State=down + an audit "health" entry with from/to. Confirms the loop
// classifies (err==nil, Running=false) as down and audits the transition.
func TestCollectAllMetrics_DownTransitionAudited(t *testing.T) {
	ts := newTestServerWithFactory(t, statusFactory{b: statusBackend{status: &model.Status{Running: false}}})
	seedHost(t, ts, "n1")

	// Establish healthy first so the down transition is from a real state (audited).
	ts.srv.factory = statusFactory{b: statusBackend{status: &model.Status{Running: true}}}
	ts.srv.collectAllMetrics()
	if s := metricsState(t, ts, "n1"); s != model.NodeStateHealthy {
		t.Fatalf("setup: state = %q, want healthy", s)
	}

	// Now fail the service 3 times → down. Each tick classifies systemd-inactive.
	ts.srv.factory = statusFactory{b: statusBackend{status: &model.Status{Running: false}}}
	for i := 0; i < 3; i++ {
		ts.srv.collectAllMetrics()
	}

	if s := metricsState(t, ts, "n1"); s != model.NodeStateDown {
		t.Fatalf("state = %q, want down after 3 fails", s)
	}
	// Two transitions: healthy→suspect (1st fail) + suspect→down (3rd fail).
	if n := auditHealthCount(t, ts); n != 2 {
		t.Fatalf("audit health entries = %d, want 2 (healthy→suspect + suspect→down)", n)
	}
}

// TestCollectAllMetrics_UnreachableSSHFail — SSH dial error → unreachable after
// DownThreshold fails. Confirms err != nil is classified as unreachable (not down).
func TestCollectAllMetrics_UnreachableSSHFail(t *testing.T) {
	ts := newTestServerWithFactory(t, statusFactory{b: statusBackend{status: &model.Status{Running: true}}})
	seedHost(t, ts, "n1")
	ts.srv.collectAllMetrics() // → healthy

	// SSH dial fails 3 times → unreachable.
	ts.srv.factory = statusFactory{b: statusBackend{err: errStr("ssh dial: i/o timeout")}}
	for i := 0; i < 3; i++ {
		ts.srv.collectAllMetrics()
	}
	if s := metricsState(t, ts, "n1"); s != model.NodeStateUnreachable {
		t.Fatalf("state = %q, want unreachable after 3 SSH fails", s)
	}
}

// TestCollectAllMetrics_HysteresisNoPrematureDown — 2 fails (below
// DownThreshold=3) from healthy → suspect, NOT down. Confirms a transient blip
// does not flap the node or write a down audit entry.
func TestCollectAllMetrics_HysteresisNoPrematureDown(t *testing.T) {
	ts := newTestServerWithFactory(t, statusFactory{b: statusBackend{status: &model.Status{Running: true}}})
	seedHost(t, ts, "n1")
	ts.srv.collectAllMetrics() // → healthy

	ts.srv.factory = statusFactory{b: statusBackend{status: &model.Status{Running: false}}}
	ts.srv.collectAllMetrics()
	ts.srv.collectAllMetrics() // 2 fails total

	if s := metricsState(t, ts, "n1"); s != model.NodeStateSuspect {
		t.Fatalf("state = %q, want suspect after 2 fails (hysteresis)", s)
	}
	if n := auditHealthCount(t, ts); n != 1 {
		t.Fatalf("audit = %d, want 1 (the healthy→suspect transition only, no down)", n)
	}
}

// TestCollectAllMetrics_Recovery — down node + 2 healthy probes → healthy,
// audited once (down→healthy). Confirms RecoverThreshold works in the loop.
func TestCollectAllMetrics_Recovery(t *testing.T) {
	ts := newTestServerWithFactory(t, statusFactory{b: statusBackend{status: &model.Status{Running: true}}})
	seedHost(t, ts, "n1")
	ts.srv.collectAllMetrics() // healthy

	// Drive to down.
	ts.srv.factory = statusFactory{b: statusBackend{status: &model.Status{Running: false}}}
	for i := 0; i < 3; i++ {
		ts.srv.collectAllMetrics()
	}
	if s := metricsState(t, ts, "n1"); s != model.NodeStateDown {
		t.Fatalf("setup: state = %q, want down", s)
	}

	// Recover: 2 healthy probes → healthy.
	ts.srv.factory = statusFactory{b: statusBackend{status: &model.Status{Running: true}}}
	ts.srv.collectAllMetrics() // oks=1, still down
	ts.srv.collectAllMetrics() // oks=2 → healthy
	if s := metricsState(t, ts, "n1"); s != model.NodeStateHealthy {
		t.Fatalf("state = %q, want healthy after 2 ok", s)
	}
}

// errStr is a trivial error for tests (avoids importing errors/fmt in the test).
type errStr string

func (e errStr) Error() string { return string(e) }

// --- operator block/unblock handler tests (P1a step 4) ----------------------
// These exercise the HTTP handlers; they are kept here with the loop tests so
// the whole health feature has one test file.

// TestMarkNodeBlocked sets state=blocked via POST and verifies it persists +
// an audit entry is written.
func TestMarkNodeBlocked(t *testing.T) {
	ts := newTestServer(t)
	seedHost(t, ts, "n1")

	w := ts.post("/ui/nodes/n1/block", url.Values{"reason": {"DPI block observed"}})
	if w.Code >= 400 {
		t.Fatalf("block: status %d, body %s", w.Code, w.Body.String())
	}
	if s := metricsState(t, ts, "n1"); s != model.NodeStateBlocked {
		t.Fatalf("state = %q, want blocked", s)
	}
	if n := auditHealthCount(t, ts); n != 1 {
		t.Fatalf("audit = %d, want 1 (blocked entry)", n)
	}
	m, _ := ts.srv.store().GetMetrics("n1")
	if m.StateReason != "DPI block observed" {
		t.Fatalf("reason = %q, want the form-supplied reason", m.StateReason)
	}
}

// TestClearNodeBlocked clears a blocked node → state=unknown + audit.
func TestClearNodeBlocked(t *testing.T) {
	ts := newTestServer(t)
	seedHost(t, ts, "n1")
	ts.post("/ui/nodes/n1/block", url.Values{})

	w := ts.post("/ui/nodes/n1/unblock", url.Values{})
	if w.Code >= 400 {
		t.Fatalf("unblock: status %d, body %s", w.Code, w.Body.String())
	}
	if s := metricsState(t, ts, "n1"); s != model.NodeStateUnknown {
		t.Fatalf("state = %q, want unknown (cleared, reclassified next tick)", s)
	}
	if n := auditHealthCount(t, ts); n != 2 { // blocked + unblocked
		t.Fatalf("audit = %d, want 2 (blocked + unblocked)", n)
	}
}

// TestClearNodeBlocked_NotBlocked409 — unblocking a non-blocked node → 409
// (only blocked nodes can be unblocked).
func TestClearNodeBlocked_NotBlocked409(t *testing.T) {
	ts := newTestServer(t)
	seedHost(t, ts, "n1")
	// healthy node (via a healthy collect), not blocked

	w := ts.post("/ui/nodes/n1/unblock", url.Values{})
	if w.Code != http.StatusConflict {
		t.Fatalf("unblock healthy: status %d, want 409", w.Code)
	}
}

// TestMarkNodeBlocked_UnknownHost — blocking a host that doesn't exist → 404
// (no metrics + no host → nothing to mark).
func TestMarkNodeBlocked_UnknownHost(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/nodes/nope/block", url.Values{})
	if w.Code != http.StatusNotFound {
		t.Fatalf("block unknown: status %d, want 404", w.Code)
	}
}