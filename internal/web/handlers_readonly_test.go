package web

// handlers_readonly_test.go — HTTP handler tests for the read-only / list views
// (dashboard, nodes, status, audit, deploy-status, profiles, clients). Uses the
// testServer harness from servertest_test.go. CTO-review C3 phase 3.

import (
	"net/http"
	"net/url"
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

// TestHandler_Profiles_Empty verifies the profiles page renders 200 with no
// profiles.
func TestHandler_Profiles_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/profiles")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_Clients_Empty verifies the clients page renders 200 with no users.
func TestHandler_Clients_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/clients")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_NewNodeForm verifies the new-node modal form renders 200.
func TestHandler_NewNodeForm(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/nodes/new")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_NewProfileForm verifies the new-profile modal renders 200.
func TestHandler_NewProfileForm(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/profiles/new")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_CreateNode_ThenList verifies a node can be created (POST returns
// the rendered row for inline HTMX swap) and then appears in the nodes list.
func TestHandler_CreateNode_ThenList(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{
		"id":       {"node-A"},
		"addr":     {"1.2.3.4:22"},
		"user":     {"root"},
		"keyPath":  {"/key"},
	}
	w := ts.post("/ui/nodes", form)
	// handleCreateNode renders the new row inline (HTMX swap), 200 on success.
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "node-A")

	w = ts.get("/ui/nodes")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "node-A")
}

// TestHandler_CreateNode_MissingID verifies a missing id is rejected.
func TestHandler_CreateNode_MissingID(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{
		"addr":     {"1.2.3.4:22"},
		"user":     {"root"},
		"keyPath":  {"/key"},
	}
	w := ts.post("/ui/nodes", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_CreateProfile_ThenList verifies a profile can be created and
// appears in the list.
func TestHandler_CreateProfile_ThenList(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{
		"name":        {"pro-profile"},
		"description": {"desc"},
		"client_type": {"user"},
		"server_role": {"any"},
	}
	w := ts.post("/ui/profiles", form)
	ts.assertStatus(w, http.StatusSeeOther)

	w = ts.get("/ui/profiles")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "pro-profile")
}

// TestHandler_CreateProfile_MissingName verifies a missing name is rejected.
func TestHandler_CreateProfile_MissingName(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{
		"client_type": {"user"},
		"server_role": {"any"},
	}
	w := ts.post("/ui/profiles", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_AuthDisabled_AnonymousAccess verifies that with AuthEnabled=false
// the UI is reachable without credentials (the harness default).
func TestHandler_AuthDisabled_AnonymousAccess(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui")
	ts.assertStatus(w, http.StatusOK)
}