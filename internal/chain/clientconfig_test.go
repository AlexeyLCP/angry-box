package chain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
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
// TestChainTUICUsers_MultiUser verifies that assigned users with per-user TUIC
// creds produce one TUICUser each (multi-user inbound), ordered deterministically
// by the input slice, and that expired/inactive users are skipped.
func TestChainTUICUsers_MultiUser(t *testing.T) {
	c := &model.Chain{
		Name:             "mc",
		UserProtocol:     model.UserProtocolTUIC,
		TUICEntryUserUUID: "chain-uuid",
		TUICEntryUserPassword: "chain-pass",
	}
	users := []model.User{
		{ID: "u1", Name: "alice", Active: true, TUICUUID: "alice-uuid", TUICPassword: "alice-pass"},
		{ID: "u2", Name: "bob", Active: true, TUICUUID: "bob-uuid", TUICPassword: "bob-pass"},
		{ID: "u3", Name: "expired", Active: true, ExpiresAt: time.Now().Add(-time.Hour), TUICUUID: "x-uuid", TUICPassword: "x-pass"},
		{ID: "u4", Name: "inactive", Active: false, TUICUUID: "i-uuid", TUICPassword: "i-pass"},
		{ID: "u5", Name: "nofallback", Active: true}, // no per-user creds -> uses chain-wide
	}
	got := chainTUICUsers(c, users)
	if len(got) != 3 {
		t.Fatalf("want 3 active non-expired users, got %d (%+v)", len(got), got)
	}
	wantUUIDs := map[string]bool{"alice-uuid": false, "bob-uuid": false, "chain-uuid": false}
	for _, u := range got {
		if _, ok := wantUUIDs[u.UUID]; ok {
			wantUUIDs[u.UUID] = true
		}
	}
	for uuid, found := range wantUUIDs {
		if !found {
			t.Errorf("missing TUIC user with uuid %q in %v", uuid, got)
		}
	}
}

func TestChainTUICUsers_NoUsers_FallsBackToChainCreds(t *testing.T) {
	c := &model.Chain{
		Name:                  "sc",
		UserProtocol:          model.UserProtocolTUIC,
		TUICEntryUserUUID:     "chain-uuid",
		TUICEntryUserPassword: "chain-pass",
	}
	got := chainTUICUsers(c, nil)
	if len(got) != 1 {
		t.Fatalf("want single fallback user, got %d", len(got))
	}
	if got[0].UUID != "chain-uuid" || got[0].Password != "chain-pass" {
		t.Errorf("fallback user = %+v, want chain-wide creds", got[0])
	}
}

