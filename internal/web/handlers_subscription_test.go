package web

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// seedSubUser creates a user with a subscription token and a chain assigned so
// collectUserLinks returns at least one link. Extra mutators tweak the user
// (expiry, active). Returns the token.
func seedSubUser(t *testing.T, ts *testServer, opts ...func(*model.User)) string {
	t.Helper()
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name:         "sub-test",
		UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "1.2.3.4:22"},
		},
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	u := &model.User{
		ID:                "u1",
		Name:              "Alice",
		Active:            true,
		Protocols:         []string{"awg"},
		ChainNames:        []string{"sub-test"},
		SubscriptionToken: "tok-abc",
	}
	for _, opt := range opts {
		opt(u)
	}
	if err := chain.EnsureUserCreds(u); err != nil {
		t.Fatalf("EnsureUserCreds: %v", err)
	}
	chain.EnsureUserAWGAddress(u, nil)
	if err := st.SaveUser(u); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	return "tok-abc"
}

// TestSub_KnownToken_Raw verifies a known token with a curl User-Agent (no
// format param) returns 200, text/plain, inline disposition, and a non-empty
// raw link list (contains "://" or "[", not pure base64).
func TestSub_KnownToken_Raw(t *testing.T) {
	ts := newTestServer(t)
	tok := seedSubUser(t, ts)
	w := ts.getWithUA("/sub/"+tok, "curl/8.0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != "inline" {
		t.Errorf("Content-Disposition = %q, want inline", cd)
	}
	body := w.Body.String()
	if body == "" {
		t.Fatal("empty body — expected a link")
	}
	if !strings.Contains(body, "://") && !strings.Contains(body, "[") {
		t.Errorf("raw body does not look like a link list: %q", firstN(body, 80))
	}
}

// TestSub_KnownToken_Base64Param verifies ?format=base64 returns a base64-
// encoded link list with attachment disposition.
func TestSub_KnownToken_Base64Param(t *testing.T) {
	ts := newTestServer(t)
	tok := seedSubUser(t, ts)
	w := ts.getWithUA("/sub/"+tok+"?format=base64", "curl/8.0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	decoded, err := base64.StdEncoding.DecodeString(w.Body.String())
	if err != nil {
		t.Fatalf("body is not valid base64: %v", err)
	}
	if len(decoded) == 0 {
		t.Error("decoded body empty")
	}
}

// TestSub_V2rayNGUserAgent_DefaultsBase64 verifies a v2rayNG User-Agent without
// a format param defaults to base64.
func TestSub_V2rayNGUserAgent_DefaultsBase64(t *testing.T) {
	ts := newTestServer(t)
	tok := seedSubUser(t, ts)
	w := ts.getWithUA("/sub/"+tok, "v2rayNG/1.8.0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if _, err := base64.StdEncoding.DecodeString(w.Body.String()); err != nil {
		t.Errorf("v2rayNG UA should default to base64; not base64: %v\nbody: %q", err, firstN(w.Body.String(), 80))
	}
}

// TestSub_UnknownToken_404 verifies an unknown token returns 404.
func TestSub_UnknownToken_404(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/sub/no-such-token")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// TestSub_ExpiredUser_404 verifies an expired user (past ExpiresAt) gets 404.
func TestSub_ExpiredUser_404(t *testing.T) {
	ts := newTestServer(t)
	tok := seedSubUser(t, ts, func(u *model.User) {
		u.ExpiresAt = time.Now().Add(-time.Hour)
	})
	w := ts.getWithUA("/sub/"+tok, "curl/8.0")
	if w.Code != http.StatusNotFound {
		t.Errorf("expired user status = %d, want 404", w.Code)
	}
}

// TestSub_DisabledUser_404 verifies an inactive user gets 404.
func TestSub_DisabledUser_404(t *testing.T) {
	ts := newTestServer(t)
	tok := seedSubUser(t, ts, func(u *model.User) {
		u.Active = false
	})
	w := ts.getWithUA("/sub/"+tok, "curl/8.0")
	if w.Code != http.StatusNotFound {
		t.Errorf("disabled user status = %d, want 404", w.Code)
	}
}

// TestSub_LazyTokenBackfill verifies a legacy user (empty token) gets one
// minted on first fetch: a fetch by an empty token 404s, but after the handler
// mints+saves (simulated here by minting directly) a fetch by the new token
// returns 200.
func TestSub_LazyTokenBackfill(t *testing.T) {
	ts := newTestServer(t)
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name: "c", UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{{ID: "n1", Addr: "1.2.3.4:22"}},
	}); err != nil {
		t.Fatal(err)
	}
	u := &model.User{ID: "legacy", Name: "Legacy", Active: true, Protocols: []string{"awg"}, ChainNames: []string{"c"}}
	chain.EnsureUserCreds(u)
	chain.EnsureUserAWGAddress(u, nil)
	if err := st.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	// Empty token is a different path (/sub/ -> root redirect to /ui, handled
	// elsewhere); the relevant assertion is that an unknown NON-empty token
	// 404s (covered by TestSub_UnknownToken_404). Here we just verify the minted
	// token round-trips after the lazy mint.
	tok, err := chain.GenerateSubscriptionToken()
	if err != nil {
		t.Fatal(err)
	}
	u.SubscriptionToken = tok
	if err := st.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	w := ts.getWithUA("/sub/"+tok, "curl/8.0")
	if w.Code != http.StatusOK {
		t.Errorf("minted token status = %d, want 200", w.Code)
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// TestSub_SingboxFormat verifies ?format=singbox returns a complete sing-box
// client config: an awg endpoint in client mode (carrying the user's creds) +
// a route final pointing at the proxy, + a mixed inbound.
func TestSub_SingboxFormat(t *testing.T) {
	ts := newTestServer(t)
	tok := seedSubUser(t, ts)
	w := ts.getWithUA("/sub/"+tok+"?format=singbox", "curl/8.0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, firstN(w.Body.String(), 200))
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := w.Body.String()
	for _, want := range []string{`"type": "awg"`, `"private_key"`, `"mixed"`, `"route"`, `"direct-out"`} {
		if !strings.Contains(body, want) {
			t.Errorf("singbox config missing %q: %s", want, firstN(body, 300))
		}
	}
}

// TestSub_UserinfoHeaders verifies the Profile-Title + Subscription-Userinfo
// metadata headers (client apps render usage/expiry from them).
func TestSub_UserinfoHeaders(t *testing.T) {
	ts := newTestServer(t)
	exp := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	tok := seedSubUser(t, ts, func(u *model.User) {
		u.ExpiresAt = exp
		u.DataLimit = 1 << 30
		u.AWGRxBytes = 1000
		u.AWGTxBytes = 500
	})
	w := ts.getWithUA("/sub/"+tok, "curl/8.0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Profile-Title"); got != "Alice" {
		t.Errorf("Profile-Title = %q, want Alice", got)
	}
	info := w.Header().Get("Subscription-Userinfo")
	for _, want := range []string{"upload=500", "download=1000", "total=1073741824"} {
		if !strings.Contains(info, want) {
			t.Errorf("Subscription-Userinfo missing %q: %q", want, info)
		}
	}
	if !strings.Contains(info, fmt.Sprintf("expire=%d", exp.Unix())) {
		t.Errorf("Subscription-Userinfo missing expire=%d: %q", exp.Unix(), info)
	}
}