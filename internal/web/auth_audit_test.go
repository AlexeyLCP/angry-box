package web

// auth_audit_test.go pins that authentication failures are logged with enough
// context (remote address, username attempted, reason) to support an audit
// trail / intrusion detection. The panel's auth gate previously returned 401
// silently, so brute-force attempts left no trace (CTO-review M12).

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func newAuditAuthCfg(t *testing.T, enabled bool) *config.Config {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse-battery-staple"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		AuthEnabled:      enabled,
		AuthUsername:     "admin",
		AuthPasswordHash: string(hash),
	}
}

func TestAuthFailure_BadPasswordIsLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	cfg := newAuditAuthCfg(t, true)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://panel:9080/ui", nil)
	req.SetBasicAuth("admin", "wrong-password")
	req.RemoteAddr = "203.0.113.7:51234"

	BasicAuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not be called on auth failure")
	}), cfg).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "auth") {
		t.Errorf("log line should mention auth, got: %q", out)
	}
	if !strings.Contains(out, "203.0.113.7") {
		t.Errorf("log line should include remote addr, got: %q", out)
	}
	if !strings.Contains(out, "admin") {
		t.Errorf("log line should include attempted username, got: %q", out)
	}
}

func TestAuthFailure_MissingCredentialsIsLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	cfg := newAuditAuthCfg(t, true)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://panel:9080/ui", nil)
	req.RemoteAddr = "198.51.100.20:51234"

	BasicAuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not be called without credentials")
	}), cfg).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "missing") || !strings.Contains(out, "credential") {
		t.Errorf("log should note missing credentials, got: %q", out)
	}
	if !strings.Contains(out, "198.51.100.20") {
		t.Errorf("log should include remote addr, got: %q", out)
	}
}

func TestAuthSuccess_NotLoggedAsFailure(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	cfg := newAuditAuthCfg(t, true)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://panel:9080/ui", nil)
	req.SetBasicAuth("admin", "correct-horse-battery-staple")
	req.RemoteAddr = "127.0.0.1:1234"

	called := false
	BasicAuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
		w := httptest.NewRecorder()
		w.WriteHeader(http.StatusOK)
	}), cfg).ServeHTTP(rr, req)

	if !called {
		t.Fatal("handler should be called on auth success")
	}
	if buf.Len() > 0 {
		t.Errorf("success should not produce a warning log, got: %q", buf.String())
	}
}