func TestBuildTUICInboundWithUsers_EmitsUsersArray(t *testing.T) {
	preset := GetDefaultPreset()
	hp := &hopParams{PrivateKey: "", ShortID: "deadbeef", ServerName: "www.cloudflare.com", Port: 443}
	users := []config.TUICUser{
		{UUID: "u1", Password: "p1"},
		{UUID: "u2", Password: "p2"},
	}
	raw := buildTUICInboundWithUsers(443, users, "in-tag", &preset, hp)
	var inb config.TUICInbound
	if err := json.Unmarshal(raw, &inb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(inb.Users) != 2 {
		t.Fatalf("want 2 users in inbound, got %d", len(inb.Users))
	}
	if inb.Users[0].UUID != "u1" || inb.Users[1].UUID != "u2" {
		t.Errorf("users order/uuids wrong: %+v", inb.Users)
	}
}

// TestBuildMergedRoute_PerClientChainExit verifies that a user pinned via
// ChainExit to the entry node (egress here) gets a direct-out auth_user rule,
// and a user pinned to the next hop gets the inter-node outbound auth_user rule,
// both emitted BEFORE the generic inbound->outbound fallback rule.
func TestBuildMergedRoute_PerClientChainExit(t *testing.T) {
	// Chain: entry -> middle -> exit. Entry node is the one we render for.
	c := &model.Chain{
		Name:         "pc",
		UserProtocol: model.UserProtocolTUIC,
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "entry.example.test:22"},
			{ID: "middle", Addr: "middle.example.test:22"},
			{ID: "exit", Addr: "exit.example.test:22"},
		},
	}
	roles := resolveChainRoles("entry", []*model.Chain{c})
	if len(roles) != 1 {
		t.Fatalf("want 1 role, got %d", len(roles))
	}
	// TUIC auth_user matches by UUID (TUICUser has no Name). Carol has no
	// TUICUUID -> userAuthIdentity returns "" -> skipped (no auth_user rule).
	usersByChain := map[string][]model.User{
		"pc": {
			{Name: "alice", Active: true, TUICUUID: "alice-uuid", ChainExit: map[string]string{"pc": "entry"}}, // egress at entry
			{Name: "bob", Active: true, TUICUUID: "bob-uuid", ChainExit: map[string]string{"pc": "middle"}},    // egress one hop down
			{Name: "carol", Active: true, TUICUUID: "carol-uuid"},                                                  // no pin -> default route
			{Name: "dave", Active: true, ChainExit: map[string]string{"pc": "entry"}},                              // no TUICUUID -> skipped
		},
	}
	rt := buildMergedRoute(roles, &model.NodeInfo{Host: model.Host{ID: "entry"}}, usersByChain)
	if rt == nil {
		t.Fatal("nil route")
	}
	var aliceRule, bobRule *config.RouteRuleEntry
	for i := range rt.Rules {
		r := &rt.Rules[i]
		if len(r.AuthUser) == 1 && r.AuthUser[0] == "alice-uuid" {
			aliceRule = r
		}
		if len(r.AuthUser) == 1 && r.AuthUser[0] == "bob-uuid" {
			bobRule = r
		}
	}
	if aliceRule == nil {
		t.Fatal("no auth_user rule for alice (pinned to entry)")
	}
	if aliceRule.Outbound != "direct-out" {
		t.Errorf("alice (exit=entry) outbound=%s, want direct-out", aliceRule.Outbound)
	}
	if bobRule == nil {
		t.Fatal("no auth_user rule for bob (pinned to middle)")
	}
	wantOut := chainInterNodeOutboundTag(&roles[0])
	if bobRule.Outbound != wantOut {
		t.Errorf("bob (exit=middle) outbound=%s, want %s", bobRule.Outbound, wantOut)
	}
	// carol (no pin) and dave (no TUICUUID) must not get auth_user rules.
	for _, r := range rt.Rules {
		if len(r.AuthUser) == 1 && (r.AuthUser[0] == "carol-uuid" || r.AuthUser[0] == "dave") {
			t.Errorf("carol (no pin) / dave (no TUICUUID) must not get an auth_user rule, got %v", r.AuthUser)
		}
	}
	// auth_user rules must precede the generic entry inbound rule.
	aliceIdx, entryIdx := -1, -1
	for i, r := range rt.Rules {
		if len(r.AuthUser) == 1 && r.AuthUser[0] == "alice-uuid" {
			aliceIdx = i
		}
		if len(r.Inbound) == 1 && r.Inbound[0] == "ch-pc-user-in" {
			entryIdx = i
		}
	}
	if aliceIdx >= 0 && entryIdx >= 0 && aliceIdx > entryIdx {
		t.Errorf("alice auth_user rule (idx %d) must precede generic entry rule (idx %d)", aliceIdx, entryIdx)
	}
}

func TestBuildMergedRoute_NoUsers_NoAuthUserRules(t *testing.T) {
	// Without usersByChain, no auth_user rules are emitted (legacy behavior).
	c := &model.Chain{
		Name:         "nu",
		UserProtocol: model.UserProtocolTUIC,
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "entry.example.test:22"},
			{ID: "exit", Addr: "exit.example.test:22"},
		},
	}
	roles := resolveChainRoles("entry", []*model.Chain{c})
	rt := buildMergedRoute(roles, &model.NodeInfo{Host: model.Host{ID: "entry"}}, nil)
	for _, r := range rt.Rules {
		if len(r.AuthUser) > 0 {
			t.Errorf("no users should yield no auth_user rules, got %+v", r)
		}
	}
}

