package web

// handlers_inbounds_test.go — /ui/inbounds (first-class InboundProfile) CRUD
// coverage. The node-scoped inbound editor was removed in the v0.8 IA
// refactor; inbounds are created here and deployed onto nodes via checkboxes.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_InboundsPage_Renders(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/inbounds")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Inbounds")
}

func TestHandler_CreateInbound_MaterializesOnCheckedNodes(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.createNode("n2", "2.2.2.2:22")
	form := url.Values{
		"name":     {"Main AWG"},
		"protocol": {"awg"},
		"port":     {"51840"},
		"node_ids": {"n1", "n2"},
	}
	w := ts.post("/ui/inbounds", form)
	ts.assertStatus(w, http.StatusOK)
	if loc := w.Header().Get("HX-Redirect"); loc != "/ui/inbounds" {
		t.Errorf("HX-Redirect = %q", loc)
	}
	st := ts.srv.store()
	profs, _ := st.ListInboundProfiles()
	if len(profs) != 1 {
		t.Fatalf("want 1 profile, got %d", len(profs))
	}
	p := profs[0]
	if p.Name != "Main AWG" || p.Protocol != "awg" || p.Port != 51840 {
		t.Errorf("profile: %+v", p)
	}
	// Both nodes materialized with per-node creds.
	for _, id := range []string{"n1", "n2"} {
		ib := st.ProfileInboundOn(id, p.ID)
		if ib == nil {
			t.Fatalf("no materialized inbound on %s", id)
		}
		if ib.ServerPrivKey == "" || ib.AWGServerAddress == "" {
			t.Errorf("%s creds/subnet missing: %+v", id, ib)
		}
	}
	// Page lists the profile with node badges.
	w = ts.get("/ui/inbounds")
	ts.assertContains(w, "Main AWG")
	ts.assertContains(w, "n1")
}

func TestHandler_CreateInbound_Validation(t *testing.T) {
	ts := newTestServer(t)
	// Missing name.
	w := ts.post("/ui/inbounds", url.Values{"protocol": {"awg"}, "port": {"51840"}})
	ts.assertStatus(w, http.StatusBadRequest)
	// Frozen protocol.
	w = ts.post("/ui/inbounds", url.Values{"name": {"x"}, "protocol": {"tuic"}, "port": {"443"}})
	ts.assertStatus(w, http.StatusBadRequest)
	// Bad port.
	w = ts.post("/ui/inbounds", url.Values{"name": {"x"}, "protocol": {"awg"}, "port": {"99999"}})
	ts.assertStatus(w, http.StatusBadRequest)
}

func TestHandler_UpdateInbound_DiffSemantics(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	ts.createNode("n2", "2.2.2.2:22")
	st := ts.srv.store()
	// Create with n1 only.
	w := ts.post("/ui/inbounds", url.Values{
		"name": {"P"}, "protocol": {"awg"}, "port": {"51840"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusOK)
	profs, _ := st.ListInboundProfiles()
	pid := profs[0].ID
	credsBefore := st.ProfileInboundOn("n1", pid).ServerPrivKey

	// Update: add n2, change port — n1 creds must be preserved.
	w = ts.post("/ui/inbounds/"+pid+"/edit", url.Values{
		"name": {"P"}, "protocol": {"awg"}, "port": {"51841"}, "node_ids": {"n1", "n2"},
	})
	ts.assertStatus(w, http.StatusOK)
	if ib := st.ProfileInboundOn("n1", pid); ib.ServerPrivKey != credsBefore {
		t.Error("n1 creds rotated on update (must be generated exactly once)")
	} else if ib.Port != 51841 {
		t.Errorf("port not synced on kept node: %d", ib.Port)
	}
	if ib := st.ProfileInboundOn("n2", pid); ib == nil {
		t.Error("n2 not materialized on update")
	}

	// Update: uncheck n2 → materialization removed.
	w = ts.post("/ui/inbounds/"+pid+"/edit", url.Values{
		"name": {"P"}, "protocol": {"awg"}, "port": {"51841"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusOK)
	if ib := st.ProfileInboundOn("n2", pid); ib != nil {
		t.Error("n2 materialization not removed")
	}
}

func TestHandler_UpdateInbound_ProtocolLocked(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := ts.srv.store()
	w := ts.post("/ui/inbounds", url.Values{
		"name": {"P"}, "protocol": {"awg"}, "port": {"51840"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusOK)
	profs, _ := st.ListInboundProfiles()
	pid := profs[0].ID
	w = ts.post("/ui/inbounds/"+pid+"/edit", url.Values{
		"name": {"P"}, "protocol": {"vless-reality"}, "port": {"51840"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusConflict)
	ts.assertContains(w, "Protocol change requires recreating")
}

func TestHandler_DeleteInbound_RefusesWhenChainReferences(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := ts.srv.store()
	w := ts.post("/ui/inbounds", url.Values{
		"name": {"P"}, "protocol": {"awg"}, "port": {"51840"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusOK)
	profs, _ := st.ListInboundProfiles()
	pid := profs[0].ID

	// Chain references the profile on n1.
	if err := st.SaveChain(&model.Chain{
		Name: "c1",
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "n1", Addr: "n1:22", InboundRef: pid}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "n2", Addr: "n2:22"}}},
		},
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	w = ts.delete("/ui/inbounds/" + pid)
	ts.assertStatus(w, http.StatusConflict)
	ts.assertContains(w, "in use by a chain")
	if _, err := st.GetInboundProfile(pid); err != nil {
		t.Error("profile deleted despite chain reference")
	}

	// Remove the reference → delete succeeds and strips the materialization.
	c, _ := st.GetChain("c1")
	c.Levels[0].Nodes[0].InboundRef = ""
	if err := st.SaveChain(c); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	w = ts.delete("/ui/inbounds/" + pid)
	ts.assertStatus(w, http.StatusOK)
	if _, err := st.GetInboundProfile(pid); err == nil {
		t.Error("profile not deleted")
	}
	if ib := st.ProfileInboundOn("n1", pid); ib != nil {
		t.Error("materialization left behind after delete")
	}
}

func TestHandler_CreateInbound_PortConflict(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	// Existing standalone inbound on 443.
	w := ts.post("/ui/inbounds", url.Values{
		"name": {"A"}, "protocol": {"vless-reality"}, "port": {"443"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusOK)
	// Second profile on the same port → conflict.
	w = ts.post("/ui/inbounds", url.Values{
		"name": {"B"}, "protocol": {"awg"}, "port": {"443"}, "node_ids": {"n1"},
	})
	if w.Code != http.StatusConflict {
		t.Errorf("want 409 port conflict, got %d (body: %s)", w.Code, truncate(w.Body.String(), 200))
	}
	if !strings.Contains(w.Body.String(), "already used") {
		t.Errorf("conflict message: %s", truncate(w.Body.String(), 200))
	}
}
