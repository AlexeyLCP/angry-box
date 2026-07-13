package web

// handlers_clients_test.go — tests for the unified Clients page (handleClients)
// and the MTProxy-aware user create flow. Task 5 of the client unification plan
// (subproject B).
//
// Note on TestHandler_ClientsPage_ShowsMTProxyBadge: asserts that the injected
// MTProxy user's NAME ("mtp-alice") appears in the rendered table (proving the
// migrated user is listed on the Clients page) AND that the "MTProxy" badge is
// rendered next to it (Task 7 added the badge in UserRow).

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_ClientsPage_Renders(t *testing.T) {
	ts := newTestServer(t)
	// Create a user via the helper.
	ts.createUser("u1", "alice")
	w := ts.get("/ui/clients")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alice")
}

// TestHandler_ClientsPage_UsesBaseLayout pins the regression where handleClients
// rendered the Users() fragment via s.render (bare fragment, no <head>) instead
// of s.renderContent (wrapped in templates.Base). Without Base the page had no
// daisyui/tailwind <head> links → the Tokyo Night theme never applied → the
// clients/users page looked unstyled (AGENTS.md #1: UI must render through the
// themed base layout). A themed page carries the data-theme attribute + the
// daisyui stylesheet link; a bare fragment has neither.
func TestHandler_ClientsPage_UsesBaseLayout(t *testing.T) {
	ts := newTestServer(t)
	ts.createUser("u1", "alice")
	// Full-page navigation (no HX-Request) — as a real browser hit. The default
	// ts.get sets HX-Request:true, for which renderContent correctly returns the
	// bare fragment; the bug only showed on a non-HTMX full load.
	w := ts.getWithUA("/ui/clients", "Mozilla/5.0")
	ts.assertStatus(w, http.StatusOK)
	body := w.Body.String()
	// Base layout markers: the <html data-theme="..."> attribute set by
	// base.templ's pre-paint script, and the daisyui stylesheet <link>.
	if !strings.Contains(body, `data-theme="tokyonight"`) {
		t.Errorf("clients page missing base-layout data-theme attribute (rendered bare fragment?):\n%s", truncateBody(body))
	}
	if !strings.Contains(body, "daisyui") {
		t.Errorf("clients page missing daisyui stylesheet link (rendered bare fragment?):\n%s", truncateBody(body))
	}
}

func truncateBody(s string) string {
	const max = 800
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func TestHandler_ClientsPage_ShowsMTProxyBadge(t *testing.T) {
	ts := newTestServer(t)
	// Inject an MTProxy user directly via the store.
	st := chain.NewStore(ts.storePath)
	_ = st.SaveUser(&model.User{ID: "m1", Name: "mtp-alice", Active: true, MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", MTProxyNodes: []string{"n1"}})
	w := ts.get("/ui/clients")
	ts.assertStatus(w, http.StatusOK)
	// The migrated MTProxy user is listed by name, and the MTProxy badge is
	// rendered next to it (Task 7).
	ts.assertContains(w, "mtp-alice")
	ts.assertContains(w, "MTProxy")
}

func TestHandler_CreateUser_WithMTProxy(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{
		"id":              {"u1"},
		"name":            {"alice"},
		"mtproxy_enabled": {"on"},
		"mtproxy_secret":  {"83b231c9ccf32ef09f48c8f63765ab4f"},
		"mtproxy_domain":  {"disk.yandex.ru"},
		"mtproxy_nodes":   {"n1"},
	}
	w := ts.post("/ui/users", form)
	ts.assertStatus(w, http.StatusOK)
	st := chain.NewStore(ts.storePath)
	u, err := st.GetUser("u1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.MTProxySecret != "83b231c9ccf32ef09f48c8f63765ab4f" {
		t.Errorf("MTProxySecret not saved: %q", u.MTProxySecret)
	}
	if len(u.MTProxyNodes) != 1 || u.MTProxyNodes[0] != "n1" {
		t.Errorf("MTProxyNodes: %v", u.MTProxyNodes)
	}
}

func TestHandler_CreateUser_RejectsDuplicateMTProxySecret(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	first := url.Values{"id": {"u1"}, "name": {"alice"}, "mtproxy_enabled": {"on"}, "mtproxy_secret": {"83b231c9ccf32ef09f48c8f63765ab4f"}, "mtproxy_domain": {"disk.yandex.ru"}, "mtproxy_nodes": {"n1"}}
	if w := ts.post("/ui/users", first); w.Code != http.StatusOK {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	dup := url.Values{"id": {"u2"}, "name": {"bob"}, "mtproxy_enabled": {"on"}, "mtproxy_secret": {"83b231c9ccf32ef09f48c8f63765ab4f"}, "mtproxy_domain": {"disk.yandex.ru"}, "mtproxy_nodes": {"n1"}}
	w := ts.post("/ui/users", dup)
	ts.assertStatus(w, http.StatusBadRequest)
}