// TestBuildMergedRoute_PerClientAWG_SourceIP verifies AWG per-client routing:
// users are distinguished by their tunnel IP (AWGAddress), and route rules use
// source_ip_cidr (not auth_user). A user pinned to the entry egresses here
// (direct-out); a user pinned to the next hop forwards down the chain. A user
// with no AWGAddress is skipped (cannot be matched by source IP). Rules precede
// the generic inbound->outbound fallback.
func TestBuildMergedRoute_PerClientAWG_SourceIP(t *testing.T) {
	// Chain: entry -> middle -> exit. Render for the entry node.
	c := &model.Chain{
		Name:         "awgpc",
		UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "entry.example.test:22"},
			{ID: "middle", Addr: "middle.example.test:22"},
			{ID: "exit", Addr: "exit.example.test:22"},
		},
	}
	roles := resolveChainRoles("entry", []*model.Chain{c})
	if len(roles) != 1 {
		t.Fatalf("want 1 role, got %d", len(roles))
	}
	usersByChain := map[string][]model.User{
		"awgpc": {
			{Name: "alice", Active: true, AWGAddress: "10.8.0.2/32", ChainExit: map[string]string{"awgpc": "entry"}},  // egress at entry
			{Name: "bob", Active: true, AWGAddress: "10.8.0.3/32", ChainExit: map[string]string{"awgpc": "middle"}}, // egress one hop down
			{Name: "carol", Active: true, AWGAddress: "10.8.0.4/32"},                                                 // no pin -> default route
			{Name: "dave", Active: true, ChainExit: map[string]string{"awgpc": "entry"}},                              // no AWGAddress -> skipped
		},
	}
	rt := buildMergedRoute(roles, &model.NodeInfo{Host: model.Host{ID: "entry"}}, usersByChain)
	if rt == nil {
		t.Fatal("nil route")
	}
	var aliceRule, bobRule *config.RouteRuleEntry
	for i := range rt.Rules {
		r := &rt.Rules[i]
		if len(r.SourceIPCIDR) == 1 && r.SourceIPCIDR[0] == "10.8.0.2/32" {
			aliceRule = r
		}
		if len(r.SourceIPCIDR) == 1 && r.SourceIPCIDR[0] == "10.8.0.3/32" {
			bobRule = r
		}
	}
	if aliceRule == nil {
		t.Fatal("no source_ip_cidr rule for alice (pinned to entry)")
	}
	if aliceRule.Outbound != "direct-out" {
		t.Errorf("alice (exit=entry) outbound=%s, want direct-out", aliceRule.Outbound)
	}
	if len(aliceRule.AuthUser) != 0 {
		t.Errorf("alice rule must use source_ip_cidr, not auth_user, got %v", aliceRule.AuthUser)
	}
	if bobRule == nil {
		t.Fatal("no source_ip_cidr rule for bob (pinned to middle)")
	}
	wantOut := chainInterNodeOutboundTag(&roles[0])
	if bobRule.Outbound != wantOut {
		t.Errorf("bob (exit=middle) outbound=%s, want %s", bobRule.Outbound, wantOut)
	}
	// carol (no pin) and dave (no AWGAddress) must not get per-client rules.
	for _, r := range rt.Rules {
		if len(r.SourceIPCIDR) == 1 && r.SourceIPCIDR[0] == "10.8.0.4/32" {
			t.Errorf("carol (no pin) must not get a source_ip_cidr rule, got %v", r.SourceIPCIDR)
		}
	}
	// Per-client rules must precede the generic entry inbound rule.
	aliceIdx, entryIdx := -1, -1
	for i, r := range rt.Rules {
		if len(r.SourceIPCIDR) == 1 && r.SourceIPCIDR[0] == "10.8.0.2/32" {
			aliceIdx = i
		}
		if len(r.Inbound) == 1 && r.Inbound[0] == "ch-awgpc-user-in" {
			entryIdx = i
		}
	}
	if aliceIdx >= 0 && entryIdx >= 0 && aliceIdx > entryIdx {
		t.Errorf("alice source_ip_cidr rule (idx %d) must precede generic entry rule (idx %d)", aliceIdx, entryIdx)
	}
}

