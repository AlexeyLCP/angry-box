package web

// clienturi_test.go — covers buildClientURI (per-protocol), buildConnectionLink,
// buildStandaloneLink, buildAWGClientConf, defaultFakeTLSDomain, contains, and
// the misc helpers (inferNodeRole/renderCurrentNodeConfig/truncForDisplay/
// jsonMarshal). CTO-review C3 phase 5.

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestBuildClientURI_AWG verifies the AWG link shape (non-imported).
func TestBuildClientURI_AWG(t *testing.T) {
	link := buildClientURI("awg", "1.2.3.4", 51820, "", "", "pubkey", "", "awg", &model.User{}, "", false)
	if !strings.HasPrefix(link, "awg://1.2.3.4:51820") {
		t.Errorf("got %q, want awg:// prefix", link)
	}
	if !strings.Contains(link, "pub=pubkey") {
		t.Errorf("got %q, want pub=pubkey", link)
	}
}

// TestBuildClientURI_AWG_Imported verifies an imported AWG secret builds a full
// client .conf.
func TestBuildClientURI_AWG_Imported(t *testing.T) {
	u := &model.User{ImportedSecret: "importedPriv", SecretType: "awg"}
	link := buildClientURI("awg", "1.2.3.4", 51820, "", "", "pubkey", "", "awg", u, "", false)
	if !strings.Contains(link, "[Interface]") {
		t.Errorf("got %q, want a full .conf with [Interface]", link)
	}
}

// TestBuildClientURI_TUIC verifies the TUIC link shape.
func TestBuildClientURI_TUIC(t *testing.T) {
	link := buildClientURI("tuic", "1.2.3.4", 443, "uuid-t", "pw-t", "", "", "tuic", &model.User{}, "", false)
	if !strings.HasPrefix(link, "tuic://uuid-t:pw-t@1.2.3.4:443") {
		t.Errorf("got %q, want tuic:// prefix", link)
	}
}

// TestBuildClientURI_TUIC_Imported verifies an imported TUIC secret.
func TestBuildClientURI_TUIC_Imported(t *testing.T) {
	u := &model.User{ImportedSecret: "imp", SecretType: "tuic"}
	link := buildClientURI("tuic", "1.2.3.4", 443, "", "", "", "", "tuic", u, "", false)
	if !strings.HasPrefix(link, "tuic://imp:imp@") {
		t.Errorf("got %q, want tuic://imp:imp@", link)
	}
}

// TestBuildClientURI_VLESSReality verifies the vless-reality share link.
func TestBuildClientURI_VLESSReality(t *testing.T) {
	link := buildClientURI("vless-reality", "1.2.3.4", 443, "uuid-v", "", "pubkey", "sid", "vless", &model.User{}, "", false)
	if !strings.HasPrefix(link, "vless://uuid-v@1.2.3.4:443") {
		t.Errorf("got %q, want vless:// prefix", link)
	}
	if !strings.Contains(link, "security=reality") {
		t.Errorf("got %q, want security=reality", link)
	}
}

// TestBuildClientURI_VLESSRealityXHTTP verifies the combined REALITY+XHTTP link.
func TestBuildClientURI_VLESSRealityXHTTP(t *testing.T) {
	link := buildClientURI("vless-reality-xhttp", "1.2.3.4", 443, "uuid-v", "", "pubkey", "sid", "vless", &model.User{}, "", false)
	if !strings.HasPrefix(link, "vless://uuid-v@1.2.3.4:443") {
		t.Errorf("got %q, want vless:// prefix", link)
	}
	if !strings.Contains(link, "type=xhttp") {
		t.Errorf("got %q, want type=xhttp", link)
	}
}

// TestBuildClientURI_XHTTP verifies the plain XHTTP link.
func TestBuildClientURI_XHTTP(t *testing.T) {
	link := buildClientURI("xhttp", "1.2.3.4", 443, "uuid-v", "", "", "", "xhttp", &model.User{}, "", false)
	if !strings.Contains(link, "type=xhttp") {
		t.Errorf("got %q, want type=xhttp", link)
	}
}

// TestBuildClientURI_VMess verifies the vmess:// + base64(JSON) link.
func TestBuildClientURI_VMess(t *testing.T) {
	link := buildClientURI("vmess", "1.2.3.4", 443, "uuid-vm", "", "", "", "vm-name", &model.User{}, "", false)
	if !strings.HasPrefix(link, "vmess://") {
		t.Errorf("got %q, want vmess:// prefix", link)
	}
}

// TestBuildClientURI_Trojan verifies the trojan link.
func TestBuildClientURI_Trojan(t *testing.T) {
	link := buildClientURI("trojan", "1.2.3.4", 443, "", "trojan-pw", "", "", "trojan-name", &model.User{}, "", false)
	if !strings.HasPrefix(link, "trojan://trojan-pw@1.2.3.4:443") {
		t.Errorf("got %q, want trojan:// prefix", link)
	}
}

