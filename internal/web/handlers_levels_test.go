package web

// handlers_levels_test.go — chain levels editor form + spider levels adaptation
// (v0.8): create/edit chains via the levels wire format, entry inbound
// validation, spider synthetic edges from levels, spider guards for levelized
// chains.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_CreateChain_LevelsMesh(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("e1", "1.1.1.1:22")
	ts.createNode("e2", "2.2.2.2:22")
	ts.createNode("x1", "3.3.3.3:22")
	ts.createNode("x2", "4.4.4.4:22")
	ts.createDeployedProfile("prof-awg", "awg", 51840, "e1", "e2")

	w := ts.post("/ui/chains", url.Values{
		"name":              {"mesh"},
		"transport":         {"xhttp"},
		"level_0_nodes":     {"e1", "e2"},
		"inboundref_e1":     {"prof-awg"},
		"inboundref_e2":     {"prof-awg"},
		"level_1_nodes":     {"x1", "x2"},
		"level_1_strategy":  {"urltest"},
	})
	ts.assertStatus(w, http.StatusOK)

	st := ts.srv.store()
	c, err := st.GetChain("mesh")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if !c.IsLevelized() || len(c.Levels) != 2 {
		t.Fatalf("levels: %+v", c.Levels)
	}
	if len(c.Levels[0].Nodes) != 2 || len(c.Levels[1].Nodes) != 2 {
		t.Fatalf("level groups: %+v", c.Levels)
	}
	if c.Levels[1].Strategy != model.StrategyURLTest {
		t.Errorf("level strategy: %q", c.Levels[1].Strategy)
	}
	for _, n := range c.Levels[0].Nodes {
		if n.InboundRef != "prof-awg" {
			t.Errorf("entry %s InboundRef = %q", n.ID, n.InboundRef)
		}
	}
	// Entry materialization happened at save (client links work pre-apply).
	if ib := st.ProfileInboundOn("e1", "prof-awg"); ib == nil {
		t.Error("entry inbound not materialized on e1")
	}
}

func TestHandler_CreateChain_RequiresDeployedInbound(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	// Profile exists but is NOT deployed on n1.
	ts.createDeployedProfile("prof-awg", "awg", 51840)
	w := ts.post("/ui/chains", url.Values{
		"name":          {"c1"},
		"level_0_nodes": {"n1"},
		"inboundref_n1": {"prof-awg"},
	})
	ts.assertStatus(w, http.StatusBadRequest)
	ts.assertContains(w, "not deployed on node")
}

func TestHandler_CreateChain_AWGTransportRejectsGroups(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("e1", "1.1.1.1:22")
	ts.createNode("x1", "2.2.2.2:22")
	ts.createNode("x2", "3.3.3.3:22")
	ts.createDeployedProfile("prof-awg", "awg", 51840, "e1")
	w := ts.post("/ui/chains", url.Values{
		"name":          {"awg-mesh"},
		"transport":     {"awg"},
		"level_0_nodes": {"e1"},
		"inboundref_e1": {"prof-awg"},
		"level_1_nodes": {"x1", "x2"},
	})
	ts.assertStatus(w, http.StatusBadRequest)
	ts.assertContains(w, "single-node levels")
}

func TestHandler_UpdateChain_PreservesTransitKeys(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("e1", "1.1.1.1:22")
	ts.createNode("x1", "2.2.2.2:22")
	ts.createDeployedProfile("prof-awg", "awg", 51840, "e1")
	st := ts.srv.store()
	// Seed a chain with transit material on the exit node (as ApplyChain would).
	c := &model.Chain{
		Name:      "c1",
		Transport: model.TransportXHTTP,
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "e1", Addr: "e1:22", InboundRef: "prof-awg"}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "x1", Addr: "x1:22", TransitPrivKey: "KEEP", TransitUUID: "KEEP-U", TransitShortID: "KEEP-S"}}},
		},
	}
	if err := st.SaveChain(c); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	// Edit: same topology, different transport preset — keys must survive.
	w := ts.post("/ui/chains/c1/edit", url.Values{
		"transport":     {"reality"},
		"level_0_nodes": {"e1"},
		"inboundref_e1": {"prof-awg"},
		"level_1_nodes": {"x1"},
	})
	ts.assertStatus(w, http.StatusOK)
	c2, _ := st.GetChain("c1")
	if got := c2.Levels[1].Nodes[0].TransitPrivKey; got != "KEEP" {
		t.Errorf("transit key rotated on edit: %q", got)
	}
	if c2.Transport != model.TransportReality {
		t.Errorf("transport not updated: %q", c2.Transport)
	}
}

func TestHandler_Spider_SyntheticEdgesFromLevels(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("e1", "1.1.1.1:22")
	ts.createNode("x1", "2.2.2.2:22")
	ts.createNode("x2", "3.3.3.3:22")
	ts.createDeployedProfile("prof-awg", "awg", 51840, "e1")
	w := ts.post("/ui/chains", url.Values{
		"name":          {"mesh"},
		"level_0_nodes": {"e1"},
		"inboundref_e1": {"prof-awg"},
		"level_1_nodes": {"x1", "x2"},
	})
	ts.assertStatus(w, http.StatusOK)

	// The spider page shows the levelized chain's mesh as synthetic edges.
	w = ts.get("/ui/spider")
	ts.assertStatus(w, http.StatusOK)
	body := w.Body.String()
	if !strings.Contains(body, "e1") || !strings.Contains(body, "x1") || !strings.Contains(body, "x2") {
		t.Errorf("spider page missing mesh nodes")
	}
	if !strings.Contains(body, "levels") {
		t.Errorf("spider page missing the levels badge for synthetic edges")
	}
}

func TestHandler_SpiderLink_RefusedForLevelizedChain(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("e1", "1.1.1.1:22")
	ts.createNode("x1", "2.2.2.2:22")
	ts.createDeployedProfile("prof-awg", "awg", 51840, "e1")
	w := ts.post("/ui/chains", url.Values{
		"name":          {"c1"},
		"level_0_nodes": {"e1"},
		"inboundref_e1": {"prof-awg"},
		"level_1_nodes": {"x1"},
	})
	ts.assertStatus(w, http.StatusOK)

	w = ts.post("/ui/spider/links", url.Values{
		"from_node":  {"e1"},
		"to_node":    {"x1"},
		"chain_name": {"c1"},
		"transport":  {"xhttp"},
	})
	ts.assertStatus(w, http.StatusConflict)
	ts.assertContains(w, "uses levels")
}
