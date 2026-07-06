package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_PresetsPage_Renders(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/presets")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Presets")
}

func TestHandler_CreateCustomPreset_AWG(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{
		"name":          {"my-awg"},
		"protocol":      {"awg"},
		"description":   {"custom AWG"},
		"awg_jc":        {"120"},
		"awg_jmin":      {"50"},
		"awg_jmax":      {"1000"},
		"awg_s1":        {"15"},
		"awg_s2":        {"85"},
		"awg_h1":        {"164"},
		"awg_h2":        {"18"},
		"awg_h3":        {"59"},
		"awg_h4":        {"110"},
		"awg_cps_level": {"3"},
		"awg_mimicry":   {"quic"},
	}
	w := ts.post("/ui/presets", form)
	ts.assertStatus(w, http.StatusOK)
	// Verify it persisted in settings.CustomPresets and is resolvable.
	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	var customs []chain.ConnectionPreset
	if err := json.Unmarshal(settings.CustomPresets, &customs); err != nil {
		t.Fatalf("unmarshal CustomPresets: %v", err)
	}
	found := false
	for _, c := range customs {
		if c.Name == "my-awg" {
			found = true
			if c.Protocol != "awg" {
				t.Errorf("Protocol: got %q want awg", c.Protocol)
			}
			if c.AWG == nil || c.AWG.JC != 120 {
				t.Errorf("AWG.JC not saved: %+v", c.AWG)
			}
		}
	}
	if !found {
		t.Errorf("custom preset my-awg not persisted")
	}
	if _, ok := chain.GetPreset("my-awg"); !ok {
		t.Errorf("custom preset not resolvable via GetPreset")
	}
}

func TestHandler_DeletePreset_RefusesIfInUse(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := chain.NewStore(ts.storePath)
	// Create a custom preset + a chain referencing it.
	_ = st.SaveSettings(&model.PanelSettings{CustomPresets: mustJSON([]chain.ConnectionPreset{{Name: "inuse", Protocol: "awg", AWG: &chain.AWGPreset{JC: 1}}})})
	_ = chain.LoadPresets([]chain.ConnectionPreset{{Name: "inuse", Protocol: "awg", AWG: &chain.AWGPreset{JC: 1}}})
	_ = st.SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{{ID: "n1", Addr: "1.1.1.1:22"}}, UserProtocol: model.UserProtocolAWG, ObfuscationProfile: "inuse"})
	w := ts.delete("/ui/presets/inuse")
	ts.assertStatus(w, http.StatusConflict)
}

func TestHandler_DeletePreset_OK(t *testing.T) {
	ts := newTestServer(t)
	st := chain.NewStore(ts.storePath)
	_ = st.SaveSettings(&model.PanelSettings{CustomPresets: mustJSON([]chain.ConnectionPreset{{Name: "free", Protocol: "awg", AWG: &chain.AWGPreset{JC: 1}}})})
	_ = chain.LoadPresets([]chain.ConnectionPreset{{Name: "free", Protocol: "awg", AWG: &chain.AWGPreset{JC: 1}}})
	w := ts.delete("/ui/presets/free")
	ts.assertStatus(w, http.StatusOK)
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}