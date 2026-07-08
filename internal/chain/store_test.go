package chain

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
	"golang.org/x/crypto/ssh"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := tempDir(t)
	return NewStore(filepath.Join(dir, "store.json"))
}

func seedHost(t *testing.T, s *Store, id, addr string) *model.Host {
	t.Helper()
	h := &model.Host{ID: id, Addr: addr, User: "root", KeyPath: "/key"}
	if err := s.SaveHost(h); err != nil {
		t.Fatalf("SaveHost: %v", err)
	}
	return h
}

// ─── Hosts ────────────────────────────────────────────────────────────────────

func TestSaveAndGetHost(t *testing.T) {
	s := tempStore(t)
	h := seedHost(t, s, "node1", "1.2.3.4:22")

	got, err := s.GetHost("node1")
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if got.ID != h.ID || got.Addr != h.Addr {
		t.Errorf("got %+v, want %+v", got, h)
	}
}

// TestStore_GetNotFound is a table-driven test covering GetHost/GetChain/
// GetUser on a missing entity (replaces the three separate _NotFound funcs +
// GetHost_EmptyStore — CTO-review §13 table-driven).
func TestStore_GetNotFound(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*Store) error
	}{
		{"GetHost not found", func(s *Store) error { _, err := s.GetHost("nobody"); return err }},
		{"GetHost empty store", func(s *Store) error { _, err := s.GetHost("any"); return err }},
		{"GetChain not found", func(s *Store) error { _, err := s.GetChain("no-chain"); return err }},
		{"GetUser not found", func(s *Store) error { _, err := s.GetUser("nobody"); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tempStore(t)
			if err := tc.fn(s); err == nil {
				t.Fatal("expected error for missing entity")
			}
		})
	}
}

func TestListHosts(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "a", "1.1.1.1:22")
	seedHost(t, s, "b", "2.2.2.2:22")

	list, err := s.ListHosts()
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(list))
	}
}

func TestListHosts_Empty(t *testing.T) {
	s := tempStore(t)
	list, err := s.ListHosts()
	if err != nil {
		t.Fatalf("ListHosts empty: %v", err)
	}
	if list != nil {
		t.Fatal("expected nil for empty store")
	}
}

func TestSaveHost_Update(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "node1", "1.2.3.4:22")

	updated := &model.Host{ID: "node1", Addr: "5.6.7.8:2222", User: "admin", KeyPath: "/newkey"}
	if err := s.SaveHost(updated); err != nil {
		t.Fatalf("SaveHost update: %v", err)
	}

	got, _ := s.GetHost("node1")
	if got.Addr != "5.6.7.8:2222" || got.User != "admin" {
		t.Errorf("update not persisted: %+v", got)
	}
}

func TestDeleteHost(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "node1", "1.2.3.4:22")

	if err := s.DeleteHost("node1"); err != nil {
		t.Fatalf("DeleteHost: %v", err)
	}

	_, err := s.GetHost("node1")
	if err == nil {
		t.Fatal("host should be deleted")
	}
}

func TestDeleteHost_ReferencedByChain(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "node1", "1.2.3.4:22")
	seedHost(t, s, "node2", "5.6.7.8:22")

	c := &model.Chain{
		Name: "test-chain",
		Nodes: []model.ChainNode{
			{ID: "node1", Addr: "1.2.3.4:22"},
			{ID: "node2", Addr: "5.6.7.8:22"},
		},
	}
	s.SaveChain(c)

	err := s.DeleteHost("node1")
	if err == nil {
		t.Fatal("expected error: host referenced by chain")
	}
}

func TestDeleteHost_NotFound(t *testing.T) {
	s := tempStore(t)
	err := s.DeleteHost("nobody")
	if err == nil {
		t.Fatal("expected error for missing host")
	}
}

// ─── Chains ───────────────────────────────────────────────────────────────────

