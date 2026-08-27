package chain

// routing_test.go — manual (operator) route-rule engine tests (LucX routing
// slice 1, 2026-08-27): expansion of RouteRule into sing-box route rules +
// local rule_set assets, per-user scoping, inbound-scope computation, and a
// real `sing-box check` of a geo rule with a local .srs file.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func rr(matchType, matchValues, action string, prio int, enabled bool) *model.RouteRule {
	return &model.RouteRule{
		ID: "rr-" + matchType, NodeID: "n1", Priority: prio,
		MatchType: matchType, MatchValues: matchValues,
		Action: action, Enabled: enabled,
	}
}

func TestSplitMatchValues(t *testing.T) {
	got := splitMatchValues("a.com, b.com\nc.com;;\n")
	want := []string{"a.com", "b.com", "c.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if len(splitMatchValues("  ")) != 0 {
		t.Error("blank input must split to nothing")
	}
}

func TestRuleSetTagValid(t *testing.T) {
	for _, ok := range []string{"telegram", "category-ads-all", "geoip-ru", "a_b.c-1"} {
		if !ruleSetTagValid(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "a b", "x/y", "a'b", "$(rm)", strings.Repeat("x", 65)} {
		if ruleSetTagValid(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestExpandManualRouteRules_PresetDomains(t *testing.T) {
	scope := []string{"tun-in", "sa-0-naive"}
	ex := ExpandManualRouteRules([]*model.RouteRule{rr("preset", "telegram", "direct", 10, true)}, scope, nil)
	if len(ex.Rules) != 1 {
		t.Fatalf("want 1 rule, got %d (%v)", len(ex.Rules), ex.Warnings)
	}
	r := ex.Rules[0]
	if r.Action != "direct" {
		t.Errorf("action = %q, want direct", r.Action)
	}
	if len(r.DomainSuffix) == 0 || r.DomainSuffix[0] != "t.me" {
		t.Errorf("DomainSuffix = %v, want telegram domains", r.DomainSuffix)
	}
	if strings.Join(r.Inbound, ",") != strings.Join(scope, ",") {
		t.Errorf("Inbound = %v, want scope %v", r.Inbound, scope)
	}
	if len(ex.RuleSets) != 0 {
		t.Errorf("domain preset must not need rule_set, got %v", ex.RuleSets)
	}
}

func TestExpandManualRouteRules_AdsPresetReject(t *testing.T) {
	r := rr("preset", "ads", "", 10, true)
	ex := ExpandManualRouteRules([]*model.RouteRule{r}, []string{"tun-in"}, nil)
	if len(ex.Rules) != 1 {
		t.Fatalf("want 1 rule, got %d (%v)", len(ex.Rules), ex.Warnings)
	}
	if ex.Rules[0].Action != "reject" {
		t.Errorf("ads preset must reject, got action %q", ex.Rules[0].Action)
	}
	if len(ex.Rules[0].RuleSet) != 1 || ex.Rules[0].RuleSet[0] != "category-ads-all" {
		t.Errorf("RuleSet = %v, want [category-ads-all]", ex.Rules[0].RuleSet)
	}
	if len(ex.RuleSets) != 1 {
		t.Fatalf("want 1 rule_set entry, got %v", ex.RuleSets)
	}
	rs := ex.RuleSets[0]
	if rs.Type != "local" || rs.Format != "binary" || rs.Path != NodeRuleSetDir+"/category-ads-all.srs" {
		t.Errorf("rule_set entry = %+v", rs)
	}
	if len(ex.Assets) != 1 || ex.Assets[0].Kind != "geosite" || ex.Assets[0].Name != "category-ads-all" {
		t.Errorf("assets = %+v", ex.Assets)
	}
}

func TestExpandManualRouteRules_Geo(t *testing.T) {
	rules := []*model.RouteRule{
		{ID: "g1", NodeID: "n1", Priority: 1, MatchType: "geosite", MatchValues: "telegram", Action: "route", OutboundTag: "direct-out", Enabled: true},
		{ID: "g2", NodeID: "n1", Priority: 2, MatchType: "geoip", MatchValues: "ru, ir", Action: "direct", Enabled: true},
	}
	ex := ExpandManualRouteRules(rules, []string{"tun-in"}, nil)
	if len(ex.Rules) != 2 {
		t.Fatalf("want 2 rules, got %d (%v)", len(ex.Rules), ex.Warnings)
	}
	if ex.Rules[0].RuleSet[0] != "telegram" {
		t.Errorf("geosite tag = %v", ex.Rules[0].RuleSet)
	}
	got := ex.Rules[1].RuleSet
	if len(got) != 2 || got[0] != "geoip-ru" || got[1] != "geoip-ir" {
		t.Errorf("geoip tags = %v", got)
	}
	// geosite telegram appears in BOTH rules? no — g1 telegram, g2 ru+ir → 3 assets.
	if len(ex.RuleSets) != 3 {
		t.Errorf("want 3 deduped rule_set entries, got %d", len(ex.RuleSets))
	}
}

func TestExpandManualRouteRules_UserScoped(t *testing.T) {
	users := []model.User{
		{ID: "u1", AWGAddress: "10.8.0.5/32", VLESSUUID: "uuid-alice"},
		{ID: "u2", AWGAddress: "10.8.0.6/32"},
		{ID: "u3"}, // no identity at all
	}
	r := rr("preset", "telegram", "direct", 10, true)
	r.UserIDs = []string{"u1", "u2", "u3", "ghost"}
	ex := ExpandManualRouteRules([]*model.RouteRule{r}, []string{"tun-in"}, users)
	// One source_ip_cidr rule (u1+u2) + one auth_user rule (u1 only).
	if len(ex.Rules) != 2 {
		t.Fatalf("want 2 rules, got %d: %+v (warnings %v)", len(ex.Rules), ex.Rules, ex.Warnings)
	}
	byIP, byAuth := ex.Rules[0], ex.Rules[1]
	if len(byIP.SourceIPCIDR) != 2 || byIP.SourceIPCIDR[0] != "10.8.0.5/32" || byIP.SourceIPCIDR[1] != "10.8.0.6/32" {
		t.Errorf("SourceIPCIDR = %v", byIP.SourceIPCIDR)
	}
	if len(byAuth.AuthUser) != 1 || byAuth.AuthUser[0] != "uuid-alice" {
		t.Errorf("AuthUser = %v", byAuth.AuthUser)
	}
	if len(ex.Warnings) == 0 {
		t.Error("want warnings for u3 (no identity) and ghost (unknown)")
	}
}

func TestExpandManualRouteRules_ScopedNoIdentity(t *testing.T) {
	users := []model.User{{ID: "u1"}}
	r := rr("domain", "example.com", "direct", 10, true)
	r.UserIDs = []string{"u1"}
	ex := ExpandManualRouteRules([]*model.RouteRule{r}, []string{"tun-in"}, users)
	if len(ex.Rules) != 0 {
		t.Fatalf("want 0 rules for identity-less user, got %d", len(ex.Rules))
	}
	if len(ex.Warnings) == 0 {
		t.Error("want a skip warning")
	}
}

func TestExpandManualRouteRules_ActionsAndDisabled(t *testing.T) {
	rules := []*model.RouteRule{
		func() *model.RouteRule { r := rr("domain", "a.com", "block", 1, true); return r }(),
		func() *model.RouteRule { r := rr("domain", "b.com", "route", 2, true); r.OutboundTag = "my-out"; return r }(),
		rr("domain", "c.com", "direct", 3, false), // disabled
	}
	ex := ExpandManualRouteRules(rules, []string{"tun-in"}, nil)
	if len(ex.Rules) != 2 {
		t.Fatalf("want 2 rules (disabled dropped), got %d", len(ex.Rules))
	}
	if ex.Rules[0].Action != "reject" {
		t.Errorf("block must map to reject, got %q", ex.Rules[0].Action)
	}
	if ex.Rules[1].Outbound != "my-out" || ex.Rules[1].Action != "" {
		t.Errorf("route rule = %+v", ex.Rules[1])
	}
}

func TestExpandManualRouteRules_BadInputs(t *testing.T) {
	cases := []*model.RouteRule{
		rr("domain", "", "direct", 1, true),           // empty values
		rr("preset", "no-such-preset", "", 2, true),   // unknown preset
		rr("geosite", "a b", "", 3, true),             // invalid tag chars
		rr("wat", "x", "", 4, true),                   // unknown match type
	}
	ex := ExpandManualRouteRules(cases, []string{"tun-in"}, nil)
	if len(ex.Rules) != 0 {
		t.Errorf("want 0 rules, got %d", len(ex.Rules))
	}
	if len(ex.Warnings) != len(cases) {
		t.Errorf("want %d warnings, got %v", len(cases), ex.Warnings)
	}
}

func TestManualRuleInboundScope(t *testing.T) {
	nodeInfo := &model.NodeInfo{
		Host: model.Host{ID: "n1"},
		Inbounds: []model.NodeInbound{
			{Protocol: "naive", Port: 443},                          // sa-0-naive
			{Protocol: "awg", Port: 51820, Source: "chain:c1"},    // chain-sourced → skip
			{Protocol: "mieru", Port: 8964, Tag: "custom-tag"},    // custom tag
		},
	}
	chain1 := &model.Chain{Name: "c1", UserProtocol: model.UserProtocolVLESSReality}
	chain1.Nodes = []model.ChainNode{{ID: "n1"}, {ID: "n2"}}
	roles := resolveChainRoles("n1", []*model.Chain{chain1})

	scope := manualRuleInboundScope(roles, nodeInfo, []*model.Chain{chain1}, true)
	joined := strings.Join(scope, ",")
	for _, want := range []string{"tun-in", "sa-0-naive", "custom-tag"} {
		if !strings.Contains(joined, want) {
			t.Errorf("scope %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "sa-1-awg") {
		t.Errorf("chain-sourced inbound must not enter scope: %q", joined)
	}
	if !strings.Contains(joined, chainUserInboundTag(chain1, "n1")) {
		t.Errorf("scope %q missing chain user-in tag", joined)
	}
}

// TestBuildMergedNodeConfig_ManualRouteSection verifies a node with manual
// route rules gets a route section even without AB_ROUTE_DNS: manual rules
// first (after the injected sniff action rule), local rule_set entries
// attached, no dns-server tag referenced.
func TestBuildMergedNodeConfig_ManualRouteSection(t *testing.T) {
	nodeInfo := &model.NodeInfo{
		Host:     model.Host{ID: "n1", Addr: "192.0.2.10:22"},
		Inbounds: []model.NodeInbound{{Protocol: "naive", Port: 443, UUID: "u"}},
	}
	rules := []*model.RouteRule{
		func() *model.RouteRule {
			r := rr("geosite", "telegram", "direct", 10, true)
			r.ID = "m1"
			return r
		}(),
	}
	cfg, _, err := buildMergedNodeConfig(MergedNodeConfigParams{
		NodeInfo:   nodeInfo,
		RouteRules: rules,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cfg.Route == nil {
		t.Fatal("route section must be emitted for manual rules")
	}
	if cfg.Route.DefaultDomainResolver != "" {
		t.Errorf("no DNS section → default_domain_resolver must stay empty, got %q", cfg.Route.DefaultDomainResolver)
	}
	// Rule order: sniff action rule first, then the manual rule.
	if len(cfg.Route.Rules) < 2 {
		t.Fatalf("want sniff + manual rule, got %d rules", len(cfg.Route.Rules))
	}
	if cfg.Route.Rules[0].Action != "sniff" {
		t.Errorf("first rule must be sniff, got %+v", cfg.Route.Rules[0])
	}
	manual := cfg.Route.Rules[1]
	if len(manual.RuleSet) != 1 || manual.RuleSet[0] != "telegram" || manual.Action != "direct" {
		t.Errorf("manual rule = %+v", manual)
	}
	if len(cfg.Route.RuleSet) != 1 || cfg.Route.RuleSet[0].Type != "local" {
		t.Fatalf("rule_set = %+v", cfg.Route.RuleSet)
	}
	// The standalone cascade rule comes AFTER the manual rule.
	last := cfg.Route.Rules[len(cfg.Route.Rules)-1]
	if len(last.Inbound) != 1 || last.Inbound[0] != "sa-0-naive" {
		t.Errorf("last rule must be the standalone cascade, got %+v", last)
	}
}

// TestSingboxCheck_LocalGeoRuleSet runs the real `sing-box check` against a
// config with a local rule_set + geo rule (the deploy shape). The .srs asset
// is downloaded once into the test temp dir — skipped when the orchestrator
// host has no network (the shape is also covered by the n1 stand).
func TestSingboxCheck_LocalGeoRuleSet(t *testing.T) {
	bin := findSingBoxBinary()
	if bin == "" || !singBoxSupportsAWG(bin) {
		t.Skip("no amnezia-box sing-box binary available")
	}
	asset, err := FetchRuleSetAsset(ruleSetKindGeoSite, "telegram")
	if err != nil {
		t.Skipf("no network for geo asset (shape covered on n1): %v", err)
	}
	// Copy into a temp path so the test never depends on cache-dir writability
	// at sing-box runtime.
	raw, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "telegram.srs")
	if err := os.WriteFile(local, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{map[string]any{
			"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 18080,
		}},
		"route": map[string]any{
			"rules": []any{
				map[string]any{"action": "sniff"},
				map[string]any{"inbound": []string{"mixed-in"}, "rule_set": []string{"telegram"}, "action": "direct"},
			},
			"rule_set": []any{map[string]any{
				"tag": "telegram", "type": "local", "format": "binary", "path": local,
			}},
			"final": "direct",
		},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmp, cfgJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "check", "-c", tmp).CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box check failed: %v\nconfig: %s\noutput: %s", err, cfgJSON, out)
	}
	fmt.Println("local rule_set check OK")
}
