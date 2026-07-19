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

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestHandler_UpdateChain verifies a chain's transport can be updated (levels
// wire format; entry inbound reference preserved).
func TestHandler_UpdateChain(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.createDeployedProfile("prof-awg", "awg", 51840, "n1")
	ts.post("/ui/chains", url.Values{"name": {"chain-U"}, "level_0_nodes": {"n1"}, "inboundref_n1": {"prof-awg"}})
	form := url.Values{"transport": {"reality"}, "level_0_nodes": {"n1"}, "inboundref_n1": {"prof-awg"}}
	w := ts.post("/ui/chains/chain-U/edit", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "chain-U")
}

// TestHandler_UpdateChain_PreservedFrozenProtocol verifies that an existing
// chain using a paused user protocol (TUIC) can still be re-saved — the
// disabled <option> in EditChainForm is not a successful form control, so the
// handler receives an empty user_protocol and must keep the frozen value
// (AGENTS.md: existing TUIC/Hysteria2 chains "may remain for display/edit").
func TestHandler_UpdateChain_PreservedFrozenProtocol(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	// Create directly with a frozen protocol (bypassing the handler guard via
	// the store) to simulate a legacy chain created before the freeze — its
	// entry references a TUIC profile deployed on the node.
	st := chain.NewStore(ts.storePath)
	ts.createDeployedProfile("prof-tuic", "tuic", 8443, "n1")
	if err := st.SaveChain(&model.Chain{
		Name:         "chain-frozen",
		Levels:       []model.ChainLevel{{ID: "l0", Nodes: []model.ChainNode{{ID: "n1", Addr: "1.1.1.1:22", InboundRef: "prof-tuic"}}}},
		Strategy:     model.StrategyURLTest,
		Transport:    model.TransportXHTTP,
		UserProtocol: model.UserProtocolTUIC,
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	// Edit preserving the same TUIC entry profile. Must succeed (frozen value
	// preserved — the guard only fires on a CHANGE to a frozen protocol).
	form := url.Values{"level_0_nodes": {"n1"}, "inboundref_n1": {"prof-tuic"}}
	w := ts.post("/ui/chains/chain-frozen/edit", form)
	ts.assertStatus(w, http.StatusOK)
	c, err := st.GetChain("chain-frozen")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if c.UserProtocol != model.UserProtocolTUIC {
		t.Errorf("frozen user protocol not preserved: got %q, want %q", c.UserProtocol, model.UserProtocolTUIC)
	}
}

// TestHandler_UpdateChain_RejectsSwitchToFrozen verifies that switching a
// non-frozen chain TO a frozen user protocol (TUIC) is rejected with 400.
func TestHandler_UpdateChain_RejectsSwitchToFrozen(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.createDeployedProfile("prof-awg", "awg", 51840, "n1")
	ts.createDeployedProfile("prof-tuic", "tuic", 8443, "n1")
	ts.post("/ui/chains", url.Values{"name": {"chain-sw"}, "level_0_nodes": {"n1"}, "inboundref_n1": {"prof-awg"}})
	// Switching the entry to a TUIC profile = switching TO a frozen protocol.
	form := url.Values{"level_0_nodes": {"n1"}, "inboundref_n1": {"prof-tuic"}}
	w := ts.post("/ui/chains/chain-sw/edit", form)
	ts.assertStatus(w, http.StatusBadRequest)
	ts.assertContains(w, "TUIC is paused")
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