// TestBuildMergedRoute_PerClientAWG_MultiHopPin verifies that a pin to a node
// further than one hop down is expressible with AWG source-IP routing — the
// case that auth_user (TUIC/VLESS) cannot do. alice pins to exit (two hops down
// from entry): entry forwards down, middle forwards down, exit direct-outs.
// We render each node's roles and check the rule on each.
func TestBuildMergedRoute_PerClientAWG_MultiHopPin(t *testing.T) {
	c := &model.Chain{
		Name:         "mh",
		UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "entry.example.test:22"},
			{ID: "middle", Addr: "middle.example.test:22"},
			{ID: "exit", Addr: "exit.example.test:22"},
		},
	}
	alice := model.User{
		Name: "alice", Active: true, AWGAddress: "10.8.0.2/32",
		ChainExit: map[string]string{"mh": "exit"}, // pin two hops down
	}
	usersByChain := map[string][]model.User{"mh": {alice}}

	type want struct {
		nodeID    string
		outbound  string // "" means no per-client rule expected on this node
		inTagWant string
	}
	cases := []want{
		// Under the kernel-AWG architecture the AWG entry's user traffic arrives
		// via the TUN overlay inbound (tun-in), NOT the old userspace
		// ch-mh-user-in endpoint (which no longer exists). Middle/exit are transit
		// nodes — their per-client rules still key on the transport-in inbound.
		{"entry", "", "tun-in"},              // entry: has outbound, before exit -> inter-node out
		{"middle", "", "ch-mh-transport-in"}, // middle: has outbound, before exit -> inter-node out
		{"exit", "direct-out", "ch-mh-transport-in"},
	}
	for i, w := range cases {
		roles := resolveChainRoles(w.nodeID, []*model.Chain{c})
		rt := buildMergedRoute(roles, &model.NodeInfo{Host: model.Host{ID: w.nodeID}}, usersByChain)
		if rt == nil {
			t.Fatalf("node %s: nil route", w.nodeID)
		}
		var got *config.RouteRuleEntry
		for j := range rt.Rules {
			r := &rt.Rules[j]
			if len(r.SourceIPCIDR) == 1 && r.SourceIPCIDR[0] == "10.8.0.2/32" {
				got = r
				break
			}
		}
		switch w.outbound {
		case "":
			// expect an inter-node outbound rule (forward down the chain)
			if got == nil {
				t.Fatalf("node %s (case %d): no source_ip_cidr rule for alice", w.nodeID, i)
			}
			if got.Outbound == "direct-out" {
				t.Errorf("node %s (case %d): outbound=direct-out, want inter-node forward", w.nodeID, i)
			}
		case "direct-out":
			if got == nil {
				t.Fatalf("node %s (case %d): no source_ip_cidr rule for alice", w.nodeID, i)
			}
			if got.Outbound != "direct-out" {
				t.Errorf("node %s (case %d): outbound=%s, want direct-out", w.nodeID, i, got.Outbound)
			}
		}
		if got != nil {
			if len(got.Inbound) != 1 || got.Inbound[0] != w.inTagWant {
				t.Errorf("node %s (case %d): inbound=%v, want [%s]", w.nodeID, i, got.Inbound, w.inTagWant)
			}
		}
	}
}

// TestBuildMergedRoute_NoUsers_AWG_NoSourceIPRules — without users, no per-
// client source_ip_cidr rules are emitted for an AWG chain.
func TestBuildMergedRoute_NoUsers_AWG_NoSourceIPRules(t *testing.T) {
	c := &model.Chain{
		Name:         "awgnu",
		UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "entry.example.test:22"},
			{ID: "exit", Addr: "exit.example.test:22"},
		},
	}
	roles := resolveChainRoles("entry", []*model.Chain{c})
	rt := buildMergedRoute(roles, &model.NodeInfo{Host: model.Host{ID: "entry"}}, nil)
	for _, r := range rt.Rules {
		if len(r.SourceIPCIDR) > 0 {
			t.Errorf("no users should yield no source_ip_cidr rules, got %+v", r)
		}
	}
}

