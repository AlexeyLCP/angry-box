package chain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// caddyModeNode returns a node in caddy mode (TLS domain + caddy installed)
// with a realistic inbound mix: two standalone inbounds on 443 (legal behind
// the SNI router), a naive on a free port, and an AWG server on UDP 443.
func caddyModeNode() *model.NodeInfo {
	info := &model.NodeInfo{
		Host:      model.Host{ID: "n1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		TLSDomain: "node1.example.com",
		Utilities: []*model.UtilityState{
			{Name: model.UtilityCaddy, Installed: true},
			{Name: model.UtilityACME, Installed: true},
		},
		Inbounds: []model.NodeInbound{
			{Protocol: "vless-reality", Port: 443, Source: "standalone", UUID: "u-1", ServerPrivKey: "pk", ShortID: "sid"},
			{Protocol: "naive", Port: 443, Source: "standalone"},
			{Protocol: "naive", Port: 8443, Source: "standalone"},
			{Protocol: "awg", Port: 443, Source: "standalone"},
		},
	}
	return info
}

func TestRenderMergedNodeConfig_CaddyMode(t *testing.T) {
	info := caddyModeNode()
	cfg, _, err := RenderMergedNodeConfig(info, nil, nil)
	if err != nil {
		t.Fatalf("two inbounds on 443 must NOT conflict in caddy mode: %v", err)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)

	// Reality (owned 443), naive-on-443 and naive-on-8443 (caddy's internal
	// HTTPS listener owns 8443) all remap to distinct loopback internals. AWG
	// (UDP) emits no sing-box listener.
	if !strings.Contains(s, `"listen":"127.0.0.1"`) {
		t.Fatal("caddy-fronted inbounds must bind loopback")
	}
	if strings.Contains(s, `"listen":"0.0.0.0"`) {
		t.Fatal("no public listeners allowed for fronted inbounds in caddy mode")
	}
	for _, port := range []string{`"listen_port":11000`, `"listen_port":11001`, `"listen_port":11002`} {
		if !strings.Contains(s, port) {
			t.Fatalf("expected %s in config: %s", port, s)
		}
	}
	// TLS-terminating naive serves the acme cert by path (SAN covers its SNI).
	if !strings.Contains(s, `"certificate_path":"/etc/angry-box-certs/node1.example.com/fullchain.pem"`) {
		t.Fatalf("naive must use the acme cert path in caddy mode: %s", s)
	}
	if !strings.Contains(s, `"key_path":"/etc/angry-box-certs/node1.example.com/key.pem"`) {
		t.Fatalf("naive must use the acme key path in caddy mode: %s", s)
	}
}

func TestRenderMergedNodeConfig_LegacyModeUnchanged(t *testing.T) {
	info := caddyModeNode()
	info.TLSDomain = "" // caddy mode off -> legacy direct listeners
	info.Utilities = nil
	cfg, _, err := RenderMergedNodeConfig(info, nil, nil)
	if err == nil {
		// Two TCP inbounds on 443 collide WITHOUT caddy — the conflict must
		// still be enforced in legacy mode.
		t.Fatal("expected a 443 port conflict in legacy mode")
	}
	_ = cfg
}

func TestDetectPortConflicts_CaddyVsChain(t *testing.T) {
	// The chain's TRANSPORT hop (n0, the non-entry node) listens on the
	// default transport port 443 — that must loudly conflict with the caddy
	// utility's ownership of 443.
	c := &model.Chain{
		Name:      "c1",
		Transport: model.TransportReality,
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
			{ID: "n0", Addr: "5.6.7.8:22", User: "root", KeyPath: "/k"},
		},
	}
	info := &model.NodeInfo{
		Host:      model.Host{ID: "n0", Addr: "5.6.7.8:22", User: "root", KeyPath: "/k"},
		TLSDomain: "node0.example.com",
		Utilities: []*model.UtilityState{{Name: model.UtilityCaddy, Installed: true}},
	}
	_, _, err := RenderMergedNodeConfig(info, []*model.Chain{c}, nil)
	if err == nil || !strings.Contains(err.Error(), "caddy") {
		t.Fatalf("expected a caddy-vs-chain port conflict, got: %v", err)
	}
}

func TestRemapInboundPorts_UDPExempt(t *testing.T) {
	inbounds := []model.NodeInbound{
		{Protocol: "awg", Port: 443},                          // UDP -> keep
		{Protocol: "mieru", Port: 443, MieruTransport: "UDP"}, // UDP -> keep
		{Protocol: "mieru", Port: 443, MieruTransport: "TCP"}, // TCP -> remap
	}
	got := RemapInboundPorts(inbounds)
	if got[0] != 443 || got[1] != 443 {
		t.Fatalf("UDP-only inbounds must keep 443: %v", got)
	}
	if got[2] == 443 {
		t.Fatalf("TCP mieru must be remapped off 443: %v", got)
	}
}

func TestCaddyEvictPort(t *testing.T) {
	if p := CaddyEvictPort(443); p != 12443 {
		t.Fatalf("evict 443 = %d, want 12443", p)
	}
	if p := CaddyEvictPort(80); p != 12080 {
		t.Fatalf("evict 80 = %d, want 12080", p)
	}
	if p := CaddyEvictPort(8444); p != 8444 {
		t.Fatalf("non-owned port must pass through: %d", p)
	}
}
