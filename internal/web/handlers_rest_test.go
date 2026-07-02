package web

// handlers_rest_test.go — covers the remaining handler branches to push web
// coverage toward 90%: update-chain, update-user, qr-image, user-qr, delete-
// assignment, new-user-form, takeover (node-not-found + connect-fails).
// CTO-review C3 phase 3.

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

// TestHandler_UpdateChain verifies a chain's strategy/transport can be updated.
func TestHandler_UpdateChain(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.post("/ui/chains", url.Values{"name": {"chain-U"}, "nodes": {"n1"}})
	form := url.Values{"strategy": {"random"}, "transport": {"reality"}, "user_protocol": {"tuic"}}
	w := ts.post("/ui/chains/chain-U/edit", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "chain-U")
}

// TestHandler_UpdateChain_NotFound verifies updating a missing chain 404s.
func TestHandler_UpdateChain_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/chains/ghost/edit", url.Values{"strategy": {"urltest"}})
	ts.assertStatus(w, http.StatusNotFound)
}

// TestHandler_NewUserForm verifies the new-user modal renders 200.
func TestHandler_NewUserForm(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/users/new")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_UpdateUser verifies a user's fields can be updated.
func TestHandler_UpdateUser(t *testing.T) {
	ts := newTestServer(t)
	ts.createUser("u1", "Alice")
	form := url.Values{
		"name":      {"Alice2"},
		"telegram":  {"@alice"},
		"email":     {"a@b.c"},
		"protocols": {"awg"},
		"active":    {"on"},
	}
	w := ts.post("/ui/users/u1/edit", form)
	// HTMX path renders the updated row (200).
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_UpdateUser_NotFound verifies updating a missing user 404s.
func TestHandler_UpdateUser_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/users/ghost/edit", url.Values{"name": {"x"}})
	ts.assertStatus(w, http.StatusNotFound)
}

// TestHandler_QRImage_Ok verifies a QR PNG is generated for a data param.
func TestHandler_QRImage_Ok(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/qr-image?data=vless://uuid@host:443")
	ts.assertStatus(w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type: got %q, want image/png", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("QR body is empty")
	}
}

// TestHandler_QRImage_MissingData verifies a missing data param is rejected.
func TestHandler_QRImage_MissingData(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/qr-image")
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_UserQR_NotFound verifies a QR for a missing user 404s.
func TestHandler_UserQR_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/users/ghost/qr")
	ts.assertStatus(w, http.StatusNotFound)
}

// TestHandler_UserQR_Ok verifies the QR page renders for a user with no chains.
func TestHandler_UserQR_Ok(t *testing.T) {
	ts := newTestServer(t)
	ts.createUser("u1", "Alice")
	w := ts.get("/ui/users/u1/qr")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_DeleteAssignment verifies deleting an existing assignment 200s.
func TestHandler_DeleteAssignment(t *testing.T) {
	ts := newTestServer(t)
	ts.createProfile("p1")
	pid := ts.profileID("p1")
	// Create an assignment first.
	ts.post("/ui/profiles/"+pid+"/assignments", url.Values{"client_type": {"user"}, "client_id": {"u1"}})
	// Resolve the assignment id from the store.
	aid := ts.assignmentID(pid, "user", "u1")
	w := ts.delete("/ui/profiles/" + pid + "/assignments/" + aid)
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_Takeover_NodeNotFound verifies takeover on a missing node renders
// the error alert (not a 500).
func TestHandler_Takeover_NodeNotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/nodes/ghost/takeover", nil)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "node not found")
}

// TestHandler_Takeover_ConnectFails verifies a failed SSH probe during takeover
// surfaces the detect-failed alert.
func TestHandler_Takeover_ConnectFails(t *testing.T) {
	ts := newTestServerWithConnector(t, &webFakeConnector{err: errors.New("dial: refused")})
	ts.createNode("n1", "1.1.1.1:22")
	w := ts.post("/ui/nodes/n1/takeover", nil)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Detect failed")
}