func TestSaveAndGetChain(t *testing.T) {
	s := tempStore(t)
	c := &model.Chain{
		Name:     "my-chain",
		Strategy: model.StrategyURLTest,
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "1.1.1.1:22"},
			{ID: "n2", Addr: "2.2.2.2:22"},
		},
	}
	if err := s.SaveChain(c); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.GetChain("my-chain")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if got.Name != c.Name || len(got.Nodes) != 2 {
		t.Errorf("got %+v, want %+v", got, c)
	}
}

func TestListChains(t *testing.T) {
	s := tempStore(t)
	s.SaveChain(&model.Chain{Name: "a", Nodes: []model.ChainNode{{ID: "x"}}})
	s.SaveChain(&model.Chain{Name: "b", Nodes: []model.ChainNode{{ID: "y"}}})

	list, err := s.ListChains()
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(list))
	}
}

func TestListChains_Empty(t *testing.T) {
	s := tempStore(t)
	list, err := s.ListChains()
	if err != nil {
		t.Fatalf("ListChains empty: %v", err)
	}
	if list != nil {
		t.Fatal("expected nil for empty store")
	}
}

func TestSaveChain_Update(t *testing.T) {
	s := tempStore(t)
	s.SaveChain(&model.Chain{Name: "c", Nodes: []model.ChainNode{{ID: "x"}}})

	updated := &model.Chain{
		Name:  "c",
		Nodes: []model.ChainNode{{ID: "x"}, {ID: "y"}},
	}
	s.SaveChain(updated)

	got, _ := s.GetChain("c")
	if len(got.Nodes) != 2 {
		t.Errorf("expected 2 nodes after update, got %d", len(got.Nodes))
	}
}

func TestDeleteChain(t *testing.T) {
	s := tempStore(t)
	s.SaveChain(&model.Chain{Name: "to-delete", Nodes: []model.ChainNode{{ID: "x"}}})

	if err := s.DeleteChain("to-delete"); err != nil {
		t.Fatalf("DeleteChain: %v", err)
	}

	_, err := s.GetChain("to-delete")
	if err == nil {
		t.Fatal("chain should be deleted")
	}
}