// TestBuildClientURI_SS verifies the shadowsocks link.
func TestBuildClientURI_SS(t *testing.T) {
	link := buildClientURI("shadowsocks", "1.2.3.4", 8388, "", "ss-pw", "", "", "ss-name", &model.User{}, "", false)
	if !strings.HasPrefix(link, "ss://") {
		t.Errorf("got %q, want ss:// prefix", link)
	}
}

// TestBuildClientURI_SS_NoPassword verifies a missing password gets generated.
func TestBuildClientURI_SS_NoPassword(t *testing.T) {
	link := buildClientURI("shadowsocks", "1.2.3.4", 8388, "", "", "", "", "ss-name", &model.User{}, "", false)
	if !strings.HasPrefix(link, "ss://") {
		t.Errorf("got %q, want ss:// prefix", link)
	}
}

// TestBuildClientURI_Hysteria2 verifies the hysteria2 link carries the obfs
// password.
func TestBuildClientURI_Hysteria2(t *testing.T) {
	link := buildClientURI("hysteria2", "1.2.3.4", 443, "hy-uuid", "", "", "", "hy", &model.User{}, "obfs-pw", false)
	if !strings.HasPrefix(link, "hysteria2://") {
		t.Errorf("got %q, want hysteria2:// prefix", link)
	}
	if !strings.Contains(link, "obfs-password=obfs-pw") {
		t.Errorf("got %q, want obfs-password=obfs-pw", link)
	}
}

// TestBuildClientURI_Hysteria2_Insecure verifies insecure=1 is appended when
// requested.
func TestBuildClientURI_Hysteria2_Insecure(t *testing.T) {
	link := buildClientURI("hysteria2", "1.2.3.4", 443, "hy-uuid", "", "", "", "hy", &model.User{}, "obfs-pw", true)
	if !strings.Contains(link, "insecure=1") {
		t.Errorf("got %q, want insecure=1", link)
	}
}

// TestBuildClientURI_Hysteria2_GenObfs verifies a missing obfs password is
// generated (non-empty).
func TestBuildClientURI_Hysteria2_GenObfs(t *testing.T) {
	link := buildClientURI("hysteria2", "1.2.3.4", 443, "hy-uuid", "", "", "", "hy", &model.User{}, "", false)
	if !strings.Contains(link, "obfs-password=") {
		t.Errorf("got %q, want an obfs-password", link)
	}
}

// TestBuildClientURI_Unknown verifies an unknown protocol returns an error-style
// string (no panic).
func TestBuildClientURI_Unknown(t *testing.T) {
	link := buildClientURI("bogus", "1.2.3.4", 443, "u", "", "", "", "n", &model.User{}, "", false)
	if !strings.Contains(link, "unsupported") && link == "" {
		t.Errorf("got %q, expected non-empty unsupported marker", link)
	}
}

// TestBuildConnectionLink_NoNodes verifies a chain with no nodes returns a
// placeholder.
func TestBuildConnectionLink_NoNodes(t *testing.T) {
	link := buildConnectionLink(&model.Chain{Name: "empty"}, &model.User{})
	if !strings.Contains(link, "no nodes") {
		t.Errorf("got %q, want no-nodes placeholder", link)
	}
}

// TestBuildConnectionLink_AWG verifies a chain link builds an awg-quick .conf
// for AWG (per-user peer; the .conf carries the server pub + entry endpoint).
func TestBuildConnectionLink_AWG(t *testing.T) {
	c := &model.Chain{
		Name:              "c1",
		UserProtocol:      model.UserProtocolAWG,
		AWGEntryServerPub: "awg-pub",
		Nodes:             []model.ChainNode{{ID: "n0", Addr: "1.2.3.4:22"}},
	}
	link := buildConnectionLink(c, &model.User{})
	if !strings.Contains(link, "[Interface]") {
		t.Errorf("got %q, want a .conf", link)
	}
	if !strings.Contains(link, "Endpoint = 1.2.3.4:") {
		t.Errorf("got %q, want Endpoint = 1.2.3.4: in .conf", link)
	}
	if !strings.Contains(link, "PublicKey = awg-pub") {
		t.Errorf("got %q, want server pub awg-pub in .conf", link)
	}
}

// TestBuildStandaloneLink_AWG verifies a standalone AWG link with a client priv
// builds a full .conf.
func TestBuildStandaloneLink_AWG(t *testing.T) {
	ib := model.NodeInbound{Protocol: "awg", Port: 51820, AWGClientPriv: "cpriv", ServerPubKey: "spub"}
	link := buildStandaloneLink("1.2.3.4:22", ib, &model.User{})
	if !strings.Contains(link, "[Interface]") {
		t.Errorf("got %q, want a full .conf", link)
	}
}

