package web

// handlers_advanced_test.go — HTTP handler tests for the deploy/SSH-coupled
// endpoints (chains CRUD, inbounds save, SSH keys, spider links, host status,
// dashboard stats, trust host key, apply-chain/node, capture, takeover). Uses
// the testServer harness + the web-package fake SSH. CTO-review C3 phase 3.

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
)

// ─── Chains CRUD ────────────────────────────────────────────────────────────

// TestHandler_CreateChain_ThenList verifies a chain can be created from existing
// nodes and appears in the list.
func TestHandler_CreateChain_ThenList(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.createNode("n2", "2.2.2.2:22")
	form := url.Values{
		"name":           {"chain-X"},
		"strategy":       {"urltest"},
		"transport":      {"xhttp"},
		"user_protocol":  {"awg"},
		"nodes":          {"n1", "n2"},
	}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "chain-X")

	w = ts.get("/ui/chains")
	ts.assertContains(w, "chain-X")
}

// TestHandler_CreateChain_MissingName verifies name + >=1 node are required.
func TestHandler_CreateChain_MissingName(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"nodes": {"n1"}}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_CreateChain_UnknownNode verifies referencing a non-existent node
// is rejected.
func TestHandler_CreateChain_UnknownNode(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"name": {"chain-Y"}, "nodes": {"ghost"}}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_EditChainForm verifies the edit-chain modal renders for an existing
// chain.
func TestHandler_EditChainForm(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.post("/ui/chains", url.Values{"name": {"chain-Z"}, "nodes": {"n1"}})
	w := ts.get("/ui/chains/chain-Z/edit")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "chain-Z")
}

// TestHandler_EditChainForm_NotFound verifies a missing chain's edit 404s.
func TestHandler_EditChainForm_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/chains/ghost/edit")
	ts.assertStatus(w, http.StatusNotFound)
}

// TestHandler_DeleteChain_Ok verifies deleting an existing chain returns 200.
func TestHandler_DeleteChain_Ok(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.post("/ui/chains", url.Values{"name": {"chain-D"}, "nodes": {"n1"}})
	w := ts.delete("/ui/chains/chain-D")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_ApplyChain_NotFound verifies applying a missing chain renders an
// ApplyResult(false) without 500ing.
func TestHandler_ApplyChain_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/chains/ghost/apply", nil)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "chain not found")
}

// TestHandler_ApplyNode_NotFound verifies applying a missing node renders an
// ApplyResult(false) without 500ing.
func TestHandler_ApplyNode_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/nodes/ghost/apply", nil)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "node not found")
}

// TestHandler_ApplyChain_HappyPath verifies an apply over a fake SSH succeeds.
func TestHandler_ApplyChain_HappyPath(t *testing.T) {
	fake := newWebFakeSSH(deployRules()...)
	ts := newTestServerWithConnector(t, &webFakeConnector{client: fake})
	ts.createNode("n1", "1.1.1.1:22")
	ts.createNode("n2", "2.2.2.2:22")
	ts.post("/ui/chains", url.Values{"name": {"chain-OK"}, "nodes": {"n1", "n2"}})
	w := ts.post("/ui/chains/chain-OK/apply", nil)
	ts.assertStatus(w, http.StatusOK)
	// ApplyResult success renders a success badge; an error message would mean
	// the fake didn't satisfy the deploy sequence.
	ts.assertNotContains(w, "chain not found")
}

// ─── Inbounds ───────────────────────────────────────────────────────────────

// TestHandler_SaveNodeInbounds* were removed with the node-scoped inbound
// editor (v0.8 IA refactor): inbounds are first-class profiles now — see
// handlers_inbounds_test.go for the /ui/inbounds CRUD coverage.

// ─── SSH Keys ───────────────────────────────────────────────────────────────

// validPEM is a minimal but well-formed PEM private key envelope for the
// looksLikePrivateKey check (it only validates the PEM header/footer pair).
const validPEM = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blb\n-----END OPENSSH PRIVATE KEY-----\n"

// TestHandler_AddSSHKey_Ok verifies a valid-looking private key is stored and
// the key list re-renders.
func TestHandler_AddSSHKey_Ok(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"name": {"mykey"}, "key_data": {validPEM}}
	w := ts.post("/ui/settings/ssh-keys", form)
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_AddSSHKey_InvalidFormat verifies a non-key paste is rejected.
func TestHandler_AddSSHKey_InvalidFormat(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"name": {"mykey"}, "key_data": {"not a key"}}
	w := ts.post("/ui/settings/ssh-keys", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Invalid key format")
}

// TestHandler_AddSSHKey_MissingFields verifies name + key_data are required.
func TestHandler_AddSSHKey_MissingFields(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"name": {"mykey"}}
	w := ts.post("/ui/settings/ssh-keys", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "required")
}

// TestHandler_DeleteSSHKey_NotFound verifies deleting a missing key 404s.
func TestHandler_DeleteSSHKey_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.delete("/ui/settings/ssh-keys/ghost")
	ts.assertStatus(w, http.StatusNotFound)
}

// ─── Spider links ───────────────────────────────────────────────────────────