func TestDeleteChain_NotFound(t *testing.T) {
	s := tempStore(t)
	err := s.DeleteChain("no-such-chain")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetChainsForNode(t *testing.T) {
	s := tempStore(t)
	s.SaveChain(&model.Chain{
		Name: "chain-a",
		Nodes: []model.ChainNode{
			{ID: "shared", Addr: "1.1.1.1:22"},
		},
	})
	s.SaveChain(&model.Chain{
		Name: "chain-b",
		Nodes: []model.ChainNode{
			{ID: "shared", Addr: "1.1.1.1:22"},
			{ID: "other", Addr: "2.2.2.2:22"},
		},
	})

	chains, err := s.GetChainsForNode("shared")
	if err != nil {
		t.Fatalf("GetChainsForNode: %v", err)
	}
	if len(chains) != 2 {
		t.Errorf("expected 2 chains for shared node, got %d", len(chains))
	}
}

func TestGetChainsForNode_None(t *testing.T) {
	s := tempStore(t)
	chains, err := s.GetChainsForNode("lonely")
	if err != nil {
		t.Fatalf("GetChainsForNode: %v", err)
	}
	if len(chains) != 0 {
		t.Errorf("expected 0 chains, got %d", len(chains))
	}
}

func TestResolveNodes(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.1.1.1:22")
	seedHost(t, s, "n2", "2.2.2.2:2222")
	s.SaveNodeInfo(&model.NodeInfo{
		Host:    model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root"},
		Country: "RU",
	})

	c := &model.Chain{
		Name: "resolve-test",
		Nodes: []model.ChainNode{
			{ID: "n1", Port: 443},
			{ID: "n2", Port: 8443},
		},
	}

	resolved, err := s.ResolveNodes(c)
	if err != nil {
		t.Fatalf("ResolveNodes: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved, got %d", len(resolved))
	}
	if resolved[0].Addr != "1.1.1.1:22" {
		t.Errorf("node0 addr: %s", resolved[0].Addr)
	}
	if resolved[1].Addr != "2.2.2.2:2222" {
		t.Errorf("node1 addr: %s", resolved[1].Addr)
	}
	if resolved[0].Port != 443 {
		t.Errorf("node0 port: %d", resolved[0].Port)
	}
}

func TestResolveNodes_MissingHost(t *testing.T) {
	s := tempStore(t)
	c := &model.Chain{
		Name:  "bad-chain",
		Nodes: []model.ChainNode{{ID: "ghost"}},
	}
	_, err := s.ResolveNodes(c)
	if err == nil {
		t.Fatal("expected error for missing host")
	}
}

// TestResolveNodes_PreservesAllTransitFields is the regression for the
// relocation/re-apply bug: ResolveNodes rebuilt a fresh ChainNode copying only
// Port + the 3 Reality transit fields + Inbounds, dropping Role/ExitTargets +
// every AWG transit/exit field. On the next ApplyChain after a process restart
// those AWG fields were empty → keys regenerated → inter-node AWG links broke
// (previous node's outbound peer.PublicKey no longer matched the new server
// pubkey; balancer awg-exit-nX no longer matched the exit's new server key).
// Relocation (update Addr + re-apply to reuse keys) is impossible while
// ResolveNodes strips the material. This test pins that EVERY persisted
// ChainNode field survives ResolveNodes.
func TestResolveNodes_PreservesAllTransitFields(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.1.1.1:22")
	s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root"}})

	seeded := model.ChainNode{
		ID:                   "n1",
		Port:                 443,
		Role:                 model.NodeRoleExit,
		ExitTargets:          []string{"n1"},
		TransitPrivKey:       "REALITY-PRIV",
		TransitShortID:       "deadbeef",
		TransitUUID:          "uuid-1234",
		TransitAWGServerPriv: "awg-srv-priv",
		TransitAWGServerPub:  "awg-srv-pub",
		TransitAWGClientPriv: "awg-cli-priv",
		TransitAWGClientPub:  "awg-cli-pub",
		TransitAWGAddress:    "10.9.0.5/32",
		TransitAWGClientPort: 51821,
		ExitAWGServerPriv:    "exit-srv-priv",
		ExitAWGServerPub:     "exit-srv-pub",
		ExitAWGListenPort:     52001,
		ExitAWGLinks:         []model.AWGExitLink{{TargetID: "n1", InterfaceName: "awg-exit-n1", ClientPriv: "lk-priv", ClientPub: "lk-pub", Address: "10.10.0.2/32", ClientPort: 53001}},
	}
	c := &model.Chain{Name: "relo", Nodes: []model.ChainNode{seeded}}
	if err := s.SaveChain(c); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.ResolveNodes(c)
	if err != nil {
		t.Fatalf("ResolveNodes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 node, got %d", len(got))
	}
	r := got[0]
	// Every persisted transit/identity field must survive ResolveNodes (the
	// relocation/re-apply invariant). A dropped field here means ApplyChain
	// regenerates it → the inter-node link breaks.
	type fieldCheck struct {
		name string
		got  string
		want string
	}
	checks := []fieldCheck{
		{"Role", string(r.Role), string(seeded.Role)},
		{"TransitPrivKey", r.TransitPrivKey, seeded.TransitPrivKey},
		{"TransitShortID", r.TransitShortID, seeded.TransitShortID},
		{"TransitUUID", r.TransitUUID, seeded.TransitUUID},
		{"TransitAWGServerPriv", r.TransitAWGServerPriv, seeded.TransitAWGServerPriv},
		{"TransitAWGServerPub", r.TransitAWGServerPub, seeded.TransitAWGServerPub},
		{"TransitAWGClientPriv", r.TransitAWGClientPriv, seeded.TransitAWGClientPriv},
		{"TransitAWGClientPub", r.TransitAWGClientPub, seeded.TransitAWGClientPub},
		{"TransitAWGAddress", r.TransitAWGAddress, seeded.TransitAWGAddress},
		{"ExitAWGServerPriv", r.ExitAWGServerPriv, seeded.ExitAWGServerPriv},
		{"ExitAWGServerPub", r.ExitAWGServerPub, seeded.ExitAWGServerPub},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q (ResolveNodes dropped it)", c.name, c.got, c.want)
		}
	}
	if r.Port != seeded.Port {
		t.Errorf("Port: got %d, want %d", r.Port, seeded.Port)
	}
	if r.TransitAWGClientPort != seeded.TransitAWGClientPort {
		t.Errorf("TransitAWGClientPort: got %d, want %d", r.TransitAWGClientPort, seeded.TransitAWGClientPort)
	}
	if r.ExitAWGListenPort != seeded.ExitAWGListenPort {
		t.Errorf("ExitAWGListenPort: got %d, want %d", r.ExitAWGListenPort, seeded.ExitAWGListenPort)
	}
	if len(r.ExitTargets) != 1 || r.ExitTargets[0] != "n1" {
		t.Errorf("ExitTargets: got %v, want [n1]", r.ExitTargets)
	}
	if len(r.ExitAWGLinks) != 1 || r.ExitAWGLinks[0].ClientPriv != "lk-priv" {
		t.Errorf("ExitAWGLinks: got %+v, want 1 link with ClientPriv=lk-priv", r.ExitAWGLinks)
	}
}

