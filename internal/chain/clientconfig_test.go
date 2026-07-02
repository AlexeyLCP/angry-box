package chain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// baseChainForClient builds a chain with the given node IDs as separate hosts
// for client-config rendering tests. All nodes share a TUIC user protocol so
// RenderClientConfig exercises the TUIC branch.
func baseChainForClient(t *testing.T, name string, nodeIDs []string, strategy model.Strategy, roles ...model.ChainNodeRole) *model.Chain {
	t.Helper()
	c := &model.Chain{
		Name:         name,
		Strategy:     strategy,
		UserProtocol: model.UserProtocolTUIC,
		Transport:    model.TransportXHTTP,
	}
	for i, id := range nodeIDs {
		n := model.ChainNode{
			ID:   id,
			Addr: id + ".example.test:22",
			User: "lcp",
		}
		if i < len(roles) {
			n.Role = roles[i]
		}
		c.Nodes = append(c.Nodes, n)
	}
	return c
}

// assertJSONContains checks the rendered client config carries the substrings.
func assertJSONContains(t *testing.T, cfg string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(cfg, s) {
			t.Errorf("client config missing %q\n--- config ---\n%s", s, cfg)
		}
	}
}

func TestRenderClientConfig_SingleEntry_LegacyTuicOut(t *testing.T) {
	// Single-entry chain (legacy: no explicit role, index 0 is entry) must use
	// the plain "tuic-out" tag with NO strategy wrapper, so existing e2e helpers
	// and configs keep working.
	c := baseChainForClient(t, "single", []string{"entry-1"}, model.StrategyURLTest)
	cfg, err := RenderClientConfig(ClientConfigParams{Chain: c})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	assertJSONContains(t, cfg,
		`"tag": "tuic-out"`,
		`"final": "tuic-out"`,
		`"server": "entry-1.example.test"`)
	if strings.Contains(cfg, "chain-lb") {
		t.Errorf("single-entry config must not have a strategy wrapper\n%s", cfg)
	}
	if strings.Contains(cfg, `"type": "urltest"`) || strings.Contains(cfg, `"type": "selector"`) {
		t.Errorf("single-entry config must not embed a strategy outbound\n%s", cfg)
	}
}

