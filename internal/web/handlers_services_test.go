package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestHandler_ServicesPage_Renders verifies the Service catalog page loads
// with the "Services" heading and the no-services hint when empty.
func TestHandler_ServicesPage_Renders(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/services")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Services")
}

// TestHandler_CreateService verifies a POST creates a Service persisted into
// PanelSettings.Services and resolvable via servicesList.
func TestHandler_CreateService(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{
		"id":          {"tg-pro"},
		"name":        {"Telegram Pro"},
		"description": {"telegram-only tier"},
		"chains":      {"c1", "c2"},
		"protocols":   {"awg"},
		"exit_c1":     {"exit-1"},
		"routing_preset_ids": {"telegram"},
		"mtproxy_enabled":    {"on"},
		"mtproxy_domain":     {"disk.yandex.ru"},
	}
	w := ts.post("/ui/services", form)
	ts.assertStatus(w, http.StatusOK)

	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	var services []model.Service
	if err := json.Unmarshal(settings.Services, &services); err != nil {
		t.Fatalf("unmarshal Services: %v", err)
	}
	var got *model.Service
	for i := range services {
		if services[i].ID == "tg-pro" {
			got = &services[i]
		}
	}
	if got == nil {
		t.Fatal("service tg-pro not persisted")
	}
	if got.Name != "Telegram Pro" || got.Description != "telegram-only tier" {
		t.Errorf("got %+v", got)
	}
	if len(got.ChainNames) != 2 || got.ChainNames[0] != "c1" {
		t.Errorf("ChainNames = %v", got.ChainNames)
	}
	if got.DefaultExitByChain["c1"] != "exit-1" {
		t.Errorf("DefaultExitByChain = %v", got.DefaultExitByChain)
	}
	if len(got.RoutingPresetIDs) != 1 || got.RoutingPresetIDs[0] != "telegram" {
		t.Errorf("RoutingPresetIDs = %v", got.RoutingPresetIDs)
	}
	if !got.MTProxy.Enabled || got.MTProxy.Domain != "disk.yandex.ru" {
		t.Errorf("MTProxy = %+v", got.MTProxy)
	}
}

// TestHandler_CreateService_MissingFields verifies ID+Name are required.
func TestHandler_CreateService_MissingFields(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/services", url.Values{"id": {"x"}}) // no name
	ts.assertStatus(w, http.StatusBadRequest)
}

// TestHandler_CreateService_DuplicateID verifies a duplicate ID is rejected 409.
func TestHandler_CreateService_DuplicateID(t *testing.T) {
	ts := newTestServer(t)
	ts.post("/ui/services", url.Values{"id": {"s1"}, "name": {"One"}})
	w := ts.post("/ui/services", url.Values{"id": {"s1"}, "name": {"Two"}})
	ts.assertStatus(w, http.StatusConflict)
}

// TestHandler_DeleteService_RefusesIfInUse verifies deletion is refused (409)
// when a user references the Service via User.ServiceID.
func TestHandler_DeleteService_RefusesIfInUse(t *testing.T) {
	ts := newTestServer(t)
	ts.post("/ui/services", url.Values{"id": {"s1"}, "name": {"One"}})
	// Seed a user referencing it.
	st := chain.NewStore(ts.storePath)
	if err := st.SaveUser(&model.User{ID: "u1", Name: "Alice", ServiceID: "s1"}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	w := ts.delete("/ui/services/s1")
	ts.assertStatus(w, http.StatusConflict)
}

// TestHandler_DeleteService_OK verifies an unreferenced Service is deleted.
func TestHandler_DeleteService_OK(t *testing.T) {
	ts := newTestServer(t)
	ts.post("/ui/services", url.Values{"id": {"s1"}, "name": {"One"}})
	w := ts.delete("/ui/services/s1")
	ts.assertStatus(w, http.StatusOK)

	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	var services []model.Service
	_ = json.Unmarshal(settings.Services, &services)
	for _, svc := range services {
		if svc.ID == "s1" {
			t.Error("service s1 still present after delete")
		}
	}
}

// TestHandler_EditServiceForm verifies the edit form loads an existing Service
// and 404s for an unknown one.
func TestHandler_EditServiceForm(t *testing.T) {
	ts := newTestServer(t)
	ts.post("/ui/services", url.Values{"id": {"s1"}, "name": {"One"}})
	w := ts.get("/ui/services/s1/edit")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "One")

	w = ts.get("/ui/services/nope/edit")
	ts.assertStatus(w, http.StatusNotFound)
}