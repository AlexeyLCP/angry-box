package web

// handlers_mutation_test.go — HTTP handler tests for the mutating endpoints
// (node/user/profile/chain CRUD, settings save, spider links). Uses the
// testServer harness. CTO-review C3 phase 3.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// createNode helper: registers a node directly via the store (bypassing the
// capture wizard's SSH probe) so the rest of the test suite — which expects a
// saved host — works without hitting a real SSH connection. The old helper
// POSTed /ui/nodes with a keyPath textarea; that route is gone (Task 5.2) and
// the canonical entry point is now the capture wizard. Saving via the store
// keeps all ~29 call sites working without editing them.
func (ts *testServer) createNode(id, addr string) {
	ts.t.Helper()
	st := chain.NewStore(ts.storePath)
	host := &model.Host{ID: id, Addr: addr, User: "root", KeyPath: "/key"}
	if err := st.SaveHost(host); err != nil {
		ts.t.Fatalf("createNode %s: SaveHost: %v", id, err)
	}
	if err := st.SaveNodeInfo(&model.NodeInfo{
		Host:   *host,
		Source: "ssh_key",
	}); err != nil {
		ts.t.Fatalf("createNode %s: SaveNodeInfo: %v", id, err)
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

// NOTE: The /ui/users LIST route now 301-redirects to /ui/clients (Task 6 of the
// client unification plan). The list-rendering behavior it used to verify is
// covered by TestHandler_ClientsPage_Renders / TestHandler_ClientsPage_ShowsMTProxyBadge
// in handlers_clients_test.go.

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
// TestHandler_SaveSettings_LanguagePersists verifies a saved language change
// is actually persisted AND applied on the next request — the settings page
// re-rendered shows the new language's option selected, and i18n.T renders the
// ru string for a known key (catches a regression where the save silently no-
// op'd or the auth middleware didn't re-read the language).
func TestHandler_SaveSettings_LanguagePersists(t *testing.T) {
	ts := newTestServer(t)
	// Save with language=ru.
	form := url.Values{
		"language":         {"ru"},
		"panel_country":    {"RU"},
		"metrics_interval": {"15"},
		"default_protocol": {"awg"},
	}
	w := ts.post("/ui/settings", form)
	ts.assertStatus(w, http.StatusOK)
	// HX-Refresh must be set so the browser reloads to pick up the new language.
	if got := w.Header().Get("HX-Refresh"); got != "true" {
		t.Errorf("HX-Refresh header = %q, want \"true\" (language changed → page must reload)", got)
	}
	// Re-render the settings page: the ru option must now be selected.
	w2 := ts.get("/ui/settings")
	ts.assertStatus(w2, http.StatusOK)
	body := w2.Body.String()
	if !strings.Contains(body, `value="ru" selected>Русский`) {
		t.Errorf("settings page did not reflect saved language=ru; ru option not selected.\nbody excerpt:\n%s", excerpt(body, `language`))
	}
	// A known ru translation key must render the ru string on the page (proves
	// i18n.T picked up the language from the auth middleware).
	if !strings.Contains(w2.Body.String(), "Настройки") && !strings.Contains(w2.Body.String(), "Сохранить") {
		// Some settings-page titles may not be in ru dict; check the page title
		// "Settings" → ru. If neither ru string appears, the language wasn't
		// applied to i18n.T at all.
		t.Errorf("settings page body has no ru i18n strings — language not applied to i18n.T")
	}
}

// TestHandler_SettingsView_NoNestedFormsInMainForm is the regression for the
// language-switch bug (2026-07-08): the SSHKeyList component rendered its own
// <form> elements (add/import/test/delete) INSIDE the main settings <form>,
// but HTML forbids nested <form> — the browser closed the outer form at the
// first nested one, which dropped the Save Settings button (and the language
// select's submit) out of the form. The fix moved #ssh-keys-list + the add-key
// form outside the main <form>. This test pins the structure: the main settings
// form (hx-post="/ui/settings") must contain NO nested <form> and must contain
// the Save Settings submit button.
func TestHandler_SettingsView_NoNestedFormsInMainForm(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/settings")
	ts.assertStatus(w, http.StatusOK)
	body := w.Body.String()

	// The "Add New SSH Key" heading must appear exactly once (the duplicate
	// add-key form inside SSHKeyList was the second copy — both visible on the
	// page). Count occurrences of the heading text.
	addKeyHeading := "Add New SSH Key"
	if got := strings.Count(body, addKeyHeading); got != 1 {
		t.Errorf("Add New SSH Key heading appears %d times, want 1 (duplicate add-key form regression):\n%s", got, excerpt(body, addKeyHeading))
	}

	// The main settings form must open before the Save Settings button and
	// close AFTER it (so the button is inside the form). We check the ordering:
	// form-open tag, then the Save Settings button, then form-close — by
	// locating each in the body and comparing offsets.
	formOpen := strings.Index(body, `hx-post="/ui/settings"`)
	if formOpen < 0 {
		t.Fatal(`main settings form hx-post="/ui/settings" not found`)
	}
	// The Save Settings button is the non-sm primary submit ("Save Settings"
	// label, en in the test harness which defaults to en).
	saveBtn := strings.Index(body, ">Save Settings<")
	if saveBtn < 0 {
		t.Fatal(`Save Settings button not found on settings page`)
	}
	if saveBtn < formOpen {
		t.Errorf("Save Settings button (offset %d) appears BEFORE the main form open (offset %d) — it must be inside the form", saveBtn, formOpen)
	}
	// The #ssh-keys-list block (which contains nested <form>s) must sit AFTER
	// the main form closes, not inside it. The main form closes with </form>;
	// ssh-keys-list must appear after that first </form>.
	firstFormClose := strings.Index(body, "</form>")
	if firstFormClose < 0 {
		t.Fatal("no </form> on settings page")
	}
	sshKeysList := strings.Index(body, `id="ssh-keys-list"`)
	if sshKeysList < 0 {
		t.Fatal(`#ssh-keys-list not found`)
	}
	if sshKeysList < firstFormClose {
		t.Errorf("#ssh-keys-list (offset %d) is INSIDE the main settings form (closes at %d) — nested <form> would break the Save Settings submit (language-switch regression)", sshKeysList, firstFormClose)
	}
}

// TestHandler_SaveSettings_PreservesSSHKeys is the regression for the data-loss
// bug (2026-07-08): handleSaveSettings rebuilt settings.SSHKeys from
// ssh_key_name/ssh_key_path form fields — a legacy pre-v0.2.5 schema. After the
// redesign the main settings form no longer carries those fields (keys are
// added via /ui/settings/ssh-keys with PEM key_data), so this block clobbered
// settings.SSHKeys to an empty slice on every Save Settings. Saving the language
// wiped all imported keys. The fix removed the clobber. This test pre-seeds a
// key, saves panel-only settings, and asserts the key survives.
func TestHandler_SaveSettings_PreservesSSHKeys(t *testing.T) {
	ts := newTestServer(t)
	// Pre-seed an SSH key directly in the store (the way /ui/settings/ssh-keys
	// would after an add).
	st := ts.srv.store()
	seedSettings, err := st.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	seedSettings.SSHKeys = []model.SSHKeyEntry{{
		ID:   "key-seed",
		Name: "seed-key",
		// A dummy private-key blob is enough — the save path doesn't re-parse it.
		KeyData:      "-----BEGIN OPENSSH PRIVATE KEY-----\nseed\n-----END OPENSSH PRIVATE KEY-----",
		Fingerprint:  "deadbeef",
		Source:       "manual",
	}}
	if err := st.SaveSettings(seedSettings); err != nil {
		t.Fatalf("seed SaveSettings: %v", err)
	}

	// Save panel-only settings (language/country/metrics/protocol) — the form
	// the UI actually sends today (NO ssh_key_name/ssh_key_path).
	form := url.Values{
		"language":         {"ru"},
		"panel_country":    {"RU"},
		"metrics_interval": {"15"},
		"default_protocol": {"awg"},
	}
	w := ts.post("/ui/settings", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Settings saved")

	// The seeded key must survive the save.
	got, err := st.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings after save: %v", err)
	}
	var found bool
	for _, k := range got.SSHKeys {
		if k.ID == "key-seed" {
			found = true
			if k.KeyData != seedSettings.SSHKeys[0].KeyData {
				t.Errorf("seeded key KeyData changed: got %q, want %q", k.KeyData, seedSettings.SSHKeys[0].KeyData)
			}
		}
	}
	if !found {
		t.Errorf("seeded SSH key key-seed was wiped by SaveSettings (data-loss regression); SSHKeys=%+v", got.SSHKeys)
	}
	// And the language must have persisted (the save still works).
	if got.Language != "ru" {
		t.Errorf("Language = %q, want ru (save should still persist panel settings)", got.Language)
	}
}

// keep chain import used (for the store() return type assertion in the
// preserves-keys test if the harness widens later).
var _ = chain.ConfigHash

// excerpt returns a small window of body around the first occurrence of needle
// (for readable test failure messages without dumping the whole page).
func excerpt(body, needle string) string {
	i := strings.Index(body, needle)
	if i < 0 {
		return "(needle not found)"
	}
	start := i - 80
	if start < 0 {
		start = 0
	}
	end := i + 120
	if end > len(body) {
		end = len(body)
	}
	return body[start:end]
}
