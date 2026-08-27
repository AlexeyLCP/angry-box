package web

// routing_ui_test.go — HTTP tests for the per-node manual routing panel
// (RouteRule CRUD + validation + panel render).

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_RouteRuleCRUD(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := ts.srv.store()

	// Create a domain_suffix rule.
	w := ts.post("/ui/nodes/n1/routing", url.Values{
		"match_type": {"domain_suffix"}, "match_values": {"example.com, cdn.example.com"},
		"action": {"direct"}, "priority": {"10"}, "comment": {"test rule"},
	})
	ts.assertStatus(w, http.StatusOK)
	rules, _ := st.ListRouteRulesForNode("n1")
	if len(rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(rules))
	}
	if rules[0].MatchType != "domain_suffix" || rules[0].Action != "direct" || rules[0].Priority != 10 {
		t.Errorf("rule = %+v", rules[0])
	}

	// Garbage preset is rejected (fail-fast validation).
	w = ts.post("/ui/nodes/n1/routing", url.Values{
		"match_type": {"preset"}, "preset_id": {"no-such-preset"}, "action": {"direct"},
	})
	ts.assertStatus(w, http.StatusBadRequest)

	// Invalid geo name is rejected.
	w = ts.post("/ui/nodes/n1/routing", url.Values{
		"match_type": {"geosite"}, "match_values": {"bad name!"}, "action": {"direct"},
	})
	ts.assertStatus(w, http.StatusBadRequest)

	// Real preset creates a rule.
	w = ts.post("/ui/nodes/n1/routing", url.Values{
		"match_type": {"preset"}, "preset_id": {"telegram"}, "action": {"direct"},
	})
	ts.assertStatus(w, http.StatusOK)
	rules, _ = st.ListRouteRulesForNode("n1")
	if len(rules) != 2 {
		t.Fatalf("want 2 rules, got %d", len(rules))
	}

	// User-scoped geoip rule against a real user.
	if err := st.SaveUser(&model.User{ID: "u1", Name: "alice", AWGAddress: "10.8.0.5/32"}); err != nil {
		t.Fatal(err)
	}
	w = ts.post("/ui/nodes/n1/routing", url.Values{
		"match_type": {"geoip"}, "match_values": {"ru"}, "action": {"reject"}, "user_ids": {"u1"},
	})
	ts.assertStatus(w, http.StatusOK)
	rules, _ = st.ListRouteRulesForNode("n1")
	if len(rules) != 3 {
		t.Fatalf("want 3 rules, got %d", len(rules))
	}

	// Toggle the first rule.
	w = ts.post("/ui/nodes/n1/routing/"+rules[0].ID+"/toggle", url.Values{})
	ts.assertStatus(w, http.StatusOK)
	rules2, _ := st.ListRouteRulesForNode("n1")
	enabledBefore := map[string]bool{}
	for _, r := range rules {
		enabledBefore[r.ID] = r.Enabled
	}
	flipped := 0
	for _, r := range rules2 {
		if r.Enabled != enabledBefore[r.ID] {
			flipped++
		}
	}
	if flipped != 1 {
		t.Errorf("toggle must flip exactly one rule, flipped %d", flipped)
	}

	// Panel renders with the rules.
	w = ts.get("/ui/nodes/n1/routing")
	ts.assertStatus(w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "example.com") {
		t.Errorf("panel must list the rule values")
	}

	// Delete all rules.
	for _, r := range rules2 {
		w = ts.post("/ui/nodes/n1/routing/"+r.ID+"/delete", url.Values{})
		ts.assertStatus(w, http.StatusOK)
	}
	rules, _ = st.ListRouteRulesForNode("n1")
	if len(rules) != 0 {
		t.Fatalf("want 0 rules after delete, got %d", len(rules))
	}
}

func TestHandler_RouteRule_UnknownNode(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/nodes/ghost/routing")
	ts.assertStatus(w, http.StatusNotFound)
}