// TestBuildAWGUserInboundMulti_Peers verifies the multi-peer AWG endpoint: one
// peer per active user with AWGPublicKey+AWGAddress, skipped users omitted, and
// a placeholder peer when nobody qualifies.
func TestBuildAWGUserInboundMulti_Peers(t *testing.T) {
	preset := GetDefaultPreset()
	users := []model.User{
		{Name: "alice", Active: true, AWGPublicKey: "pub-alice", AWGAddress: "10.8.0.2/32"},
		{Name: "bob", Active: true, AWGPublicKey: "pub-bob", AWGAddress: "10.8.0.3/32"},
		{Name: "carol", Active: false, AWGPublicKey: "pub-carol", AWGAddress: "10.8.0.4/32"}, // inactive -> skip
		{Name: "dave", Active: true, AWGPublicKey: "", AWGAddress: "10.8.0.5/32"},            // no pub -> skip
		{Name: "eve", Active: true, AWGPublicKey: "pub-eve", AWGAddress: ""},                  // no addr -> skip
	}
	epJSON, _, err := buildAWGUserInboundMulti(8443, "ch-x-user-in", &preset, "", users, nil)
	if err != nil {
		t.Fatalf("buildAWGUserInboundMulti: %v", err)
	}
	var ep config.WireGuardEndpoint
	if err := json.Unmarshal(epJSON, &ep); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, epJSON)
	}
	if ep.Tag != "ch-x-user-in" {
		t.Errorf("tag=%s, want ch-x-user-in", ep.Tag)
	}
	if len(ep.Peers) != 2 {
		t.Fatalf("want 2 peers (alice, bob), got %d: %+v", len(ep.Peers), ep.Peers)
	}
	wantPeers := map[string]string{"pub-alice": "10.8.0.2/32", "pub-bob": "10.8.0.3/32"}
	for _, p := range ep.Peers {
		want, ok := wantPeers[p.PublicKey]
		if !ok {
			t.Errorf("unexpected peer pubkey %s", p.PublicKey)
			continue
		}
		if len(p.AllowedIPs) != 1 || p.AllowedIPs[0] != want {
			t.Errorf("peer %s allowed_ips=%v, want [%s]", p.PublicKey, p.AllowedIPs, want)
		}
	}
}

// TestBuildAWGUserInboundMulti_NoQualifiedUsers_Placeholder — when no user has
// AWG creds, the endpoint still gets a placeholder peer so the config is valid.
func TestBuildAWGUserInboundMulti_NoQualifiedUsers_Placeholder(t *testing.T) {
	preset := GetDefaultPreset()
	users := []model.User{{Name: "alice", Active: true}} // no AWG creds
	epJSON, _, err := buildAWGUserInboundMulti(8443, "ch-x-user-in", &preset, "", users, nil)
	if err != nil {
		t.Fatalf("buildAWGUserInboundMulti: %v", err)
	}
	var ep config.WireGuardEndpoint
	if err := json.Unmarshal(epJSON, &ep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ep.Peers) != 1 {
		t.Fatalf("want 1 placeholder peer, got %d", len(ep.Peers))
	}
	if ep.Peers[0].PublicKey != "CLIENT_PUBLIC_KEY_HERE" {
		t.Errorf("placeholder pubkey=%s, want CLIENT_PUBLIC_KEY_HERE", ep.Peers[0].PublicKey)
	}
}

