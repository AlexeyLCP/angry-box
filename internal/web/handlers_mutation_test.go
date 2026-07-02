package web

// handlers_mutation_test.go — HTTP handler tests for the mutating endpoints
// (node/user/profile/chain CRUD, settings save, spider links). Uses the
// testServer harness. CTO-review C3 phase 3.

import (
	"net/http"
	"net/url"
	"testing"
)

// createNode helper: POSTs a node and returns its id (assumes success).
func (ts *testServer) createNode(id, addr string) {
	ts.t.Helper()
	form := url.Values{"id": {id}, "addr": {addr}, "user": {"root"}, "keyPath": {"/key"}}
	w := ts.post("/ui/nodes", form)
	if w.Code != http.StatusOK {
		ts.t.Fatalf("createNode %s: got %d, want 200 (body: %s)", id, w.Code, w.Body.String())
	}
}

// createUser helper: POSTs a user and returns its id.
func (ts *testServer) createUser(id, name string) {
	ts.t.Helper()
	form := url.Values{"id": {id}, "name": {name}}
	w := ts.post("/ui/users", form)
	if w.Code != http.StatusOK {
		ts.t.Fatalf("createUser %s: got %d, want 200 (body: %s)", id, w.Code, w.Body.String())
	}
}

// createProfile helper.
func (ts *testServer) createProfile(name string) {
	ts.t.Helper()
	form := url.Values{"name": {name}, "client_type": {"user"}, "server_role": {"any"}}
	w := ts.post("/ui/profiles", form)
	if w.Code != http.StatusSeeOther {
		ts.t.Fatalf("createProfile %s: got %d, want 303", name, w.Code)
	}
}

// ─── Nodes ──────────────────────────────────────────────────────────────────

// TestHandler_DeleteNode_Ok verifies deleting an existing node returns 200 + empty.
func TestHandler_DeleteNode_Ok(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("node-D", "1.2.3.4:22")
	w := ts.delete("/ui/nodes/node-D")
	ts.assertStatus(w, http.StatusOK)
	if w.Body.String() != "" {
		t.Errorf("delete body: got %q, want empty", w.Body.String())
	}
}

// TestHandler_DeleteNode_NotFound verifies deleting a missing node surfaces an
// error row (200 with an error banner — the HTMX swap path).
func TestHandler_DeleteNode_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.delete("/ui/nodes/ghost")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Failed to delete")
}

// TestHandler_EditNodeForm verifies the edit form renders for an existing node.
func TestHandler_EditNodeForm(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("node-E", "5.6.7.8:22")
	w := ts.get("/ui/nodes/node-E/edit")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "node-E")
}

// TestHandler_EditNodeForm_NotFound verifies a missing node's edit form 404s.
func TestHandler_EditNodeForm_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/nodes/ghost/edit")
	ts.assertStatus(w, http.StatusNotFound)
}

// TestHandler_UpdateNode verifies a node's metadata can be updated.
func TestHandler_UpdateNode(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("node-F", "1.2.3.4:22")
	form := url.Values{
		"addr":      {"9.9.9.9:22"},
		"user":      {"root"},
		"keyPath":   {"/key"},
		"country":   {"DE"},
		"bandwidth": {"100"},
	}
	w := ts.post("/ui/nodes/node-F/edit", form)
	ts.assertStatus(w, http.StatusOK)
	// Verify the change persisted by re-listing.
	w = ts.get("/ui/nodes")
	ts.assertContains(w, "9.9.9.9:22")
}

// TestHandler_NodeInboundsForm verifies the inbounds form renders for an existing node.
func TestHandler_NodeInboundsForm(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("node-G", "1.2.3.4:22")
	w := ts.get("/ui/nodes/node-G/inbounds")
	ts.assertStatus(w, http.StatusOK)
}

// ─── Users ──────────────────────────────────────────────────────────────────

// TestHandler_UsersList_Empty verifies the users page renders 200 on an empty store.
func TestHandler_UsersList_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/users")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_CreateUser_ThenList verifies a user is created (inline row) and
// appears in the list.
func TestHandler_CreateUser_ThenList(t *testing.T) {
	ts := newTestServer(t)
	ts.createUser("user-1", "Alice")
	w := ts.get("/ui/users")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Alice")
}