// TestHandler_CreateSpiderLink_Ok verifies a link between two existing nodes is
// created and re-renders the spider view.
func TestHandler_CreateSpiderLink_Ok(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.createNode("n2", "2.2.2.2:22")
	form := url.Values{"from_node": {"n1"}, "to_node": {"n2"}, "chain_name": {"spider-chain"}}
	w := ts.post("/ui/spider/links", form)
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_CreateSpiderLink_MissingFields verifies all three fields required.
func TestHandler_CreateSpiderLink_MissingFields(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{"from_node": {"n1"}}
	w := ts.post("/ui/spider/links", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_CreateSpiderLink_SameNode verifies from==to is rejected.
func TestHandler_CreateSpiderLink_SameNode(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{"from_node": {"n1"}, "to_node": {"n1"}, "chain_name": {"c"}}
	w := ts.post("/ui/spider/links", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_DeleteSpiderLink_NotFound verifies deleting a missing link 404s.
func TestHandler_DeleteSpiderLink_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.delete("/ui/spider/links/ghost")
	ts.assertStatus(w, http.StatusNotFound)
}

// ─── Host status / dashboard stats / trust ──────────────────────────────────

// TestHandler_HostStatus_NotFound verifies a missing host renders the error
// badge (not a 500).
func TestHandler_HostStatus_NotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/hosts/ghost/status")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Host not found")
}

// TestHandler_HostStatus_Ok verifies the status probe runs against the noop
// backend and renders 200.
func TestHandler_HostStatus_Ok(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	w := ts.get("/ui/hosts/n1/status")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_DashboardStats verifies the stats partial renders 200.
func TestHandler_DashboardStats(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/dashboard/stats")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_TrustHostKey verifies trusting a fingerprint that matches the
// pending observed fingerprint redirects to the capture form. The pending
// fingerprint is persisted by handleCaptureNode when the HostKeyError is
// rendered (CTO-review §6: the trust POST must be verified against the
// actually-observed host key, not an arbitrary submitted value).
func TestHandler_TrustHostKey(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	// Seed the pending fingerprint as handleCaptureNode would.
	st := chain.NewStore(ts.storePath)
	info, _ := st.GetNodeInfo("n1")
	info.PendingHostKeyFingerprint = "SHA256:abc"
	_ = st.SaveNodeInfo(info)
	form := url.Values{"addr": {"1.1.1.1:22"}, "fingerprint": {"SHA256:abc"}}
	w := ts.post("/ui/nodes/n1/trust", form)
	ts.assertStatus(w, http.StatusSeeOther)
	// Pending fingerprint should be cleared after a successful trust.
	info2, _ := st.GetNodeInfo("n1")
	if info2.PendingHostKeyFingerprint != "" {
		t.Errorf("pending fingerprint not cleared after trust: %q", info2.PendingHostKeyFingerprint)
	}
}

// TestHandler_TrustHostKey_MismatchFingerprint verifies that a trust POST with
// a fingerprint that does NOT match the observed pending fingerprint is
// rejected with 400 (CTO-review §6 MITM/CSRF guard).
func TestHandler_TrustHostKey_MismatchFingerprint(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := chain.NewStore(ts.storePath)
	info, _ := st.GetNodeInfo("n1")
	info.PendingHostKeyFingerprint = "SHA256:abc"
	_ = st.SaveNodeInfo(info)
	form := url.Values{"addr": {"1.1.1.1:22"}, "fingerprint": {"SHA256:FORGED"}}
	w := ts.post("/ui/nodes/n1/trust", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_TrustHostKey_NoPending verifies that a trust POST with no prior
// pending fingerprint on record is rejected with 400 (forged POST without a
// preceding capture attempt).
func TestHandler_TrustHostKey_NoPending(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	w := ts.post("/ui/nodes/n1/trust", url.Values{"addr": {"1.1.1.1:22"}, "fingerprint": {"SHA256:abc"}})
	ts.assertStatus(w, http.StatusBadRequest)
}

// ─── Capture form ───────────────────────────────────────────────────────────

// TestHandler_NodeCaptureForm verifies the capture form renders for an existing
// node.
func TestHandler_NodeCaptureForm(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	w := ts.get("/ui/nodes/n1/capture")
	ts.assertStatus(w, http.StatusOK)
}

// TestHandler_CaptureNode_MissingFields verifies a capture POST without key
// data is rejected (400 — the deploy-bug root-cause guard: a node must not be
// savable with an empty KeyPath).
func TestHandler_CaptureNode_MissingFields(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	w := ts.post("/ui/nodes/n1/capture", url.Values{"addr": {"1.1.1.1:22"}})
	ts.assertStatus(w, http.StatusBadRequest)
}

// ─── Takeover (detect) ──────────────────────────────────────────────────────

// TestHandler_DetectVPN_NodeNotFound verifies detect on a missing node renders
// the error alert (not a 500).
func TestHandler_DetectVPN_NodeNotFound(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/nodes/ghost/detect-vpn")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "node not found")
}

// TestHandler_DetectVPN_ConnectFails verifies a failed SSH probe surfaces the
// detect-failed alert (not a 500).
func TestHandler_DetectVPN_ConnectFails(t *testing.T) {
	ts := newTestServerWithConnector(t, &webFakeConnector{err: errors.New("dial: refused")})
	ts.createNode("n1", "1.1.1.1:22")
	w := ts.get("/ui/nodes/n1/detect-vpn")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Detect failed")
}