package web

// handlers_clients_test.go — tests for the unified Clients page (handleClients)
// and the MTProxy-aware user create flow. Task 5 of the client unification plan
// (subproject B).
//
// Note on TestHandler_ClientsPage_ShowsMTProxyBadge: the Users template does not
// yet render an MTProxy badge (Task 7 adds it), so this test asserts only that
// the injected MTProxy user's NAME ("mtp-alice") appears in the rendered table —
// proving the migrated user is listed on the Clients page. The badge itself is a
// Task 7 template concern.

import (
	"net/http"
	"net/url"
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

func TestHandler_ClientsPage_ShowsMTProxyBadge(t *testing.T) {
	ts := newTestServer(t)
	// Inject an MTProxy user directly via the store.
	st := chain.NewStore(ts.storePath)
	_ = st.SaveUser(&model.User{ID: "m1", Name: "mtp-alice", Active: true, MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", MTProxyNodes: []string{"n1"}})
	w := ts.get("/ui/clients")
	ts.assertStatus(w, http.StatusOK)
	// The Users template does not yet render an MTProxy badge (Task 7 adds it),
	// so assert only that the migrated MTProxy user is listed by name.
	ts.assertContains(w, "mtp-alice")
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