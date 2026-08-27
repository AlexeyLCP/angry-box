//go:build !nosqlite

package takeover

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	_ "modernc.org/sqlite"
)

// buildFixtureDB creates a minimal 3x-ui/lucx-ui schema with one vless+reality
// inbound (2 clients), one naive inbound, one mtproto inbound, traffic rows and
// a routing template. Returns the raw DB bytes.
func buildFixtureDB(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE inbounds (id INTEGER, remark TEXT, port INTEGER, protocol TEXT, enable BOOLEAN, settings TEXT, stream_settings TEXT)`,
		`CREATE TABLE client_traffics (id INTEGER, inbound_id INTEGER, enable BOOLEAN, email TEXT UNIQUE, up INTEGER, down INTEGER, total INTEGER, expiry_time INTEGER, last_sub_fetch INTEGER)`,
		`CREATE TABLE settings (id INTEGER, key TEXT, value TEXT)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}

	vlessSettings := `{"clients":[
		{"id":"aaaa-uuid-1","email":"alice","enable":true,"expiryTime":0,"totalGB":0,"tgId":111,"subId":"s1","flow":"xtls-rprx-vision"},
		{"id":"bbbb-uuid-2","email":"bob","enable":true,"expiryTime":32503680000000,"totalGB":1073741824,"tgId":0,"subId":"s2","flow":"xtls-rprx-vision"}
	],"decryption":"none"}`
	vlessStream := `{"network":"tcp","security":"reality","realitySettings":{"privateKey":"PRIVATE_KEY_B64","shortIds":["ab12","cd34"],"serverNames":["yahoo.com"],"target":"yahoo.com:443","settings":{"publicKey":"PUBLIC_KEY_B64"}}}`
	naiveSettings := `{"domain":"n.example.org","clients":[{"email":"alice","enable":true,"totalGB":0,"expiryTime":0}]}`
	mtSettings := `{"fakeTlsDomain":"www.cloudflare.com","clients":[{"secret":"ee00112233445566778899aabbccddeeff7777772e636c6f7564666c6172652e636f6d","email":"tg-user","enable":true}]}`

	ins := []struct{ remark string; port int; proto, settings, stream string }{
		{"reality-main", 443, "vless", vlessSettings, vlessStream},
		{"naive-in", 8443, "naive", naiveSettings, ``},
		{"mtproto-in", 444, "mtproto", mtSettings, ``},
		{"legacy-vmess", 8080, "vmess", `{"clients":[]}`, ``},
	}
	for i, in := range ins {
		if _, err := db.Exec(`INSERT INTO inbounds VALUES (?,?,?,?,1,?,?)`, i+1, in.remark, in.port, in.proto, in.settings, in.stream); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO client_traffics VALUES (1,1,1,'alice',1000,2000,0,0,1700000000000)`); err != nil {
		t.Fatal(err)
	}
	tmpl := `{"routing":{"rules":[
		{"type":"field","outboundTag":"direct","domain":["geosite:ru","domain:example.com"]},
		{"type":"field","outboundTag":"block","ip":["geoip:cn","10.0.0.0/8"]},
		{"type":"field","outboundTag":"proxy-out","domain":["skip.me"]}
	]}}`
	if _, err := db.Exec(`INSERT INTO settings VALUES (1,'xrayTemplateConfig',?)`, tmpl); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParsePanelDB(t *testing.T) {
	raw := buildFixtureDB(t)
	db, err := ParsePanelDB(raw)
	if err != nil {
		t.Fatalf("ParsePanelDB: %v", err)
	}
	if len(db.Inbounds) != 4 {
		t.Fatalf("inbounds = %d, want 4", len(db.Inbounds))
	}
	if len(db.Traffics) != 1 {
		t.Fatalf("traffics = %d, want 1", len(db.Traffics))
	}
	if !strings.Contains(db.RoutingJSON, "routing") {
		t.Fatalf("routing template missing: %s", db.RoutingJSON)
	}
}

func TestConvertPanelImport(t *testing.T) {
	raw := buildFixtureDB(t)
	db, err := ParsePanelDB(raw)
	if err != nil {
		t.Fatal(err)
	}
	imp := ConvertPanelImport("node-1", db)

	// Inbounds: reality + naive + mtproto imported; vmess skipped.
	if len(imp.NodeInbounds) != 3 {
		t.Fatalf("imported inbounds = %d, want 3 (vmess skipped): %+v", len(imp.NodeInbounds), imp.NodeInbounds)
	}
	var reality *int
	for i := range imp.NodeInbounds {
		if imp.NodeInbounds[i].Protocol == "vless-reality" {
			reality = &i
		}
	}
	if reality == nil {
		t.Fatal("no vless-reality inbound imported")
	}
	rib := imp.NodeInbounds[*reality]
	if rib.ServerPrivKey != "PRIVATE_KEY_B64" || rib.ShortID != "ab12" {
		t.Fatalf("reality key material wrong: %+v", rib)
	}
	if rib.UUID != "aaaa-uuid-1" {
		t.Fatalf("shared reality UUID = %q, want first client's", rib.UUID)
	}

	// Users: alice (tgId 111, vless + naive merged), bob, tg-user (mtproxy).
	if len(imp.Users) != 3 {
		t.Fatalf("users = %d, want 3: %+v", len(imp.Users), userNames(imp.Users))
	}
	var alice, bob, tgUser *int
	for i, u := range imp.Users {
		switch {
		case u.TelegramID == 111:
			alice = &i
		case u.Name == "bob":
			bob = &i
		case u.MTProxySecret != "":
			tgUser = &i
		}
	}
	if alice == nil || bob == nil || tgUser == nil {
		t.Fatalf("user set wrong: %+v", userNames(imp.Users))
	}
	au := imp.Users[*alice]
	if au.VLESSUUID != "aaaa-uuid-1" {
		t.Fatalf("alice VLESSUUID = %q", au.VLESSUUID)
	}
	if au.UsedTraffic != 3000 {
		t.Fatalf("alice UsedTraffic = %d, want 3000 (1000+2000)", au.UsedTraffic)
	}
	if au.FirstUseAt.IsZero() {
		t.Fatal("alice FirstUseAt must come from last_sub_fetch")
	}
	bu := imp.Users[*bob]
	if bu.ExpireStrategy != "fixed_date" || bu.ExpiresAt.IsZero() {
		t.Fatalf("bob must have a fixed expiry: %+v", bu.ExpiresAt)
	}
	if bu.DataLimit != 1073741824 {
		t.Fatalf("bob DataLimit = %d", bu.DataLimit)
	}
	mu := imp.Users[*tgUser]
	if mu.MTProxySecret != "00112233445566778899aabbccddeeff" {
		t.Fatalf("mtproxy secret = %q", mu.MTProxySecret)
	}
	if mu.MTProxyDomain != "www.cloudflare.com" {
		t.Fatalf("mtproxy domain = %q", mu.MTProxyDomain)
	}

	// Routing: direct rule (domain+geosite) + block rule (geoip+cidr); the
	// proxy-out rule is skipped.
	if len(imp.RouteRules) != 4 {
		t.Fatalf("route rules = %d, want 4 (domain, geosite, geoip, ip_cidr): %+v", len(imp.RouteRules), ruleSummary(imp.RouteRules))
	}
	for _, r := range imp.RouteRules {
		if r.NodeID != "node-1" || !r.Enabled {
			t.Fatalf("route rule malformed: %+v", r)
		}
	}

	// Report carries the skipped vmess note (nothing silent).
	joined := strings.Join(imp.Report, "\n")
	if !strings.Contains(joined, "vmess") || !strings.Contains(joined, "skipped") {
		t.Fatalf("report must mention the skipped vmess inbound: %s", joined)
	}
}

func userNames(users []*model.User) []string {
	var names []string
	for _, u := range users {
		names = append(names, u.Name)
	}
	return names
}

func ruleSummary(rules []*model.RouteRule) []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.MatchType+"->"+r.Action)
	}
	return out
}