// TestRenderClientAWGConf_PerUser verifies the per-user awg-quick .conf carries
// the user's AWGPrivateKey + AWGAddress, the server public key, and the entry
// endpoint. A pinned ChainExit to a direct entry selects that entry's host.
func TestRenderClientAWGConf_PerUser(t *testing.T) {
	c := &model.Chain{
		Name:              "awgchain",
		UserProtocol:      model.UserProtocolAWG,
		AWGEntryServerPub: "SERVER_PUB",
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "entry.example.test:22"},
			{ID: "exit", Addr: "exit.example.test:22"},
		},
	}
	u := &model.User{
		Name: "alice", Active: true,
		AWGPrivateKey: "ALICE_PRIV",
		AWGAddress:    "10.8.0.5/32",
	}
	conf, err := RenderClientAWGConf(ClientConfigParams{Chain: c, User: u})
	if err != nil {
		t.Fatalf("RenderClientAWGConf: %v", err)
	}
	for _, want := range []string{
		"Address = 10.8.0.5/32",
		"PrivateKey = ALICE_PRIV",
		"PublicKey = SERVER_PUB",
		"Endpoint = entry.example.test:",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("conf missing %q\n%s", want, conf)
		}
	}
}

// TestRenderClientAWGConf_PinnedEntry verifies ChainExit pin to a direct entry
// makes the .conf dial that entry's host (not the default first entry).
func TestRenderClientAWGConf_PinnedEntry(t *testing.T) {
	c := &model.Chain{
		Name:              "awgme",
		UserProtocol:      model.UserProtocolAWG,
		AWGEntryServerPub: "SERVER_PUB",
		Nodes: []model.ChainNode{
			{ID: "e1", Addr: "e1.example.test:22", Role: model.NodeRoleEntry},
			{ID: "e2", Addr: "e2.example.test:22", Role: model.NodeRoleEntry},
			{ID: "exit", Addr: "exit.example.test:22"},
		},
	}
	u := &model.User{
		Name: "bob", Active: true,
		AWGPrivateKey: "BOB_PRIV",
		AWGAddress:    "10.8.0.6/32",
		ChainExit:     map[string]string{"awgme": "e2"}, // pin to the 2nd entry
	}
	conf, err := RenderClientAWGConf(ClientConfigParams{Chain: c, User: u})
	if err != nil {
		t.Fatalf("RenderClientAWGConf: %v", err)
	}
	if !strings.Contains(conf, "Endpoint = e2.example.test:") {
		t.Errorf("pinned .conf should dial e2, got:\n%s", conf)
	}
	if strings.Contains(conf, "e1.example.test") {
		t.Errorf("pinned .conf must not dial e1, got:\n%s", conf)
	}
}

// TestRenderClientAWGConf_NotAWG — non-AWG chain returns an error.
func TestRenderClientAWGConf_NotAWG(t *testing.T) {
	c := &model.Chain{Name: "tuicchain", UserProtocol: model.UserProtocolTUIC,
		Nodes: []model.ChainNode{{ID: "entry", Addr: "e.test:22"}}}
	if _, err := RenderClientAWGConf(ClientConfigParams{Chain: c}); err == nil {
		t.Fatal("expected error for non-AWG chain")
	}
}

// TestAllocateAWGPeerIP verifies deterministic first-free allocation, skipping
// the server (.1) and already-taken addresses.
func TestAllocateAWGPeerIP(t *testing.T) {
	taken := []string{"10.8.0.2/32", "10.8.0.3/32", "10.8.0.5/32"}
	got := allocateAWGPeerIP(taken)
	if got != "10.8.0.4/32" {
		t.Errorf("first free after 2,3,5 = %s, want 10.8.0.4/32", got)
	}
	// normalization: "10.8.0.3" without /32 must collide with "10.8.0.3/32"
	got = allocateAWGPeerIP([]string{"10.8.0.2", "10.8.0.3/32"})
	if got != "10.8.0.4/32" {
		t.Errorf("normalized collision: got %s, want 10.8.0.4/32", got)
	}
	// empty taken -> first allocatable is .2
	if got := allocateAWGPeerIP(nil); got != "10.8.0.2/32" {
		t.Errorf("empty taken: got %s, want 10.8.0.2/32", got)
	}
}