// TestResolveNodes_ReapplyKeepsAWGKeys verifies a second ResolveNodes (simulating
// a re-apply after a process restart) does not lose the AWG transit material —
// the core relocation/re-apply invariant.
func TestResolveNodes_ReapplyKeepsAWGKeys(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.1.1.1:22")
	s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root"}})
	c := &model.Chain{Name: "relo2", Nodes: []model.ChainNode{{
		ID:                   "n1",
		TransitAWGServerPriv: "awg-srv-priv",
		TransitAWGServerPub:  "awg-srv-pub",
		ExitAWGServerPriv:    "exit-srv-priv",
		Role:                 model.NodeRoleExit,
		ExitTargets:          []string{"n1"},
	}}}
	if err := s.SaveChain(c); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	first, _ := s.ResolveNodes(c)
	// Simulate re-apply: load chain fresh from store + ResolveNodes again.
	c2, err := s.GetChain("relo2")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	second, err := s.ResolveNodes(c2)
	if err != nil {
		t.Fatalf("second ResolveNodes: %v", err)
	}
	if second[0].TransitAWGServerPriv != first[0].TransitAWGServerPriv {
		t.Errorf("TransitAWGServerPriv changed across re-apply: %q -> %q", first[0].TransitAWGServerPriv, second[0].TransitAWGServerPriv)
	}
	if second[0].ExitAWGServerPriv != first[0].ExitAWGServerPriv {
		t.Errorf("ExitAWGServerPriv changed across re-apply: %q -> %q", first[0].ExitAWGServerPriv, second[0].ExitAWGServerPriv)
	}
	if second[0].Role != first[0].Role || len(second[0].ExitTargets) != 1 {
		t.Errorf("Role/ExitTargets not preserved across re-apply: %+v", second[0])
	}
}

// ─── Users ─────────────────────────────────────────────────────────────────────

func TestSaveAndGetUser(t *testing.T) {
	s := tempStore(t)
	u := &model.User{
		ID:        "user1",
		Name:      "Alice",
		Active:    true,
		Protocols: []string{"awg"},
	}
	if err := s.SaveUser(u); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	got, err := s.GetUser("user1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Name != "Alice" || !got.Active {
		t.Errorf("got %+v", got)
	}
}

