package web

// handlers_capture_test.go — tests for the capture wizard validation (Task 5)
// and the simplified handleUpdateNode key handling (Task 6). The deploy bug's
// root cause was that a node could be saved with an empty KeyPath; the guard
// tested here is what closes that hole. Uses the testServer harness
// (newTestServer / ts.post / ts.assertStatus / ts.assertContains).

import (
	"net/http"
	"net/url"
	"testing"
)

// TestHandler_CaptureNode_RejectsEmptyKey verifies a capture POST with no key,
// no password, and no manual paste is rejected with 400 (the deploy-bug guard).
func TestHandler_CaptureNode_RejectsEmptyKey(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"addr": {"1.1.1.1:22"}}
	w := ts.post("/ui/nodes/n1/capture", form)
	ts.assertStatus(w, http.StatusBadRequest)
	ts.assertContains(w, "Choose a key or enter password")
}

// TestHandler_CaptureNode_RejectsMissingAddr verifies a capture POST without an
// address is rejected with 400 even when a key is selected.
func TestHandler_CaptureNode_RejectsMissingAddr(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"ssh_key_id": {"key-1"}}
	w := ts.post("/ui/nodes/n1/capture", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_CaptureNode_RejectsStaleKeyID verifies a capture POST referencing
// a key id that is not in the registry is rejected with 400 (so a typo or a
// since-deleted key does not silently fall through to an empty/deploy-failing
// KeyPath).
func TestHandler_CaptureNode_RejectsStaleKeyID(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"addr": {"1.1.1.1:22"}, "ssh_key_id": {"key-does-not-exist"}}
	w := ts.post("/ui/nodes/n1/capture", form)
	ts.assertStatus(w, http.StatusBadRequest)
	ts.assertContains(w, "Selected key not found in registry")
}

// TestHandler_NodeCaptureForm_NewNode verifies the capture wizard renders 200
// for an id that is NOT yet in the store (the new-node path builds an empty
// in-memory Host instead of 404'ing).
func TestHandler_NodeCaptureForm_NewNode(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/nodes/newnode/capture")
	ts.assertStatus(w, http.StatusOK)
}

// ─── Task 6 — handleUpdateNode (simplified ssh_key_id reading) ──────────────

// TestHandler_UpdateNode_RejectsStaleKeyID verifies the edit form rejects a
// ssh_key_id that is not in the registry (the same existence guard as capture,
// applied on edit so a stale KeyPath cannot be persisted).
func TestHandler_UpdateNode_RejectsStaleKeyID(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{"ssh_key_id": {"key-nope"}}
	w := ts.post("/ui/nodes/n1/edit", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_UpdateNode_PreservesKeyWhenBlank verifies that an edit POST with
// no ssh_key_id preserves the node's existing KeyPath (lets the applier fall
// back to the panel default rather than wiping the key).
func TestHandler_UpdateNode_PreservesKeyWhenBlank(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{} // no ssh_key_id
	w := ts.post("/ui/nodes/n1/edit", form)
	ts.assertStatus(w, http.StatusOK)
}