func TestRenderClientConfig_MultiEntry_URLTestWrapper(t *testing.T) {
	// Two explicit entry nodes -> two tuic-out-<id> outbounds + a urltest
	// "chain-lb" wrapper; route/DNS detour through "chain-lb".
	c := baseChainForClient(t, "multi", []string{"entry-a", "entry-b"},
		model.StrategyURLTest,
		model.NodeRoleEntry, model.NodeRoleEntry)
	cfg, err := RenderClientConfig(ClientConfigParams{Chain: c})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	assertJSONContains(t, cfg,
		`"tag": "tuic-out-entry-a"`,
		`"tag": "tuic-out-entry-b"`,
		`"type": "urltest"`,
		`"tag": "chain-lb"`,
		`"final": "chain-lb"`)
	if strings.Contains(cfg, `"tag": "tuic-out"`) {
		t.Errorf("multi-entry config must not use the legacy plain tuic-out tag\n%s", cfg)
	}
	// The urltest outbound must reference both per-entry tags as its outbounds.
	var raw struct {
		Outbounds []struct {
			Type      string   `json:"type"`
			Tag       string   `json:"tag"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(cfg), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var lb *struct {
		Type      string   `json:"type"`
		Tag       string   `json:"tag"`
		Outbounds []string `json:"outbounds"`
	}
	for i := range raw.Outbounds {
		if raw.Outbounds[i].Tag == "chain-lb" {
			lb = &raw.Outbounds[i]
			break
		}
	}
	if lb == nil {
		t.Fatalf("no chain-lb outbound in config\n%s", cfg)
	}
	if lb.Type != "urltest" {
		t.Errorf("chain-lb type = %q, want urltest", lb.Type)
	}
	want := map[string]bool{"tuic-out-entry-a": false, "tuic-out-entry-b": false}
	for _, o := range lb.Outbounds {
		if _, ok := want[o]; ok {
			want[o] = true
		}
	}
	for tag, found := range want {
		if !found {
			t.Errorf("chain-lb outbounds missing %q (got %v)", tag, lb.Outbounds)
		}
	}
}

func TestRenderClientConfig_MultiEntry_SelectorWrapper(t *testing.T) {
	c := baseChainForClient(t, "sel", []string{"entry-a", "entry-b"},
		model.StrategySelector,
		model.NodeRoleEntry, model.NodeRoleEntry)
	cfg, err := RenderClientConfig(ClientConfigParams{Chain: c})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	assertJSONContains(t, cfg, `"type": "selector"`, `"tag": "chain-lb"`, `"default": "tuic-out-entry-a"`)
}

func TestRenderClientConfig_MultiEntry_UnknownStrategy_FallsBackToURLTest(t *testing.T) {
	// An unset or unrecognized strategy for a multi-entry chain must still
	// produce a usable urltest wrapper (revives the previously-dead
	// BuildStrategyOutbound and the previously-ignored Chain.Strategy).
	c := baseChainForClient(t, "fallback", []string{"entry-a", "entry-b"},
		model.StrategyBond, // bond -> BuildStrategyOutbound returns nil -> fall back to urltest
		model.NodeRoleEntry, model.NodeRoleEntry)
	cfg, err := RenderClientConfig(ClientConfigParams{Chain: c})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	assertJSONContains(t, cfg, `"type": "urltest"`, `"tag": "chain-lb"`)
}

func TestRenderClientConfig_EntryHostOverride_AppliesToAllEntries(t *testing.T) {
	// EntryHostOverride (used to run the client on the entry VPS, loopback)
	// must apply to every per-entry outbound so multi-entry works locally too.
	c := baseChainForClient(t, "override", []string{"entry-a", "entry-b"},
		model.StrategyURLTest,
		model.NodeRoleEntry, model.NodeRoleEntry)
	cfg, err := RenderClientConfig(ClientConfigParams{Chain: c, EntryHostOverride: "127.0.0.1"})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	// Both entries must point at 127.0.0.1; neither at the .example.test host.
	if strings.Contains(cfg, "entry-a.example.test") || strings.Contains(cfg, "entry-b.example.test") {
		t.Errorf("EntryHostOverride not applied to all entries\n%s", cfg)
	}
	assertJSONContains(t, cfg, `"server": "127.0.0.1"`)
}

func TestResolveChainRoles_MultiEntry(t *testing.T) {
	// Two nodes flagged entry -> both become entry roles for their nodeID,
	// independent of index. The node that is neither flagged nor index 0 is
	// transit. Verifies the A2 change (resolveChainRoles honors Role).
	c := &model.Chain{
		Name:         "r",
		UserProtocol: model.UserProtocolTUIC,
		Nodes: []model.ChainNode{
			{ID: "e1", Addr: "e1.example.test:22", Role: model.NodeRoleEntry},
			{ID: "e2", Addr: "e2.example.test:22", Role: model.NodeRoleEntry},
			{ID: "t1", Addr: "t1.example.test:22"}, // transit (empty role, index > 0)
		},
	}
	roles := resolveChainRoles("e2", []*model.Chain{c})
	if len(roles) != 1 {
		t.Fatalf("want 1 role for e2, got %d", len(roles))
	}
	if !roles[0].IsEntry {
		t.Errorf("e2 (Role=entry at index 1) must be IsEntry=true")
	}
	if roles[0].IsTransit {
		t.Errorf("e2 must not be transit")
	}
	if !roles[0].HasOutbound {
		t.Errorf("e2 (index 1, not last) must have an outbound to t1")
	}

	rolesT := resolveChainRoles("t1", []*model.Chain{c})
	if len(rolesT) != 1 {
		t.Fatalf("want 1 role for t1, got %d", len(rolesT))
	}
	if rolesT[0].IsEntry {
		t.Errorf("t1 (no role, index 2) must not be entry")
	}
	if !rolesT[0].IsTransit {
		t.Errorf("t1 must be transit")
	}
	if rolesT[0].HasOutbound {
		t.Errorf("t1 (last node) must not have an outbound")
	}
}

func TestResolveChainRoles_BackwardCompat_Index0IsEntry(t *testing.T) {
	// No explicit roles -> legacy behavior: index 0 is entry, rest transit.
	c := &model.Chain{
		Name:         "legacy",
		UserProtocol: model.UserProtocolTUIC,
		Nodes: []model.ChainNode{
			{ID: "n0", Addr: "n0.example.test:22"},
			{ID: "n1", Addr: "n1.example.test:22"},
		},
	}
	r0 := resolveChainRoles("n0", []*model.Chain{c})
	if !r0[0].IsEntry || r0[0].IsTransit {
		t.Errorf("n0 (index 0, no role) must be entry-only, got entry=%v transit=%v", r0[0].IsEntry, r0[0].IsTransit)
	}
	r1 := resolveChainRoles("n1", []*model.Chain{c})
	if r1[0].IsEntry || !r1[0].IsTransit {
		t.Errorf("n1 (index 1, no role) must be transit-only, got entry=%v transit=%v", r1[0].IsEntry, r1[0].IsTransit)
	}
}

func TestChainEntryPort_SingleVsMultiEntry(t *testing.T) {
	// Single entry (legacy or one explicit Role=entry): base port, no offset.
	c1 := &model.Chain{Name: "s", UserEntryPort: 443, Nodes: []model.ChainNode{{ID: "e0"}}}
	if got := chainEntryPort(c1, "e0"); got != 443 {
		t.Errorf("single entry port = %d, want 443", got)
	}
	// Two explicit entries: first = base, second = base+1.
	c2 := &model.Chain{
		Name:         "m",
		UserEntryPort: 443,
		Nodes: []model.ChainNode{
			{ID: "a", Role: model.NodeRoleEntry},
			{ID: "b", Role: model.NodeRoleEntry},
		},
	}
	if got := chainEntryPort(c2, "a"); got != 443 {
		t.Errorf("multi entry[0] port = %d, want 443", got)
	}
	if got := chainEntryPort(c2, "b"); got != 444 {
		t.Errorf("multi entry[1] port = %d, want 444", got)
	}
}

func TestChainUserInboundTag_SingleVsMultiEntry(t *testing.T) {
	// Single entry -> legacy tag without node suffix.
	c1 := &model.Chain{Name: "s", Nodes: []model.ChainNode{{ID: "e0"}}}
	if got := chainUserInboundTag(c1, "e0"); got != "ch-s-user-in" {
		t.Errorf("single entry tag = %q, want ch-s-user-in", got)
	}
	// Multi-entry -> suffixed tags.
	c2 := &model.Chain{
		Name:  "m",
		Nodes: []model.ChainNode{{ID: "a", Role: model.NodeRoleEntry}, {ID: "b", Role: model.NodeRoleEntry}},
	}
	if got := chainUserInboundTag(c2, "a"); got != "ch-m-user-in-a" {
		t.Errorf("multi entry[a] tag = %q, want ch-m-user-in-a", got)
	}
	if got := chainUserInboundTag(c2, "b"); got != "ch-m-user-in-b" {
		t.Errorf("multi entry[b] tag = %q, want ch-m-user-in-b", got)
	}
}