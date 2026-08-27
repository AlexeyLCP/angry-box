package chain

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestRemapInboundPorts(t *testing.T) {
	inbounds := []model.NodeInbound{
		{Protocol: "vless-reality", Port: 443}, // owned -> remap
		{Protocol: "naive", Port: 443},         // owned -> remap (distinct!)
		{Protocol: "mtproxy", Port: 9000},      // free -> keep
		{Protocol: "mieru", Port: 80},          // owned -> remap
	}
	got := RemapInboundPorts(inbounds)
	if got[0] == 443 || got[1] == 443 || got[3] == 80 {
		t.Fatalf("owned ports not remapped: %v", got)
	}
	if got[0] == got[1] || got[0] == got[3] || got[1] == got[3] {
		t.Fatalf("remapped ports must be distinct: %v", got)
	}
	if got[2] != 9000 {
		t.Fatalf("free port changed: %v", got)
	}
	// Deterministic.
	if got2 := RemapInboundPorts(inbounds); got2[0] != got[0] || got2[1] != got[1] {
		t.Fatalf("remap not deterministic: %v vs %v", got, got2)
	}

	// An inbound natively on an internal remap port must not be collided with.
	inbounds2 := []model.NodeInbound{
		{Protocol: "mieru", Port: 11000},       // native on the remap range
		{Protocol: "vless-reality", Port: 443}, // must skip 11000
	}
	got2 := RemapInboundPorts(inbounds2)
	if got2[0] != 11000 {
		t.Fatalf("native port moved: %v", got2)
	}
	if got2[1] == 11000 {
		t.Fatalf("remap collided with a native port: %v", got2)
	}
}

func caddyTestNode() *model.NodeInfo {
	return &model.NodeInfo{
		TLSDomain: "node1.example.com",
		Inbounds: []model.NodeInbound{
			{Protocol: "vless-reality", Port: 443, Source: "standalone"},
			{Protocol: "naive", Port: 443, Source: "standalone"},
			{Protocol: "naive", Port: 8443, Source: "standalone"},
			{Protocol: "mtproxy", Port: 8444, Source: "standalone"},
			{Protocol: "vless-reality", Port: 9443, Source: "chain:c1"}, // skipped (chain-sourced)
		},
	}
}

func TestBuildCaddyPlan(t *testing.T) {
	info := caddyTestNode()
	plan, err := BuildCaddyPlan(info, 42)
	if err != nil {
		t.Fatalf("BuildCaddyPlan: %v", err)
	}
	if plan.Domain != "node1.example.com" || plan.Revision != 42 {
		t.Fatalf("plan fields: %+v", plan)
	}
	if plan.RealityPort == 0 {
		t.Fatal("reality default target not set")
	}
	// Two standalone naive inbounds -> two SNI routes with distinct labels.
	if len(plan.SNIRoutes) != 2 {
		t.Fatalf("SNIRoutes = %+v, want 2 (naive, naive-1)", plan.SNIRoutes)
	}
	if plan.SNIRoutes[0].Host != "naive.node1.example.com" {
		t.Fatalf("first SNI host = %q", plan.SNIRoutes[0].Host)
	}
	if plan.SNIRoutes[1].Host != "naive-1.node1.example.com" {
		t.Fatalf("second SNI host = %q", plan.SNIRoutes[1].Host)
	}
	sans := plan.CaddySANs()
	if sans[0] != "node1.example.com" || sans[1] != "panel.node1.example.com" {
		t.Fatalf("SANs head: %v", sans)
	}
	if len(sans) != 4 {
		t.Fatalf("SANs = %v, want 4 (main, panel, 2x naive)", sans)
	}

	if _, err := BuildCaddyPlan(&model.NodeInfo{}, 1); err == nil {
		t.Fatal("empty TLSDomain must fail the plan")
	}
}

