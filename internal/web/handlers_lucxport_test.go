package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/awg/vpnuri"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestBuildClientURI_NaiveMieru(t *testing.T) {
	u := &model.User{NaiveUsername: "alice", NaivePassword: "p1", MieruUsername: "alice", MieruPassword: "p2"}
	n := buildClientURI("naive", "1.2.3.4", 443, "", "", "", "", "n1", u, "", false)
	if !strings.HasPrefix(n, "naive+https://alice:p1@1.2.3.4:443") {
		t.Errorf("naive: %s", n)
	}
	m := buildClientURI("mieru", "1.2.3.4", 8964, "", "", "", "", "n1", u, "", false)
	if !strings.HasPrefix(m, "mierus://alice:p2@1.2.3.4:8964") {
		t.Errorf("mieru: %s", m)
	}
	u.TrustTunnelUsername, u.TrustTunnelPassword = "alice", "p3"
	tt := buildClientURI("trusttunnel", "1.2.3.4", 443, "", "", "", "", "n1", u, "", false)
	if !strings.HasPrefix(tt, "tt://alice:p3@1.2.3.4:443") {
		t.Errorf("trusttunnel: %s", tt)
	}
}

func TestHandler_CreateInbound_NaiveMieru(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	w := ts.post("/ui/inbounds", url.Values{
		"name": {"naive1"}, "protocol": {"naive"}, "port": {"443"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusConflict)
	w = ts.post("/ui/inbounds", url.Values{
		"name": {"mieru1"}, "protocol": {"mieru"}, "port": {"8964"}, "mieru_transport": {"UDP"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusOK)
	w = ts.post("/ui/inbounds", url.Values{
		"name": {"tt1"}, "protocol": {"trusttunnel"}, "port": {"8443"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusConflict)
	st := ts.srv.store()
	profs, _ := st.ListInboundProfiles()
	got := map[string]string{}
	for _, p := range profs {
		got[p.Protocol] = p.MieruTransport
	}
	if _, ok := got["naive"]; ok {
		t.Fatal("naive without TLS domain must not be created")
	}
	if got["mieru"] != "UDP" {
		t.Fatalf("mieru transport = %q", got["mieru"])
	}
	if _, ok := got["trusttunnel"]; ok {
		t.Fatal("trusttunnel without TLS domain must not be created")
	}
	ib := st.ProfileInboundOn("n1", profs[0].ID)
	if ib == nil {
		t.Fatal("no materialized inbound")
	}
}

func TestHandler_CreateInbound_NaiveWithTLSDomain(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := ts.srv.store()
	info, err := st.GetNodeInfo("n1")
	if err != nil {
		t.Fatal(err)
	}
	info.TLSDomain = "n.example.com"
	info.Utilities = []*model.UtilityState{
		{Name: model.UtilityCaddy, Installed: true},
		{Name: model.UtilityACME, Installed: true},
	}
	if err := st.SaveNodeInfo(info); err != nil {
		t.Fatal(err)
	}
	w := ts.post("/ui/inbounds", url.Values{
		"name": {"naive1"}, "protocol": {"naive"}, "port": {"443"}, "node_ids": {"n1"},
	})
	ts.assertStatus(w, http.StatusOK)
	profs, _ := st.ListInboundProfiles()
	found := false
	for _, p := range profs {
		if p.Protocol == "naive" {
			found = true
		}
	}
	if !found {
		t.Fatal("naive with TLS domain must be created")
	}
}

func TestSub_FormatVPNAndClash(t *testing.T) {
	ts := newTestServer(t)
	tok := seedSubUser(t, ts)
	st := chain.NewStore(ts.storePath)
	u, err := st.GetUserBySubscriptionToken(tok)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	u.AWGPrivateKey = "clientpriv"
	u.AWGPublicKey = "clientpub"
	u.AWGAddress = "10.8.0.2/32"
	u.AWGPresharedKey = "pskvalue=="
	if err := st.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	ni := &model.NodeInfo{
		Host: model.Host{ID: "n1", Addr: "1.2.3.4:22"},
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", Port: 51820, Tag: "awg1", ServerPubKey: "serverpub", ForUsers: []string{"u1"}, AWGClientPriv: "clientpriv"},
		},
	}
	if err := st.SaveNodeInfo(ni); err != nil {
		t.Fatal(err)
	}

	w := ts.getWithUA("/sub/"+tok+"?format=vpn", "curl/8.0")
	if w.Code != http.StatusOK {
		t.Fatalf("vpn status %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "vpn://") {
		t.Errorf("vpn body: %s", w.Body.String())
	}
	payload, err := vpnuri.Decode(strings.TrimSpace(w.Body.String()))
	if err != nil {
		t.Fatalf("decode vpn: %v", err)
	}
	conf, err := vpnuri.ConfFromPayload(payload)
	if err != nil {
		t.Fatalf("conf: %v", err)
	}
	if !strings.Contains(conf, "[Interface]") {
		t.Errorf("decoded conf: %s", conf)
	}

	w = ts.getWithUA("/sub/"+tok+"?format=clash", "curl/8.0")
	if w.Code != http.StatusOK {
		t.Fatalf("clash status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "type: wireguard") || !strings.Contains(body, "amnezia-wg-option") {
		t.Errorf("clash body: %s", body)
	}
}

func TestBuildAWGClientConf_PSK(t *testing.T) {
	conf := buildAWGClientConf("1.2.3.4", 51820, "priv", "pub", "", "", "10.8.0.2/32", nil, "psk123")
	if !strings.Contains(conf, "PresharedKey = psk123") {
		t.Errorf("missing PSK: %s", conf)
	}
}
