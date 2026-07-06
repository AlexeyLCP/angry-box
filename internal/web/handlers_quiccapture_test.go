package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_CreateChain_WithQuicLive(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{
		"name":                   {"chain-q"},
		"nodes":                  {"n1"},
		"user_protocol":          {"awg"},
		"awg_cps_mimicry":        {"quic-live"},
		"awg_cps_capture_domain": {"disk.yandex.ru"},
	}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusOK)
	st := chain.NewStore(ts.storePath)
	c, err := st.GetChain("chain-q")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if c.AWGCPSMimicry != "quic-live" {
		t.Errorf("AWGCPSMimicry: got %q want quic-live", c.AWGCPSMimicry)
	}
	if c.AWGCPSCaptureDomain != "disk.yandex.ru" {
		t.Errorf("AWGCPSCaptureDomain: got %q want disk.yandex.ru", c.AWGCPSCaptureDomain)
	}
}

func TestHandler_CreateChain_InvalidMimicry(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{
		"name":            {"chain-bad"},
		"nodes":           {"n1"},
		"user_protocol":   {"awg"},
		"awg_cps_mimicry": {"bogus"},
	}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

func TestHandler_CreateChain_InvalidCaptureDomain(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{
		"name":                   {"chain-bad2"},
		"nodes":                  {"n1"},
		"user_protocol":          {"awg"},
		"awg_cps_mimicry":        {"quic-live"},
		"awg_cps_capture_domain": {"not a domain!!!"},
	}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

func TestHandler_UpdateChain_ResetsCaptureCacheOnDomainChange(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := chain.NewStore(ts.storePath)
	// Seed a chain that already has a captured domain.
	if err := st.SaveChain(&model.Chain{
		Name:                 "chain-c",
		Nodes:                []model.ChainNode{{ID: "n1", Addr: "1.1.1.1:22"}},
		Strategy:             model.StrategyURLTest,
		Transport:            model.TransportXHTTP,
		UserProtocol:         model.UserProtocolAWG,
		AWGCPSMimicry:        "quic-live",
		AWGCPSCaptureDomain:  "old.example.com",
		AWGCPSCapturedDomain: "old.example.com",
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	form := url.Values{
		"strategy":               {"urltest"},
		"awg_cps_mimicry":        {"quic-live"},
		"awg_cps_capture_domain": {"new.example.com"},
	}
	w := ts.post("/ui/chains/chain-c/edit", form)
	ts.assertStatus(w, http.StatusOK)
	c, err := st.GetChain("chain-c")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if c.AWGCPSCaptureDomain != "new.example.com" {
		t.Errorf("CaptureDomain: got %q want new.example.com", c.AWGCPSCaptureDomain)
	}
	if c.AWGCPSCapturedDomain != "" {
		t.Errorf("CapturedDomain should be reset on domain change, got %q", c.AWGCPSCapturedDomain)
	}
}

func TestHandler_UpdateChain_ClearsCaptureWhenMimicryLeavesQuicLive(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name:                 "chain-d",
		Nodes:                []model.ChainNode{{ID: "n1", Addr: "1.1.1.1:22"}},
		Strategy:             model.StrategyURLTest,
		Transport:            model.TransportXHTTP,
		UserProtocol:         model.UserProtocolAWG,
		AWGCPSMimicry:        "quic-live",
		AWGCPSCaptureDomain:  "disk.yandex.ru",
		AWGCPSCapturedDomain: "disk.yandex.ru",
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	form := url.Values{
		"strategy":        {"urltest"},
		"awg_cps_mimicry": {"quic"},
	}
	w := ts.post("/ui/chains/chain-d/edit", form)
	ts.assertStatus(w, http.StatusOK)
	c, err := st.GetChain("chain-d")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if c.AWGCPSMimicry != "quic" {
		t.Errorf("Mimicry: got %q want quic", c.AWGCPSMimicry)
	}
	if c.AWGCPSCaptureDomain != "" {
		t.Errorf("CaptureDomain should be cleared when leaving quic-live, got %q", c.AWGCPSCaptureDomain)
	}
	if c.AWGCPSCapturedDomain != "" {
		t.Errorf("CapturedDomain should be cleared when leaving quic-live, got %q", c.AWGCPSCapturedDomain)
	}
}

func TestHandler_CapturePreview_RejectsEmptyDomain(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/chains/capture-preview", url.Values{})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Invalid capture domain")
}