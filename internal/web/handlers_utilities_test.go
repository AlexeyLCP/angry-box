package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func utilityTestServer(t *testing.T, rules ...webFakeRule) (*testServer, *webFakeSSH) {
	t.Helper()
	ssh := newWebFakeSSH(rules...)
	ts := newTestServerWithConnector(t, &webFakeConnector{client: ssh})
	st := ts.srv.store()
	if err := st.SaveHost(&model.Host{ID: "n1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/key"}); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return ts, ssh
}

func TestHandler_Utilities_SaveDomain(t *testing.T) {
	ts, _ := utilityTestServer(t)
	st := ts.srv.store()

	form := url.Values{"tls_domain": {"https://Node1.Example.COM/"}}
	res := ts.post("/ui/nodes/n1/utilities/domain", form)
	if res.Code != http.StatusOK {
		t.Fatalf("save domain: code %d, body %s", res.Code, res.Body.String())
	}
	info, err := st.GetNodeInfo("n1")
	if err != nil || info.TLSDomain != "node1.example.com" {
		t.Fatalf("TLSDomain not normalized/saved: %+v, err %v", info, err)
	}

	// Invalid domain is rejected, store untouched.
	res = ts.post("/ui/nodes/n1/utilities/domain", url.Values{"tls_domain": {"not a domain"}})
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Invalid TLS domain") {
		t.Fatalf("invalid domain: code %d body %s", res.Code, res.Body.String())
	}
	info, _ = st.GetNodeInfo("n1")
	if info.TLSDomain != "node1.example.com" {
		t.Fatalf("invalid domain overwrote the store: %q", info.TLSDomain)
	}
}

func TestHandler_Utilities_PanelRequiresDomain(t *testing.T) {
	ts, _ := utilityTestServer(t)
	res := ts.get("/ui/nodes/n1/utilities")
	if res.Code != http.StatusOK {
		t.Fatalf("panel: code %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "Set a TLS domain") {
		t.Fatal("panel without domain must ask for one")
	}
}

func TestHandler_Utilities_InstallRefusedWithoutDomain(t *testing.T) {
	ts, ssh := utilityTestServer(t)
	res := ts.post("/ui/nodes/n1/utilities/install", url.Values{"name": {"all"}})
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "TLS domain") {
		t.Fatalf("install without domain must refuse: %s", res.Body.String())
	}
	if len(ssh.commands) != 0 {
		t.Fatalf("no SSH must happen without a domain, got %v", ssh.commands)
	}
}

// utilityInstallRules scripts a full happy-path install on the fake SSH.
func utilityInstallRules() []webFakeRule {
	return []webFakeRule{
		{substring: "/opt/angry-box/caddy/caddy version", out: "NOT_INSTALLED"},
		{substring: "uname -m", out: "x86_64\n"},
		{substring: "acme.sh && echo OK", out: "MISSING"},
		{substring: "ab-acme-install", out: ""},
		{substring: "caddy validate", out: ""},
		{substring: "is-active", out: "active"},
	}
}