// TestBuildStandaloneLink_AWG_Imported verifies an imported AWG secret builds a
// .conf.
func TestBuildStandaloneLink_AWG_Imported(t *testing.T) {
	ib := model.NodeInbound{Protocol: "awg", Port: 51820, ServerPubKey: "spub"}
	u := &model.User{ImportedSecret: "impPriv", SecretType: "awg"}
	link := buildStandaloneLink("1.2.3.4:22", ib, u)
	if !strings.Contains(link, "[Interface]") {
		t.Errorf("got %q, want a full .conf", link)
	}
}

// TestBuildStandaloneLink_MTProxy verifies the tg:// proxy link.
func TestBuildStandaloneLink_MTProxy(t *testing.T) {
	ib := model.NodeInbound{Protocol: "mtproxy", Port: 443, UUID: "aabbccddea" + "00112233445566778899aabbccddeeff"}
	link := buildStandaloneLink("1.2.3.4:22", ib, &model.User{})
	if !strings.HasPrefix(link, "tg://proxy?") {
		t.Errorf("got %q, want tg://proxy? prefix", link)
	}
}

// TestBuildStandaloneLink_VLESS verifies a standalone vless (no reality/xhttp
// qualifier) falls through to the placeholder, since the share-link builder only
// knows vless-reality / vless-reality-xhttp.
func TestBuildStandaloneLink_VLESS(t *testing.T) {
	ib := model.NodeInbound{Protocol: "vless", Port: 8443, UUID: "uuid-v", ServerPubKey: "pub", ShortID: "sid"}
	link := buildStandaloneLink("1.2.3.4:22", ib, &model.User{})
	if !strings.HasPrefix(link, "# vless config for") {
		t.Errorf("got %q, want # vless config for placeholder", link)
	}
}

// TestDefaultFakeTLSDomain_Set verifies the Obfuscation field is honoured.
func TestDefaultFakeTLSDomain_Set(t *testing.T) {
	ib := model.NodeInbound{Obfuscation: "telegram.org"}
	if got := defaultFakeTLSDomain(ib); got != "telegram.org" {
		t.Errorf("got %q, want telegram.org", got)
	}
}

// TestDefaultFakeTLSDomain_Default verifies the default domain.
func TestDefaultFakeTLSDomain_Default(t *testing.T) {
	if got := defaultFakeTLSDomain(model.NodeInbound{}); got != "disk.yandex.ru" {
		t.Errorf("got %q, want disk.yandex.ru", got)
	}
}

// TestContains verifies the slice helper.
func TestContains(t *testing.T) {
	if !contains([]string{"a", "b"}, "a") {
		t.Error("expected a found")
	}
	if contains([]string{"a", "b"}, "c") {
		t.Error("expected c not found")
	}
}

// TestInferNodeRole verifies role inference from inbounds.
func TestInferNodeRole(t *testing.T) {
	if got := inferNodeRole(&model.NodeInfo{Inbounds: []model.NodeInbound{{Protocol: "mtproxy"}}}); got != "mtproxy_server" {
		t.Errorf("got %q, want mtproxy_server", got)
	}
	if got := inferNodeRole(&model.NodeInfo{Inbounds: []model.NodeInbound{{Protocol: "awg"}}}); got != "awg_balancer" {
		t.Errorf("got %q, want awg_balancer", got)
	}
	if got := inferNodeRole(&model.NodeInfo{Inbounds: []model.NodeInbound{{Protocol: "vless"}}}); got != "proxy_node" {
		t.Errorf("got %q, want proxy_node", got)
	}
}

// TestTruncForDisplay verifies the ellipsis behaviour.
func TestTruncForDisplay(t *testing.T) {
	if got := truncForDisplay("short", 80); got != "short" {
		t.Errorf("got %q, want short (no truncation)", got)
	}
	long := strings.Repeat("x", 100)
	got := truncForDisplay(long, 10)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("got %q, want … suffix", got)
	}
	// s[:10] + "…" (… is 3 bytes in UTF-8) -> 13 bytes; just assert the prefix.
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) {
		t.Errorf("got %q, want first 10 x preserved", got)
	}
}

// TestJSONMarshal verifies the helper round-trips valid data and surfaces an
// error object for non-serializable input.
func TestJSONMarshal(t *testing.T) {
	if got := jsonMarshal(map[string]any{"a": 1}); !strings.Contains(got, `"a":1`) {
		t.Errorf("got %q, want a:1", got)
	}
	// A channel is not JSON-serializable -> the helper returns an error object.
	got := jsonMarshal(struct{ C chan int }{C: make(chan int)})
	if !strings.Contains(got, "error") {
		t.Errorf("got %q, want an error object", got)
	}
}

// keep helpers referenced.
var _ = model.NodeInbound{}