func TestListUsers(t *testing.T) {
	s := tempStore(t)
	s.SaveUser(&model.User{ID: "u1", Name: "A"})
	s.SaveUser(&model.User{ID: "u2", Name: "B"})

	list, _ := s.ListUsers()
	if len(list) != 2 {
		t.Errorf("expected 2 users, got %d", len(list))
	}
}

// TestGetUserBySubscriptionToken verifies the public /sub/{token} lookup: a
// user is found by token, an empty token never matches, and an unknown token
// returns ErrUserNotFound.
func TestGetUserBySubscriptionToken(t *testing.T) {
	s := tempStore(t)
	s.SaveUser(&model.User{ID: "u1", Name: "A", SubscriptionToken: "tok-A"})
	s.SaveUser(&model.User{ID: "u2", Name: "B", SubscriptionToken: "tok-B"})
	s.SaveUser(&model.User{ID: "u3", Name: "NoToken"})

	got, err := s.GetUserBySubscriptionToken("tok-A")
	if err != nil {
		t.Fatalf("GetUserBySubscriptionToken: %v", err)
	}
	if got.ID != "u1" {
		t.Errorf("got user %q, want u1", got.ID)
	}

	if _, err := s.GetUserBySubscriptionToken("tok-B"); err != nil {
		t.Errorf("second known token: %v", err)
	}
	if _, err := s.GetUserBySubscriptionToken(""); err == nil {
		t.Error("empty token must not match")
	}
	if _, err := s.GetUserBySubscriptionToken("no-such-token"); err == nil {
		t.Error("unknown token must return ErrUserNotFound")
	}
}

func TestDeleteUser(t *testing.T) {
	s := tempStore(t)
	s.SaveUser(&model.User{ID: "u1", Name: "A"})

	if err := s.DeleteUser("u1"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	_, err := s.GetUser("u1")
	if err == nil {
		t.Fatal("user should be deleted")
	}
}

func TestDeleteUser_NotFound(t *testing.T) {
	s := tempStore(t)
	err := s.DeleteUser("nobody")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSaveUser_SetsCreatedAt(t *testing.T) {
	s := tempStore(t)
	u := &model.User{ID: "u1", Name: "A"}
	s.SaveUser(u)

	got, _ := s.GetUser("u1")
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

// ─── NodeInfo ──────────────────────────────────────────────────────────────────

func TestSaveAndGetNodeInfo(t *testing.T) {
	s := tempStore(t)
	ni := &model.NodeInfo{
		Host:    model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root"},
		Country: "IR",
		Inbounds: []model.NodeInbound{
			{Protocol: "vless-reality", Port: 443, UUID: "test-uuid"},
		},
	}
	s.SaveNodeInfo(ni)

	got, err := s.GetNodeInfo("n1")
	if err != nil {
		t.Fatalf("GetNodeInfo: %v", err)
	}
	if got.Country != "IR" || len(got.Inbounds) != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestGetNodeInfo_NotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.GetNodeInfo("ghost")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListNodeInfos(t *testing.T) {
	s := tempStore(t)
	s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "a"}})
	s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "b"}})

	list, _ := s.ListNodeInfos()
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

func TestSaveNodeInfo_Update(t *testing.T) {
	s := tempStore(t)
	s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "n1"}, Country: "RU"})
	s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "n1"}, Country: "CN"})

	got, _ := s.GetNodeInfo("n1")
	if got.Country != "CN" {
		t.Errorf("expected CN, got %s", got.Country)
	}
}

// ─── Metrics ───────────────────────────────────────────────────────────────────

func TestSaveAndGetMetrics(t *testing.T) {
	s := tempStore(t)
	m := &model.NodeMetrics{HostID: "n1", Online: true, Version: "1.12.0", LatencyMs: 42}
	s.SaveMetrics(m)

	got, err := s.GetMetrics("n1")
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if !got.Online || got.Version != "1.12.0" {
		t.Errorf("got %+v", got)
	}
}