// TestEnsureUserCreds_AWG_Keypair verifies AWG users get a WireGuard keypair
// (but no address — that needs the store via EnsureUserAWGAddress).
func TestEnsureUserCreds_AWG_Keypair(t *testing.T) {
	u := &model.User{Name: "alice", Active: true, Protocols: []string{"awg"}}
	EnsureUserCreds(u)
	if u.AWGPrivateKey == "" || u.AWGPublicKey == "" {
		t.Fatalf("AWG user should get a keypair, got priv=%q pub=%q", u.AWGPrivateKey, u.AWGPublicKey)
	}
	if u.AWGAddress != "" {
		t.Errorf("EnsureUserCreds must not allocate AWGAddress (needs store), got %q", u.AWGAddress)
	}
	// idempotent: second call preserves existing creds
	priv, pub := u.AWGPrivateKey, u.AWGPublicKey
	EnsureUserCreds(u)
	if u.AWGPrivateKey != priv || u.AWGPublicKey != pub {
		t.Error("EnsureUserCreds overwrote existing AWG keypair")
	}
}

// TestEnsureUserAWGAddress verifies address allocation against taken IPs.
func TestEnsureUserAWGAddress(t *testing.T) {
	u := &model.User{Name: "alice", Active: true, Protocols: []string{"awg"}}
	EnsureUserAWGAddress(u, []string{"10.8.0.2/32"})
	if u.AWGAddress != "10.8.0.3/32" {
		t.Errorf("AWGAddress=%s, want 10.8.0.3/32", u.AWGAddress)
	}
	// already set -> not changed
	u.AWGAddress = "10.8.0.9/32"
	EnsureUserAWGAddress(u, []string{"10.8.0.9/32"})
	if u.AWGAddress != "10.8.0.9/32" {
		t.Errorf("AWGAddress changed: %s", u.AWGAddress)
	}
	// non-AWG user -> no address
	other := &model.User{Name: "bob", Protocols: []string{"tuic"}}
	EnsureUserAWGAddress(other, nil)
	if other.AWGAddress != "" {
		t.Errorf("non-AWG user got AWGAddress %q", other.AWGAddress)
	}
}

// TestRenderClientConfig_PerUserCreds verifies that a User with per-user TUIC
// creds overrides the chain-wide creds in the rendered client config, and that
// a nil/no-cred user falls back to chain-wide (legacy).
func TestRenderClientConfig_PerUserCreds(t *testing.T) {
	c := &model.Chain{
		Name:                  "pu",
		UserProtocol:          model.UserProtocolTUIC,
		Strategy:              model.StrategyURLTest,
		TUICEntryUserUUID:     "chain-uuid",
		TUICEntryUserPassword: "chain-pass",
		Nodes:                 []model.ChainNode{{ID: "entry", Addr: "entry.example.test:22"}},
	}
	// Per-user creds used.
	cfg, err := RenderClientConfig(ClientConfigParams{Chain: c, User: &model.User{
		ID: "u1", Name: "alice", TUICUUID: "alice-uuid", TUICPassword: "alice-pass",
	}})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	if !strings.Contains(cfg, `"uuid": "alice-uuid"`) {
		t.Errorf("per-user TUICUUID not used in config\n%s", cfg)
	}
	if !strings.Contains(cfg, `"password": "alice-pass"`) {
		t.Errorf("per-user TUICPassword not used in config\n%s", cfg)
	}
}

func TestRenderClientConfig_NoUserCreds_FallsBackToChain(t *testing.T) {
	c := &model.Chain{
		Name:                  "fb",
		UserProtocol:          model.UserProtocolTUIC,
		Strategy:              model.StrategyURLTest,
		TUICEntryUserUUID:     "chain-uuid",
		TUICEntryUserPassword: "chain-pass",
		Nodes:                 []model.ChainNode{{ID: "entry", Addr: "entry.example.test:22"}},
	}
	// Nil user -> chain-wide creds.
	cfg, err := RenderClientConfig(ClientConfigParams{Chain: c})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	if !strings.Contains(cfg, `"uuid": "chain-uuid"`) {
		t.Errorf("chain-wide TUICUUID not used as fallback\n%s", cfg)
	}
	if !strings.Contains(cfg, `"password": "chain-pass"`) {
		t.Errorf("chain-wide TUICPassword not used as fallback\n%s", cfg)
	}
}
