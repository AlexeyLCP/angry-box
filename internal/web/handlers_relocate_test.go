package web

// handlers_relocate_test.go — HTTP tests for /ui/nodes/{id}/relocate.
//
// RelocateNode re-applies every chain containing the node, which dials SSH.
// The handler tests use a node that is in ZERO chains so RelocateNode's
// per-chain re-apply loop is empty — this exercises the handler wiring (form
// parse, addr update via the store, RelocateResult render) without a fake SSH
// connector. The full multi-chain re-apply + key-reuse flow is covered by the
// chain-package relocate tests (relocate_test.go) with a fake applier.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestHandler_RelocateNode_UpdatesAddr verifies POST /ui/nodes/{id}/relocate
// with a new addr updates the Host + renders a RelocateResult (no chains →
// all-success vacuously; the alert is alert-success because there were no
// failures).
func TestHandler_RelocateNode_UpdatesAddr(t *testing.T) {
	ts := newTestServer(t)
	st := ts.srv.store()
	st.SaveHost(&model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root", KeyPath: "/key"})
	st.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root"}})

	w := ts.post("/ui/nodes/n1/relocate", url.Values{"new_addr": {"9.9.9.9:22"}})
	ts.assertStatus(w, http.StatusOK)
	// RelocateResult renders the new addr in the success alert.
	ts.assertContains(w, "9.9.9.9:22")
	// The Host was actually updated.
	h, err := st.GetHost("n1")
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if h.Addr != "9.9.9.9:22" {
		t.Errorf("Host.Addr = %q, want 9.9.9.9:22 (handler did not update the store)", h.Addr)
	}
}

// TestHandler_RelocateNode_EmptyAddr verifies a missing new_addr renders the
// error alert (no store mutation).
func TestHandler_RelocateNode_EmptyAddr(t *testing.T) {
	ts := newTestServer(t)
	st := ts.srv.store()
	st.SaveHost(&model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root", KeyPath: "/key"})

	w := ts.post("/ui/nodes/n1/relocate", url.Values{"new_addr": {""}})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
	ts.assertContains(w, "new address is required")
	// Addr unchanged.
	h, _ := st.GetHost("n1")
	if h.Addr != "1.1.1.1:22" {
		t.Errorf("Host.Addr changed on empty-addr submit: %q", h.Addr)
	}
}

// TestHandler_RelocateNode_UnknownNode verifies a missing node ID renders an
// error (RelocateNode returns ErrHostNotFound).
func TestHandler_RelocateNode_UnknownNode(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/nodes/ghost/relocate", url.Values{"new_addr": {"9.9.9.9:22"}})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
}

// TestHandler_RelocateNode_BadSSHKey verifies a stale SSH key id is rejected
// before any addr mutation (the relocate would fail to dial otherwise).
func TestHandler_RelocateNode_BadSSHKey(t *testing.T) {
	ts := newTestServer(t)
	st := ts.srv.store()
	st.SaveHost(&model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root", KeyPath: "/key"})

	w := ts.post("/ui/nodes/n1/relocate", url.Values{
		"new_addr":       {"9.9.9.9:22"},
		"new_ssh_key_id": {"does-not-exist"},
	})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
	ts.assertContains(w, "Selected key not found in registry")
	// Addr unchanged (rejected before RelocateNode).
	h, _ := st.GetHost("n1")
	if h.Addr != "1.1.1.1:22" {
		t.Errorf("Host.Addr changed despite bad SSH key: %q", h.Addr)
	}
}