func TestGetMetrics_NotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.GetMetrics("nobody")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListMetrics(t *testing.T) {
	s := tempStore(t)
	s.SaveMetrics(&model.NodeMetrics{HostID: "a", Online: true})
	s.SaveMetrics(&model.NodeMetrics{HostID: "b", Online: false})

	list, _ := s.ListMetrics()
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

func TestSaveMetrics_LastChecked(t *testing.T) {
	s := tempStore(t)
	m := &model.NodeMetrics{HostID: "n1"}
	s.SaveMetrics(m)

	got, _ := s.GetMetrics("n1")
	if got.LastChecked.IsZero() {
		t.Error("LastChecked should be set")
	}
}

// ─── Settings ──────────────────────────────────────────────────────────────────

func TestGetSettings_Default(t *testing.T) {
	s := tempStore(t)
	settings, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if settings.MetricsInterval <= 0 {
		t.Errorf("default MetricsInterval should be > 0, got %d", settings.MetricsInterval)
	}
}

func TestSaveAndGetSettings(t *testing.T) {
	s := tempStore(t)
	custom := &model.PanelSettings{MetricsInterval: 60, Language: "ru"}
	s.SaveSettings(custom)

	got, _ := s.GetSettings()
	if got.MetricsInterval != 60 || got.Language != "ru" {
		t.Errorf("got %+v", got)
	}
}

// ─── KnownHosts / HostKeyManager ───────────────────────────────────────────────

func TestSaveAndGetKnownHost(t *testing.T) {
	s := tempStore(t)
	kh := &model.KnownHost{Addr: "1.2.3.4", Fingerprint: "SHA256:abc", Trusted: true}
	s.SaveKnownHost(kh)

	got, err := s.GetKnownHost("1.2.3.4")
	if err != nil {
		t.Fatalf("GetKnownHost: %v", err)
	}
	if got.Fingerprint != "SHA256:abc" || !got.Trusted {
		t.Errorf("got %+v", got)
	}
}

func TestGetKnownHost_NotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.GetKnownHost("9.9.9.9")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckHostKey_TOFU(t *testing.T) {
	s := tempStore(t)
	// Use a real ed25519 key for fingerprint
	pubKey := testPubKey(t)

	err := s.CheckHostKey("new-host", pubKey)
	if err != nil {
		t.Fatalf("TOFU should succeed: %v", err)
	}

	// Verify it was saved
	kh, err := s.GetKnownHost("new-host")
	if err != nil {
		t.Fatalf("should be saved after TOFU: %v", err)
	}
	if !kh.Trusted {
		t.Error("TOFU key should be trusted")
	}
}

func TestCheckHostKey_Changed(t *testing.T) {
	s := tempStore(t)
	pub1 := testPubKey(t)

	// First use — TOFU
	s.CheckHostKey("host-x", pub1)

	// Second use with different key — should reject
	// Generate a different key by using a different seed
	_ = pub1
	err := s.CheckHostKey("host-x", testPubKey2(t))
	if err == nil {
		t.Fatal("expected HostKeyError for changed key")
	}
	hkErr, ok := err.(*sshclient.HostKeyError)
	if !ok {
		t.Fatalf("expected *HostKeyError, got %T", err)
	}
	if !hkErr.Changed {
		t.Error("error should indicate key changed")
	}
}

func TestCheckHostKey_Untrusted(t *testing.T) {
	s := tempStore(t)
	pub := testPubKey(t)
	s.SaveKnownHost(&model.KnownHost{Addr: "untrusted-host", Fingerprint: ssh.FingerprintSHA256(pub), Trusted: false})

	err := s.CheckHostKey("untrusted-host", pub)
	if err == nil {
		t.Fatal("expected HostKeyError for untrusted key")
	}
}

func TestResolveKey_System(t *testing.T) {
	s := tempStore(t)
	// "system-" prefix triggers home dir lookup
	data, ok := s.ResolveKey("system-nonexistent-file-99999")
	if ok || data != "" {
		t.Error("should not resolve non-existent system key")
	}
}

func TestResolveKey_Stored(t *testing.T) {
	s := tempStore(t)
	s.SaveSettings(&model.PanelSettings{
		SSHKeys: []model.SSHKeyEntry{
			{ID: "key-1", Name: "test", KeyData: "my-private-key-data"},
		},
	})

	data, ok := s.ResolveKey("key-1")
	if !ok {
		t.Fatal("should resolve stored key")
	}
	if data != "my-private-key-data" {
		t.Errorf("got %q", data)
	}
}

func TestResolveKey_Empty(t *testing.T) {
	s := tempStore(t)
	_, ok := s.ResolveKey("")
	if ok {
		t.Error("empty key should not resolve")
	}
}

// ─── Concurrency ───────────────────────────────────────────────────────────────

func TestConcurrentSaveHost(t *testing.T) {
	s := tempStore(t)
	var wg sync.WaitGroup
	n := 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("host-%d", idx)
			s.SaveHost(&model.Host{ID: id, Addr: fmt.Sprintf("10.0.0.%d:22", idx), User: "root"})
		}(i)
	}
	wg.Wait()

	list, _ := s.ListHosts()
	if len(list) != n {
		t.Errorf("expected %d hosts after concurrent writes, got %d", n, len(list))
	}
}

