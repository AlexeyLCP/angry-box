package web

// handlers_clone_test.go — HTTP tests for /ui/nodes/{id}/clone (P1b).
//
// CloneNode re-applies every chain containing the source node, which dials SSH.
// The handler tests use a source node in ZERO chains so CloneNode's per-chain
// re-apply loop is empty — this exercises the handler wiring (form parse, new
// ID collision check, Host/NodeInfo creation via the store, CloneResult render)
// without a fake SSH connector. The full multi-chain re-apply + fresh-identity
// flow is covered by the chain-package clone tests (clone_test.go) with a fake
// applier.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// seedSourceForClone builds a source node (no chains) with identity + config so
// the clone's fresh identity + copied config can be asserted.
func seedSourceForClone(t *testing.T, ts *testServer) {
	t.Helper()
	st := ts.srv.store()
	st.SaveHost(&model.Host{ID: "src", Addr: "1.1.1.1:22", User: "root", KeyPath: "/key"})
	st.SaveNodeInfo(&model.NodeInfo{
		Host:    model.Host{ID: "src", Addr: "1.1.1.1:22", User: "root", KeyPath: "/key"},
		Country: "RU", Bandwidth: "1Gbps",
		Inbounds: []model.NodeInbound{{
			Protocol: "vless-reality", Port: 443, ForUsers: []string{"u1"},
			UUID: "SRC-UUID", ServerPrivKey: "SRC-PRIV", ShortID: "src-sid", Tag: "src-tag",
		}},
	})
}

// TestHandler_CloneForm_Renders — GET /ui/nodes/{id}/clone renders the modal 200
// with the source ID + the new-ID field.
func TestHandler_CloneForm_Renders(t *testing.T) {
	ts := newTestServer(t)
	seedSourceForClone(t, ts)

	w := ts.get("/ui/nodes/src/clone")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Clone node")
	ts.assertContains(w, "src")
	ts.assertContains(w, "new_id")
}

// TestHandler_CloneForm_UnknownNode — GET on a missing node → 404.
func TestHandler_CloneForm_UnknownNode(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/nodes/ghost/clone")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}

// TestHandler_CloneNode_OK — POST with a new ID + addr creates the clone (Host +
// NodeInfo with FRESH identity + COPIED config) and renders a success result.
// No chains → empty re-apply loop → no SSH dial.
func TestHandler_CloneNode_OK(t *testing.T) {
	ts := newTestServer(t)
	seedSourceForClone(t, ts)

	w := ts.post("/ui/nodes/src/clone", url.Values{
		"new_id":   {"clone1"},
		"new_addr": {"9.9.9.9:22"},
	})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "clone1")
	ts.assertContains(w, "9.9.9.9:22")

	// Clone Host exists with the new addr.
	h, err := ts.srv.store().GetHost("clone1")
	if err != nil || h.Addr != "9.9.9.9:22" {
		t.Fatalf("clone host = %+v err %v", h, err)
	}
	// Clone NodeInfo: fresh inbound identity + copied config.
	ci, _ := ts.srv.store().GetNodeInfo("clone1")
	if ci == nil {
		t.Fatal("clone nodeinfo missing")
	}
	if ci.Country != "RU" {
		t.Fatalf("clone Country = %q, want RU (copied)", ci.Country)
	}
	cib := ci.Inbounds[0]
	if cib.UUID == "" || cib.UUID == "SRC-UUID" {
		t.Fatalf("clone inbound UUID = %q, want fresh", cib.UUID)
	}
	if cib.ServerPrivKey == "SRC-PRIV" {
		t.Fatal("clone inbound ServerPrivKey == SRC-PRIV (not fresh)")
	}
	if len(cib.ForUsers) != 1 || cib.ForUsers[0] != "u1" {
		t.Fatalf("clone ForUsers = %v, want [u1] (copied)", cib.ForUsers)
	}
	// Source untouched.
	si, _ := ts.srv.store().GetNodeInfo("src")
	if si.Inbounds[0].UUID != "SRC-UUID" {
		t.Fatalf("source inbound identity changed: %+v", si.Inbounds[0])
	}
}

// TestHandler_CloneNode_EmptyNewID — missing new_id → error alert, no clone.
func TestHandler_CloneNode_EmptyNewID(t *testing.T) {
	ts := newTestServer(t)
	seedSourceForClone(t, ts)

	w := ts.post("/ui/nodes/src/clone", url.Values{"new_addr": {"9.9.9.9:22"}})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
	ts.assertContains(w, "New node ID is required")
	if _, err := ts.srv.store().GetHost("clone1"); err == nil {
		t.Fatal("clone created despite empty new_id")
	}
}

// TestHandler_CloneNode_EmptyAddr — missing new_addr → error alert.
func TestHandler_CloneNode_EmptyAddr(t *testing.T) {
	ts := newTestServer(t)
	seedSourceForClone(t, ts)

	w := ts.post("/ui/nodes/src/clone", url.Values{"new_id": {"clone1"}})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
	ts.assertContains(w, "new address is required")
}

// TestHandler_CloneNode_DuplicateID — a new_id that already exists → error alert
// (CloneNode's collision check), no overwrite of the existing node.
func TestHandler_CloneNode_DuplicateID(t *testing.T) {
	ts := newTestServer(t)
	seedSourceForClone(t, ts)
	ts.srv.store().SaveHost(&model.Host{ID: "taken", Addr: "5.5.5.5:22", User: "root", KeyPath: "/k"})

	w := ts.post("/ui/nodes/src/clone", url.Values{
		"new_id":   {"taken"},
		"new_addr": {"9.9.9.9:22"},
	})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
	ts.assertContains(w, "already exists")
	// The pre-existing "taken" host is unchanged.
	h, _ := ts.srv.store().GetHost("taken")
	if h.Addr != "5.5.5.5:22" {
		t.Fatalf("existing host 'taken' was overwritten: %+v", h)
	}
}

// TestHandler_CloneNode_SameID — new_id == source → error alert.
func TestHandler_CloneNode_SameID(t *testing.T) {
	ts := newTestServer(t)
	seedSourceForClone(t, ts)

	w := ts.post("/ui/nodes/src/clone", url.Values{
		"new_id":   {"src"},
		"new_addr": {"9.9.9.9:22"},
	})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
}

// TestHandler_CloneNode_BadSSHKey — a stale SSH key id → error alert (rejected
// before cloning, like relocate).
func TestHandler_CloneNode_BadSSHKey(t *testing.T) {
	ts := newTestServer(t)
	seedSourceForClone(t, ts)

	w := ts.post("/ui/nodes/src/clone", url.Values{
		"new_id":         {"clone1"},
		"new_addr":       {"9.9.9.9:22"},
		"new_ssh_key_id": {"does-not-exist"},
	})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
	ts.assertContains(w, "Selected key not found in registry")
	if _, err := ts.srv.store().GetHost("clone1"); err == nil {
		t.Fatal("clone created despite bad SSH key")
	}
}