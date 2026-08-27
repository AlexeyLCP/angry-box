//go:build e2e

package chain_test

// e2e_routing_test.go — live verification of manual routing (LucX routing
// slice 1) on the n1 stand: ApplyMergedNode pushes the local .srs rule-set
// assets over SSH, emits the route section, and sing-box starts with them.
//
// Run from a machine with SSH access to n1:
//
//	AB_E2E_ROUTING=1 go test -tags e2e ./internal/chain/ -run TestE2E_ManualRouting -v -timeout 600s

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

func TestE2E_ManualRouting(t *testing.T) {
	if os.Getenv("AB_E2E_ROUTING") != "1" {
		t.Skip("set AB_E2E_ROUTING=1 to run the live n1 routing deploy test")
	}
	home, _ := os.UserHomeDir()
	key := filepath.Join(home, ".ssh", "id_ed25519")
	const addr = "144.31.224.212:22"

	store := chain.NewStore(filepath.Join(t.TempDir(), "store.json"))
	host := model.Host{ID: "n1", Addr: addr, User: "root", KeyPath: key}
	if err := store.SaveHost(&host); err != nil {
		t.Fatal(err)
	}

	user := &model.User{
		ID: "u1", Name: "e2e-routing", Active: true,
		MieruUsername: "e2e", MieruPassword: "routing-pass",
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}

	info := &model.NodeInfo{
		Host: host,
		Inbounds: []model.NodeInbound{{
			Protocol: "mieru", Port: 18964, Tag: "sa-0-mieru", MieruTransport: "TCP", ForUsers: []string{"u1"},
		}},
	}
	if err := store.SaveNodeInfo(info); err != nil {
		t.Fatal(err)
	}

	rules := []*model.RouteRule{
		{NodeID: "n1", Priority: 10, MatchType: "geosite", MatchValues: "telegram", Action: "reject", Enabled: true, Comment: "e2e"},
		{NodeID: "n1", Priority: 20, MatchType: "geoip", MatchValues: "ru", Action: "direct", Enabled: true, Comment: "e2e"},
		{NodeID: "n1", Priority: 30, MatchType: "domain_suffix", MatchValues: "example.org", Action: "direct", UserIDs: []string{"u1"}, Enabled: true, Comment: "e2e scoped"},
	}
	for _, r := range rules {
		if err := store.SaveRouteRule(r); err != nil {
			t.Fatal(err)
		}
	}

	applier := chain.NewApplier(factory.New(nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	report, mergeReport, err := applier.ApplyMergedNode(ctx, store, info)
	if err != nil {
		t.Fatalf("ApplyMergedNode: %v (merge warnings: %v)", err, mergeReport.Warnings)
	}
	if report == nil || len(report.Nodes) == 0 || !report.Nodes[0].Success {
		t.Fatalf("apply report: %+v", report)
	}

	client, err := sshclient.Connect(addr, "root", key)
	if err != nil {
		t.Fatalf("verify ssh: %v", err)
	}
	defer client.Close()

	out, err := client.Run("ls /etc/sing-box/rules/ && systemctl is-active sing-box")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, want := range []string{"telegram.srs", "geoip-ru.srs", "active"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify output missing %q:\n%s", want, out)
		}
	}
	cfg, err := client.Run("cat /etc/sing-box/config.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"rule_set"`, `"/etc/sing-box/rules/telegram.srs"`, `"reject"`, `"sniff"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %s", want)
		}
	}
	t.Log("manual routing deploy verified on n1")
}