func TestRenderCaddyfile(t *testing.T) {
	plan, err := BuildCaddyPlan(caddyTestNode(), 7)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	out, err := RenderCaddyfile(plan)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"store rev 7",
		"admin off",
		"layer4",
		"@own tls_sni node1.example.com panel.node1.example.com",
		"@sni0 tls_sni naive.node1.example.com",
		"@sni1 tls_sni naive-1.node1.example.com",
		"tls_sni", // reality default = plain route (last)
		"https://127.0.0.1:8443",
		"http://127.0.0.1:8080",
		"/etc/angry-box-certs/node1.example.com/fullchain.pem",
		"@panel host panel.node1.example.com",
		"reverse_proxy 127.0.0.1:8900",
		"handle_path /sub/*",
		"try_files {path} {path}.b64",
		"/.well-known/acme-challenge/*",
		"redir https://node1.example.com{uri} permanent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Caddyfile missing %q", want)
		}
	}
	// The default (reality) route must be present exactly once on :443 (the
	// :80 layer4 block contributes the other bare "route {" occurrence).
	if n := strings.Count(out, "\t\t\troute {\n"); n != 2 {
		t.Fatalf("bare routes = %d, want 2 (:80 + reality default)", n)
	}
	// No reality port -> only the :80 bare route remains.
	plan.RealityPort = 0
	out2, _ := RenderCaddyfile(plan)
	if n := strings.Count(out2, "\t\t\troute {\n"); n != 1 {
		t.Fatalf("bare routes without reality = %d, want 1 (:80 only)", n)
	}
	if _, err := RenderCaddyfile(CaddyPlan{}); err == nil {
		t.Fatal("empty domain must fail the render")
	}
}

func TestValidateUtilityDeps(t *testing.T) {
	// Not caddy mode — everything allowed.
	if err := ValidateUtilityDeps(&model.NodeInfo{}, "naive", 443); err != nil {
		t.Fatalf("non-caddy node must pass: %v", err)
	}

	caddy := &model.NodeInfo{
		TLSDomain: "n.example.com",
		Utilities: []*model.UtilityState{{Name: model.UtilityCaddy, Installed: true}},
	}
	// naive needs caddy + acme; acme missing.
	err := ValidateUtilityDeps(caddy, "naive", 443)
	if err == nil || !strings.Contains(err.Error(), model.UtilityACME) {
		t.Fatalf("expected missing-acme error, got %v", err)
	}
	caddy.Utilities = append(caddy.Utilities, &model.UtilityState{Name: model.UtilityACME, Installed: true})
	if err := ValidateUtilityDeps(caddy, "naive", 443); err != nil {
		t.Fatalf("all deps installed must pass: %v", err)
	}
	// Protocols without requirements pass in caddy mode.
	if err := ValidateUtilityDeps(caddy, "mieru", 8443); err != nil {
		t.Fatalf("mieru needs no utilities: %v", err)
	}
	// mtproxy on a caddy-owned port is a hard refusal.
	if err := ValidateUtilityDeps(caddy, "mtproxy", 443); err == nil {
		t.Fatal("mtproxy on 443 must be refused in caddy mode")
	}
	if err := ValidateUtilityDeps(caddy, "mtproxy", 8444); err != nil {
		t.Fatalf("mtproxy on a free port must pass: %v", err)
	}
}

func TestMissingUtilities(t *testing.T) {
	caddy := &model.NodeInfo{TLSDomain: "n.example.com"}
	got := MissingUtilities(caddy, "trusttunnel")
	if len(got) != 2 {
		t.Fatalf("MissingUtilities = %v, want caddy+acme", got)
	}
	if got := MissingUtilities(&model.NodeInfo{}, "trusttunnel"); got != nil {
		t.Fatalf("non-caddy node must report nothing: %v", got)
	}
}

func TestIssueNodeCert_RejectsShellDomains(t *testing.T) {
	// Domain strings are interpolated into a root shell — refuse metachars.
	if err := IssueNodeCert(t.Context(), nil, false, "ok.example.com", []string{"evil'; rm -rf /"}, nil); err == nil {
		t.Fatal("shell-metachar domain must be rejected")
	}
}