func TestHandler_Utilities_InstallAll(t *testing.T) {
	t.Setenv("ANGRY_CADDY_CHECKSUM", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	t.Setenv("ANGRY_CADDY_URL", "https://example.com/caddy.tar.gz")
	t.Setenv("ANGRY_ACME_CHECKSUM", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	t.Setenv("ANGRY_ACME_URL", "https://example.com/acme.tar.gz")
	ts, ssh := utilityTestServer(t, utilityInstallRules()...)
	st := ts.srv.store()

	info, _ := st.GetNodeInfo("n1")
	if info == nil {
		info = &model.NodeInfo{}
	}
	info.ID = "n1"
	info.TLSDomain = "node1.example.com"
	info.Inbounds = []model.NodeInbound{{Protocol: "vless-reality", Port: 443, Source: "standalone"}}
	if err := st.SaveNodeInfo(info); err != nil {
		t.Fatal(err)
	}

	res := ts.post("/ui/nodes/n1/utilities/install", url.Values{"name": {"all"}})
	if res.Code != http.StatusOK {
		t.Fatalf("install: code %d body %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "alert-error") {
		t.Fatalf("install reported an error: %s", res.Body.String())
	}

	info, _ = st.GetNodeInfo("n1")
	for _, name := range model.AllUtilities() {
		if !info.UtilityInstalled(name) {
			t.Fatalf("utility %s not marked installed: %+v", name, info.Utilities)
		}
	}
	// The pushed Caddyfile must carry the reality default route on the
	// remapped internal port (reality inbound was on 443 -> 11000).
	caddyfile := ssh.uploadedContent("/tmp/ab-caddyfile.new")
	if caddyfile == "" {
		t.Fatal("caddyfile was never uploaded to the node")
	}
	if !strings.Contains(caddyfile, "11000") {
		t.Fatalf("expected remapped reality port 11000 in Caddyfile, got: %s", caddyfile)
	}
	if !strings.Contains(caddyfile, "tls_sni node1.example.com") {
		t.Fatalf("expected own-domain SNI route in Caddyfile, got: %s", caddyfile)
	}
}

func TestHandler_InboundCreate_GatedByUtilities(t *testing.T) {
	ts, _ := utilityTestServer(t)
	st := ts.srv.store()

	info := &model.NodeInfo{}
	info.ID = "n1"
	info.TLSDomain = "node1.example.com" // caddy mode, but no utilities installed
	if err := st.SaveNodeInfo(info); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"name": {"naive-test"}, "protocol": {"naive"}, "port": {"443"},
		"node_ids": {"n1"},
	}
	res := ts.post("/ui/inbounds", form)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), model.UtilityACME) {
		t.Fatalf("naive on a utility-less caddy node must be refused (409 + acme), got %d: %s", res.Code, res.Body.String())
	}

	// With the utilities installed the same form passes the gate.
	for _, name := range []string{model.UtilityCaddy, model.UtilityACME} {
		if err := st.SetUtilityState("n1", &model.UtilityState{Name: name, Installed: true}); err != nil {
			t.Fatal(err)
		}
	}
	res = ts.post("/ui/inbounds", form)
	if res.Code != http.StatusOK {
		t.Fatalf("naive with utilities must pass, got %d: %s", res.Code, res.Body.String())
	}
}

func TestBuildStandaloneLinkFor_CaddyFronted(t *testing.T) {
	node := &model.NodeInfo{
		Host:      model.Host{ID: "n1", Addr: "1.2.3.4:22"},
		TLSDomain: "node1.example.com",
		Utilities: []*model.UtilityState{{Name: model.UtilityCaddy, Installed: true}},
		Inbounds: []model.NodeInbound{
			{Protocol: "naive", Port: 443, Source: "standalone"},
			{Protocol: "naive", Port: 8443, Source: "standalone"},
			{Protocol: "mieru", Port: 9000, Source: "standalone"},
		},
	}
	u := &model.User{NaiveUsername: "al", NaivePassword: "pw", MieruUsername: "al", MieruPassword: "pw"}

	// First naive -> naive.<domain>, second -> naive-1.<domain>; SNI = host.
	l0 := buildStandaloneLinkFor(node, 0, u)
	if !strings.Contains(l0, "@naive.node1.example.com:443") || !strings.Contains(l0, "sni=naive.node1.example.com") {
		t.Fatalf("naive link not fronted: %s", l0)
	}
	l1 := buildStandaloneLinkFor(node, 1, u)
	if !strings.Contains(l1, "@naive-1.node1.example.com:443") {
		t.Fatalf("second naive link not fronted with -1 slug: %s", l1)
	}
	// mieru is not a TLS-utility protocol -> raw node IP.
	l2 := buildStandaloneLinkFor(node, 2, u)
	if !strings.Contains(l2, "1.2.3.4") {
		t.Fatalf("mieru must stay on the raw node address: %s", l2)
	}

	// Not caddy mode -> everything on the raw address.
	node.Utilities = nil
	if l := buildStandaloneLinkFor(node, 0, u); !strings.Contains(l, "1.2.3.4") {
		t.Fatalf("non-caddy node must use the raw address: %s", l)
	}
}