func TestConcurrentReadWhileWrite(t *testing.T) {
	s := tempStore(t)
	// Pre-populate
	for i := 0; i < 20; i++ {
		seedHost(t, s, fmt.Sprintf("h%d", i), fmt.Sprintf("10.0.0.%d:22", i))
	}

	var wg sync.WaitGroup
	// Writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.SaveHost(&model.Host{ID: fmt.Sprintf("w%d", idx), Addr: fmt.Sprintf("10.1.0.%d:22", idx), User: "root"})
		}(i)
	}
	// Readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ListHosts()
			s.GetHost("h0")
		}()
	}
	wg.Wait()

	list, _ := s.ListHosts()
	if len(list) < 20 {
		t.Errorf("expected at least 20 hosts, got %d", len(list))
	}
}

func TestConcurrentChainOperations(t *testing.T) {
	s := tempStore(t)
	var wg sync.WaitGroup
	n := 30

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("chain-%d", idx)
			s.SaveChain(&model.Chain{Name: name, Nodes: []model.ChainNode{{ID: fmt.Sprintf("n%d", idx)}}})
		}(i)
	}
	wg.Wait()

	list, _ := s.ListChains()
	if len(list) != n {
		t.Errorf("expected %d chains, got %d", n, len(list))
	}
}

// ─── Edge cases ────────────────────────────────────────────────────────────────

func TestStore_ReadNonExistentFile(t *testing.T) {
	s := tempStore(t)
	// File doesn't exist yet — readStore should return os.IsNotExist
	_, err := s.readStore()
	if !os.IsNotExist(err) {
		t.Errorf("expected IsNotExist, got %v", err)
	}
}

func TestStore_PersistenceAcrossInstances(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "store.json")

	s1 := NewStore(path)
	seedHost(t, s1, "persist", "1.1.1.1:22")

	// New store instance with same path
	s2 := NewStore(path)
	got, err := s2.GetHost("persist")
	if err != nil {
		t.Fatalf("data not persisted across instances: %v", err)
	}
	if got.Addr != "1.1.1.1:22" {
		t.Errorf("addr mismatch: %s", got.Addr)
	}
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

func testPubKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	// Generate a minimal ed25519 key for testing
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("new public key: %v", err)
	}
	return pub
}

func testPubKey2(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate test key2: %v", err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("new public key2: %v", err)
	}
	return pub
}
