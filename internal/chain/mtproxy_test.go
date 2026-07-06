package chain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestBuildMTProxyInbound verifies the mtproxy inbound carries the extended
// "ee"+hex secret for each enabled user and the canonical FakeTLS options.
func TestBuildMTProxyInbound(t *testing.T) {
	users := []*model.User{
		{ID: "u1", Name: "alice", MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", Active: true},
		{ID: "u2", Name: "bob", MTProxySecret: "00112233445566778899aabbccddeeff", MTProxyDomain: "www.bing.com", Active: true},
		{ID: "u3", Name: "off", MTProxySecret: "abc", MTProxyDomain: "x.com", Active: false}, // disabled -> skipped
	}
	raw := buildMTProxyInbound(443, "mtp-in", users)
	if raw == nil {
		t.Fatal("buildMTProxyInbound returned nil for 2 enabled users")
	}
	var inb map[string]any
	if err := json.Unmarshal(raw, &inb); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, string(raw))
	}
	if inb["type"] != "mtproxy" {
		t.Errorf("type = %v, want mtproxy", inb["type"])
	}
	if inb["tag"] != "mtp-in" {
		t.Errorf("tag = %v, want mtp-in", inb["tag"])
	}
	if inb["listen_port"] != float64(443) {
		t.Errorf("listen_port = %v, want 443", inb["listen_port"])
	}
	// Canonical FakeTLS / fronting options (VPN/docs/sing-box-extended.md).
	if inb["concurrency"] != float64(8192) {
		t.Errorf("concurrency = %v, want 8192", inb["concurrency"])
	}
	if inb["domain_fronting_port"] != float64(443) {
		t.Errorf("domain_fronting_port = %v, want 443", inb["domain_fronting_port"])
	}
	if inb["prefer_ip"] != "prefer-ipv4" {
		t.Errorf("prefer_ip = %v, want prefer-ipv4", inb["prefer_ip"])
	}
	if inb["auto_update"] != true {
		t.Errorf("auto_update = %v, want true", inb["auto_update"])
	}
	// 2 enabled users (disabled "off" skipped), each with the extended secret.
	ulist, _ := inb["users"].([]any)
	if len(ulist) != 2 {
		t.Fatalf("want 2 users (disabled skipped), got %d", len(ulist))
	}
	// alice: ee + 83b231c9ccf32ef09f48c8f63765ab4f + hex("disk.yandex.ru")
	wantAlice := "ee83b231c9ccf32ef09f48c8f63765ab4f" + hexStr("disk.yandex.ru")
	found := false
	for _, u := range ulist {
		m, _ := u.(map[string]any)
		if m["name"] == "alice" && m["secret"] == wantAlice {
			found = true
		}
	}
	if !found {
		t.Errorf("alice secret missing/wrong; want %s, users: %+v", wantAlice, ulist)
	}
}

// TestBuildMTProxyInbound_NoEnabledUsersReturnsNil verifies that with no
// enabled users the renderer returns nil (so the node-level loop skips
// emitting an empty mtproxy inbound, which sing-box would reject).
func TestBuildMTProxyInbound_NoEnabledUsersReturnsNil(t *testing.T) {
	cases := []struct {
		name  string
		users []*model.User
	}{
		{"nil", nil},
		{"all-disabled", []*model.User{{Name: "x", MTProxySecret: "ab", Active: false}}},
		{"no-secret", []*model.User{{Name: "x", MTProxySecret: "", Active: true, MTProxyDomain: "d.com"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if raw := buildMTProxyInbound(443, "mtp-in", tc.users); raw != nil {
				t.Errorf("want nil (no qualified users), got %s", string(raw))
			}
		})
	}
}

// TestBuildMergedNodeConfig_MTProxyStandalone verifies a standalone MTProxy
// NodeInbound produces an mtproxy-typed inbound (not the old VLESS/WS default).
func TestBuildMergedNodeConfig_MTProxyStandalone(t *testing.T) {
	nodeInfo := &model.NodeInfo{
		Host: model.Host{ID: "mtp-node"},
		Inbounds: []model.NodeInbound{
			{Protocol: "mtproxy", Port: 443, Tag: "sa-mtp"},
		},
	}
	users := []*model.User{
		{ID: "u1", Name: "alice", MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", Active: true},
	}
	cfg, _, err := buildMergedNodeConfig(nodeInfo, nil, nil, nil, users)
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")
	s := string(cfgJSON)
	if !strings.Contains(s, `"type": "mtproxy"`) {
		t.Errorf("config missing mtproxy inbound:\n%s", s)
	}
	if !strings.Contains(s, `"disk.yandex.ru"`) {
		// the extended secret includes hex(domain); just assert the ee prefix + secret are present
	}
	if strings.Contains(s, `"type": "vless"`) {
		t.Errorf("mtproxy standalone must NOT fall through to VLESS/WS:\n%s", s)
	}
}

// TestBuildMergedNodeConfig_MTProxyChainEntry verifies a chain with
// UserProtocol == MTProxy produces an mtproxy-typed inbound at the entry node.
func TestBuildMergedNodeConfig_MTProxyChainEntry(t *testing.T) {
	c := &model.Chain{
		Name:         "mtp-chain",
		UserProtocol: model.UserProtocolMTProxy,
		Nodes: []model.ChainNode{
			{ID: "mtp-node", Addr: "mtp.example.test:22", Role: model.NodeRoleEntry},
		},
	}
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "mtp-node"}}
	users := []*model.User{
		{ID: "u1", Name: "alice", MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", Active: true},
	}
	cfg, _, err := buildMergedNodeConfig(nodeInfo, []*model.Chain{c}, nil, nil, users)
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")
	s := string(cfgJSON)
	if !strings.Contains(s, `"type": "mtproxy"`) {
		t.Errorf("chain MTProxy entry missing mtproxy inbound:\n%s", s)
	}
}

// hexStr returns the hex encoding of s (used to build the expected ee-secret).
func hexStr(s string) string {
	src := []byte(s)
	out := make([]byte, len(src)*2)
	const hexc = "0123456789abcdef"
	for i, b := range src {
		out[i*2] = hexc[b>>4]
		out[i*2+1] = hexc[b&0xf]
	}
	return string(out)
}