func TestPushNodeSubscriptions(t *testing.T) {
	ssh := newWebFakeSSH()
	ts := newTestServerWithConnector(t, &webFakeConnector{client: ssh})
	st := ts.srv.store()

	if err := st.SaveHost(&model.Host{ID: "n1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChain(&model.Chain{
		Name:         "sub-test",
		UserProtocol: model.UserProtocolAWG,
		Nodes:        []model.ChainNode{{ID: "n1", Addr: "1.2.3.4:22"}},
	}); err != nil {
		t.Fatal(err)
	}
	u := &model.User{
		ID: "u1", Name: "Alice", Active: true,
		Protocols: []string{"awg"}, ChainNames: []string{"sub-test"},
		SubscriptionToken: "tok-push",
	}
	chain.EnsureUserCreds(u)
	chain.EnsureUserAWGAddress(u, nil)
	if err := st.SaveUser(u); err != nil {
		t.Fatal(err)
	}

	info := &model.NodeInfo{}
	info.ID = "n1"
	info.Utilities = []*model.UtilityState{{Name: model.UtilitySub, Installed: true}}
	if err := st.SaveNodeInfo(info); err != nil {
		t.Fatal(err)
	}

	if err := ts.srv.PushNodeSubscriptions(context.Background(), "n1"); err != nil {
		t.Fatalf("PushNodeSubscriptions: %v", err)
	}

	// The full per-user file set must land in the node's sub dir.
	want := []string{"tok-push.raw", "tok-push.b64", "tok-push.clash.yaml", "tok-push.html"}
	for _, name := range want {
		found := false
		for _, up := range ssh.uploads {
			if strings.HasSuffix(up, "/"+name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("subscription file %s was not pushed (uploads: %v)", name, ssh.uploads)
		}
	}
	// Utility state stamped with the pushed user count + a revision.
	info, _ = st.GetNodeInfo("n1")
	sub := model.FindUtility(info.Utilities, model.UtilitySub)
	if sub == nil || !strings.Contains(sub.Version, "1 users") {
		t.Fatalf("sub utility state not stamped: %+v", sub)
	}

	// A node WITHOUT the sub utility is a no-op (no SSH).
	ssh2 := newWebFakeSSH()
	ts2 := newTestServerWithConnector(t, &webFakeConnector{client: ssh2})
	st2 := ts2.srv.store()
	_ = st2.SaveHost(&model.Host{ID: "n2", Addr: "9.9.9.9:22", User: "root", KeyPath: "/k"})
	ni := &model.NodeInfo{}
	ni.ID = "n2"
	_ = st2.SaveNodeInfo(ni)
	if err := ts2.srv.PushNodeSubscriptions(context.Background(), "n2"); err != nil {
		t.Fatalf("no-op push errored: %v", err)
	}
	if len(ssh2.uploads) != 0 {
		t.Fatalf("node without sub utility must not receive uploads: %v", ssh2.uploads)
	}
}

func TestBulkCreateUsers(t *testing.T) {
	ts := newTestServer(t)
	st := ts.srv.store()
	if err := st.SaveChain(&model.Chain{
		Name:         "bulk-chain",
		UserProtocol: model.UserProtocolAWG,
		Nodes:        []model.ChainNode{{ID: "n1", Addr: "1.2.3.4:22"}},
	}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"count":         {"5"},
		"name_prefix":   {"client"},
		"expires_days":  {"30"},
		"data_limit_gb": {"10"},
		"chain_names":   {"bulk-chain"},
	}
	res := ts.post("/ui/users/bulk", form)
	if res.Code != http.StatusOK {
		t.Fatalf("bulk create: code %d body %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "5") {
		t.Fatalf("result must mention 5 created users: %s", res.Body.String())
	}

	users, _ := st.ListUsers()
	if len(users) != 5 {
		t.Fatalf("created %d users, want 5", len(users))
	}
	for _, u := range users {
		if !strings.HasPrefix(u.Name, "client-") {
			t.Errorf("user %q missing prefix", u.Name)
		}
		if u.SubscriptionToken == "" {
			t.Errorf("user %q has no subscription token", u.Name)
		}
		if u.DataLimit != 10*1024*1024*1024 {
			t.Errorf("user %q data limit = %d, want 10 GiB", u.Name, u.DataLimit)
		}
		if u.ExpiresAt.IsZero() {
			t.Errorf("user %q must have an expiry", u.Name)
		}
		if len(u.ChainNames) != 1 || u.ChainNames[0] != "bulk-chain" {
			t.Errorf("user %q chains = %v", u.Name, u.ChainNames)
		}
	}

	// Invalid count is refused.
	res = ts.post("/ui/users/bulk", url.Values{"count": {"0"}, "name_prefix": {"x"}})
	if !strings.Contains(res.Body.String(), "1..100") {
		t.Fatalf("count 0 must be refused: %s", res.Body.String())
	}
}
