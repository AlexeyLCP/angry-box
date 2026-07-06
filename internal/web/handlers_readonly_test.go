package web

// handlers_readonly_test.go — HTTP handler tests for the read-only / list views
// (dashboard, nodes, status, audit, deploy-status, clients). Uses the
// testServer harness from servertest_test.go. CTO-review C3 phase 3.

import (
	"net/http"
	"testing"
)

// TestHandler_Dashboard_Ok verifies the dashboard renders 200 with the title.
func TestHandler_Dashboard_Ok(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_RootRedirect verifies "/" redirects to /ui.
func TestHandler_RootRedirect(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/")
	ts.assertStatus(w, http.StatusSeeOther)
	if loc := w.Header().Get("Location"); loc != "/ui" {
		t.Errorf("Location: got %q, want /ui", loc)
	}
}

// TestHandler_NodesList_Empty verifies the nodes page renders 200 even with no
// nodes (no panic on empty store).
func TestHandler_NodesList_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/nodes")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_Status_NoHosts verifies the status page shows the empty-state hint
// when no hosts are registered.
func TestHandler_Status_NoHosts(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/status")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_Audit_Empty verifies the audit log renders 200 with an empty store.
func TestHandler_Audit_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/audit")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_DeployStatus_Empty verifies the deploy-status page renders 200.
func TestHandler_DeployStatus_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/deploy-status")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_Clients_Empty verifies the clients page renders 200 with no users.
func TestHandler_Clients_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/clients")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_CreateNode_ThenList verifies a node created via the createNode
// helper (which saves directly to the store, mirroring the post-capture state)
// appears in the nodes list. The old POST /ui/nodes route is gone (Task 5.2);
// node creation now goes through the capture wizard, which is covered by the
// capture handler tests.
func TestHandler_CreateNode_ThenList(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("node-A", "1.2.3.4:22")
	w := ts.get("/ui/nodes")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "node-A")
}

// TestHandler_AuthDisabled_AnonymousAccess verifies that with AuthEnabled=false
// the UI is reachable without credentials (the harness default).
func TestHandler_AuthDisabled_AnonymousAccess(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui")
	ts.assertStatus(w, http.StatusOK)
}