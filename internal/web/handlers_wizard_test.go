package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// seedServiceForWizard stores a Service in PanelSettings.Services so the
// wizard create path can pick it.
func seedServiceForWizard(t *testing.T, ts *testServer, svc model.Service) {
	t.Helper()
	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	var services []model.Service
	if len(settings.Services) > 0 {
		_ = json.Unmarshal(settings.Services, &services)
	}
	services = append(services, svc)
	b, _ := json.Marshal(services)
	settings.Services = b
	st.SaveSettings(settings)
}

// TestWizard_CreateUser_WithService_ExpandsFields verifies that picking a
// Service in the wizard expands it into the user's ChainNames/Protocols/
// ChainExit/MTProxy/ServiceID, and that EnsureUserCreds runs (AWGAddress
// populated for the AWG protocol).
func TestWizard_CreateUser_WithService_ExpandsFields(t *testing.T) {
	ts := newTestServer(t)
	seedServiceForWizard(t, ts, model.Service{
		ID:                 "tg-pro",
		Name:               "Telegram Pro",
		ChainNames:         []string{"c1"},
		Protocols:          []string{"awg"},
		DefaultExitByChain: map[string]string{"c1": "exit-1"},
	})
	// Seed the chain c1 so EnsureUserCreds/AWG-address allocation works.
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name: "c1", UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{{ID: "exit-1", Addr: "1.2.3.4:22"}},
	}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"id":         {"alice"},
		"name":       {"Alice"},
		"service_id": {"tg-pro"},
	}
	w := ts.post("/ui/users", form)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got, err := st.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.ServiceID != "tg-pro" {
		t.Errorf("ServiceID = %q, want tg-pro", got.ServiceID)
	}
	if len(got.ChainNames) != 1 || got.ChainNames[0] != "c1" {
		t.Errorf("ChainNames = %v, want [c1]", got.ChainNames)
	}
	if got.ChainExit["c1"] != "exit-1" {
		t.Errorf("ChainExit = %v, want c1=exit-1", got.ChainExit)
	}
	if got.AWGAddress == "" {
		t.Error("AWGAddress empty — EnsureUserCreds/EnsureUserAWGAddress did not run")
	}
	if got.SubscriptionToken == "" {
		t.Error("SubscriptionToken empty — create should mint one")
	}
	if got.ExpireStrategy != "never" {
		t.Errorf("ExpireStrategy = %q, want never (default)", got.ExpireStrategy)
	}
}

// TestWizard_CreateUser_CustomPath_ChainExitExposed verifies the Custom path
// reads exit_<chainName> form values into User.ChainExit — the first UI
// surface for the already-wired ChainExit map.
func TestWizard_CreateUser_CustomPath_ChainExitExposed(t *testing.T) {
	ts := newTestServer(t)
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name: "c1", UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{{ID: "n1", Addr: "1.2.3.4:22"}, {ID: "n2", Addr: "5.6.7.8:22"}},
	}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"id":      {"bob"},
		"name":    {"Bob"},
		"chains":  {"c1"},
		"exit_c1": {"n2"},
		// no service_id → Custom path
	}
	w := ts.post("/ui/users", form)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	got, err := st.GetUser("bob")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.ServiceID != "" {
		t.Errorf("ServiceID = %q, want empty (Custom path)", got.ServiceID)
	}
	if got.ChainExit["c1"] != "n2" {
		t.Errorf("ChainExit = %v, want c1=n2", got.ChainExit)
	}
}

// TestWizard_ExpireStrategy_StartOnFirstUse_StatusOnHold verifies that a
// start_on_first_use user (never fetched yet) gets Status=on_hold.
func TestWizard_ExpireStrategy_StartOnFirstUse_StatusOnHold(t *testing.T) {
	ts := newTestServer(t)
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name: "c1", UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{{ID: "n1", Addr: "1.2.3.4:22"}},
	}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"id":              {"carol"},
		"name":            {"Carol"},
		"service_id":      {"tg-pro"},
		"expire_strategy": {"start_on_first_use"},
		"usage_duration":  {"2592000"},
	}
	seedServiceForWizard(t, ts, model.Service{ID: "tg-pro", Name: "TP", ChainNames: []string{"c1"}, Protocols: []string{"awg"}})
	w := ts.post("/ui/users", form)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	got, err := st.GetUser("carol")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Status != "on_hold" {
		t.Errorf("Status = %q, want on_hold", got.Status)
	}
	if got.UsageDuration != 2592000 {
		t.Errorf("UsageDuration = %d, want 2592000", got.UsageDuration)
	}
}

// TestWizard_CreateUser_MintsSubToken verifies the create path mints a non-
// empty subscription token (the lazy backfill path is separate; new users
// always get one at create).
func TestWizard_CreateUser_MintsSubToken(t *testing.T) {
	ts := newTestServer(t)
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name: "c1", UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{{ID: "n1", Addr: "1.2.3.4:22"}},
	}); err != nil {
		t.Fatal(err)
	}
	ts.post("/ui/users", url.Values{"id": {"dave"}, "name": {"Dave"}, "chains": {"c1"}})
	got, _ := st.GetUser("dave")
	if got.SubscriptionToken == "" {
		t.Error("create did not mint a subscription token")
	}
	// And the token must resolve back to the user.
	resolved, err := st.GetUserBySubscriptionToken(got.SubscriptionToken)
	if err != nil || resolved.ID != "dave" {
		t.Errorf("token did not resolve to dave: %v", resolved)
	}
}

// TestWizard_ExpiredUser_StatusExpired verifies the create path sets Status=
// expired when ExpiresAt is in the past + fixed_date strategy.
func TestWizard_ExpiredUser_StatusExpired(t *testing.T) {
	ts := newTestServer(t)
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name: "c1", UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{{ID: "n1", Addr: "1.2.3.4:22"}},
	}); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).Format("2006-01-02")
	ts.post("/ui/users", url.Values{
		"id":              {"eve"},
		"name":            {"Eve"},
		"chains":          {"c1"},
		"expire_strategy": {"fixed_date"},
		"expires_at":      {past},
	})
	got, _ := st.GetUser("eve")
	if got.Status != "expired" {
		t.Errorf("Status = %q, want expired", got.Status)
	}
}