// TestHandler_CreateUser_MissingFields verifies id+name are required.
func TestHandler_CreateUser_MissingFields(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"id": {""}, "name": {""}}
	w := ts.post("/ui/users", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_DeleteUser verifies deleting a user returns 200.
func TestHandler_DeleteUser(t *testing.T) {
	ts := newTestServer(t)
	ts.createUser("user-2", "Bob")
	w := ts.delete("/ui/users/user-2")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_EditUserForm verifies the edit-user form renders.
func TestHandler_EditUserForm(t *testing.T) {
	ts := newTestServer(t)
	ts.createUser("user-3", "Carol")
	w := ts.get("/ui/users/user-3/edit")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "user-3")
}

// TestHandler_EditUserForm_NotFound verifies a missing user's edit form 404s.
func TestHandler_EditUserForm_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/users/ghost/edit")
	ts.assertStatus(w, http.StatusNotFound)
}

// TestHandler_UserConfig verifies the client config endpoint returns 200.
func TestHandler_UserConfig(t *testing.T) {
	ts := newTestServer(t)
	ts.createUser("user-4", "Dave")
	w := ts.get("/ui/users/user-4/config")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_UserConfig_NotFound verifies a missing user's config 404s.
func TestHandler_UserConfig_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/users/ghost/config")
	ts.assertStatus(w, http.StatusNotFound)
}

// ─── Profiles ───────────────────────────────────────────────────────────────

// TestHandler_DeleteProfile verifies deleting a profile returns 200.
func TestHandler_DeleteProfile(t *testing.T) {
	ts := newTestServer(t)
	ts.createProfile("p1")
	pid := ts.profileID("p1")
	w := ts.delete("/ui/profiles/" + pid)
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_EditProfileForm verifies the edit-profile modal renders.
func TestHandler_EditProfileForm(t *testing.T) {
	ts := newTestServer(t)
	ts.createProfile("p2")
	pid := ts.profileID("p2")
	w := ts.get("/ui/profiles/" + pid + "/edit")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "p2")
}

// TestHandler_EditProfileForm_NotFound verifies a missing profile's edit 404s.
func TestHandler_EditProfileForm_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/profiles/ghost/edit")
	ts.assertStatus(w, http.StatusNotFound)
}

// TestHandler_UpdateProfile verifies a profile can be renamed.
func TestHandler_UpdateProfile(t *testing.T) {
	ts := newTestServer(t)
	ts.createProfile("p3")
	pid := ts.profileID("p3")
	form := url.Values{"name": {"p3-renamed"}, "client_type": {"user"}, "server_role": {"any"}}
	w := ts.post("/ui/profiles/"+pid+"/edit", form)
	ts.assertStatus(w, http.StatusSeeOther)
}

// ─── Chains ─────────────────────────────────────────────────────────────────

// TestHandler_ChainsList_Empty verifies the chains page renders 200 on empty.
func TestHandler_ChainsList_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/chains")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_NewChainForm verifies the new-chain modal renders.
func TestHandler_NewChainForm(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/chains/new")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_DeleteChain_NotFound verifies deleting a missing chain 404s.
func TestHandler_DeleteChain_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.delete("/ui/chains/ghost")
	ts.assertStatus(w, http.StatusNotFound)
}

// ─── Settings ───────────────────────────────────────────────────────────────

// TestHandler_SettingsView verifies the settings page renders 200.
func TestHandler_SettingsView(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/settings")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_SaveSettings_PanelOnly verifies saving non-auth panel settings
// (language/country) returns the success alert (auth is disabled in the harness
// so no old-password check runs).
func TestHandler_SaveSettings_PanelOnly(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{
		"language":          {"ru"},
		"panel_country":     {"RU"},
		"metrics_interval":  {"15"},
		"default_protocol":  {"awg"},
		"ssh_key_name":      {"k1"},
		"ssh_key_path":      {"/key1"},
	}
	w := ts.post("/ui/settings", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Settings saved")
}

// ─── Spider ─────────────────────────────────────────────────────────────────

// TestHandler_SpiderWeb_Empty verifies the spider-web editor renders 200.
func TestHandler_SpiderWeb_Empty(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/spider")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_SaveNodePosition verifies a node position save returns 200.
func TestHandler_SaveNodePosition(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("node-H", "1.2.3.4:22")
	form := url.Values{"x": {"100"}, "y": {"200"}}
	w := ts.post("/ui/spider/nodes/node-H/position", form)
	ts.assertStatus(w, http.StatusOK)
}