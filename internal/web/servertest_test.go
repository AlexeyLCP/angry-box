package web

// servertest_test.go — the HTTP test harness for the web package. Builds a
// Server backed by an in-memory store (temp dir), a fake factory + fake SSH
// connector, and auth disabled, then exposes helpers to fire requests through
// the real ServeMux and assert on the response. No network, no VPS.
//
// This is the foundation of C3 phase 3: every handler test below builds a
// testServer and calls ts.get/ts.post/ts.delete.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/config"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// noopBackend is a ports.Backend whose every method is a no-op success. Used by
// the harness so handler tests don't need the real singbox backend (which would
// try to SSH for GetStatus/Deploy etc.).
type noopBackend struct{}

func (noopBackend) Deploy(context.Context, model.Host) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (noopBackend) DeployWithOptions(context.Context, model.Host, model.DeployOptions) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (noopBackend) InstallAWGModule(context.Context, model.Host) error { return nil }
func (noopBackend) InstallAWGModuleWithOptions(context.Context, model.Host, model.DeployOptions) error { return nil }
func (noopBackend) ApplyConfig(context.Context, model.Host, model.ConfigType, model.ConfigParams) error {
	return nil
}
func (noopBackend) Remove(context.Context, model.Host) error                     { return nil }
func (noopBackend) GetStatus(context.Context, model.Host) (*model.Status, error) { return &model.Status{}, nil }
func (noopBackend) GenerateConfig(model.ConfigType, model.ConfigParams) (*model.Config, error) {
	return &model.Config{}, nil
}
func (noopBackend) Reload(context.Context, model.Host) error { return nil }
func (noopBackend) Name() string                            { return "fake" }
func (noopBackend) Version() string                         { return "test" }

// noopFactory is a ports.Factory returning noopBackend.
type noopFactory struct{}

func (noopFactory) Create() ports.Backend { return noopBackend{} }

// testServer is a fully-wired Server + mux ready for httptest calls.
type testServer struct {
	srv      *Server
	mux      *http.ServeMux
	storePath string
	t        *testing.T
}

// newTestServer builds a Server with an in-memory store (temp dir), a noop
// factory, a fake SSH connector, auth disabled. It registers all routes on a
// fresh ServeMux. Cleanup is automatic via t.Cleanup.
func newTestServer(t *testing.T) *testServer {
	return newTestServerWithConnector(t, nil)
}

// newTestServerWithConnector is like newTestServer but injects an SSH connector
// (for apply-chain / takeover tests that need a fake SSH). nil falls back to the
// production default.
func newTestServerWithConnector(t *testing.T, connector ports.SSHConnector) *testServer {
	t.Helper()
	return newTestServerWith(t, connector, noopFactory{})
}

// newTestServerWithFactory is like newTestServer but injects a custom backend
// factory — used by the health/collectAllMetrics tests to simulate a failing
// or healthy GetStatus without a real SSH dial.
func newTestServerWithFactory(t *testing.T, f ports.Factory) *testServer {
	t.Helper()
	return newTestServerWith(t, nil, f)
}

// newTestServerWith is the shared constructor behind the convenience variants.
func newTestServerWith(t *testing.T, connector ports.SSHConnector, f ports.Factory) *testServer {
	t.Helper()
	dir := t.TempDir()
	storePath := dir + "/store.json"
	cfg := &config.Config{StoreFile: storePath, AuthEnabled: false}
	srv := NewServer(storePath, true, cfg, "127.0.0.1:9080", f)
	if connector != nil {
		srv.connector = connector
	}
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := &testServer{srv: srv, mux: mux, storePath: storePath, t: t}
	t.Cleanup(func() {
		if c, ok := srv.connector.(interface{ Close() error }); ok {
			_ = c.Close()
		}
	})
	return ts
}

// request fires a request through the real mux and returns the recorded
// response. body may be nil. If form is non-nil the request is a form POST with
// the given values (Content-Type application/x-www-form-urlencoded).
func (ts *testServer) request(method, path string, body io.Reader, form url.Values) *httptest.ResponseRecorder {
	ts.t.Helper()
	var r *http.Request
	var err error
	if form != nil {
		r, err = http.NewRequest(method, path, strings.NewReader(form.Encode()))
		if err != nil {
			ts.t.Fatalf("NewRequest: %v", err)
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r, err = http.NewRequest(method, path, body)
		if err != nil {
			ts.t.Fatalf("NewRequest: %v", err)
		}
	}
	// Mark as HTMX so handlers that branch on isHTMXRequest render the partial
	// (the common path the UI actually exercises).
	r.Header.Set("HX-Request", "true")
	// A non-empty RemoteAddr keeps clientIP happy (auth-limiter / logging).
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	ts.mux.ServeHTTP(w, r)
	return w
}

// get fires a GET.
func (ts *testServer) get(path string) *httptest.ResponseRecorder {
	return ts.request(http.MethodGet, path, nil, nil)
}

// post fires a form POST.
func (ts *testServer) post(path string, form url.Values) *httptest.ResponseRecorder {
	return ts.request(http.MethodPost, path, nil, form)
}

// delete fires a DELETE.
func (ts *testServer) delete(path string) *httptest.ResponseRecorder {
	return ts.request(http.MethodDelete, path, nil, nil)
}

// getWithUA fires a GET with the given User-Agent (for handlers that negotiate
// on UA, e.g. /sub/{token}). Does NOT set HX-Request (subscription clients
// aren't HTMX).
func (ts *testServer) getWithUA(path, ua string) *httptest.ResponseRecorder {
	ts.t.Helper()
	r, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		ts.t.Fatalf("NewRequest: %v", err)
	}
	r.Header.Set("User-Agent", ua)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	ts.mux.ServeHTTP(w, r)
	return w
}

// assertStatus fails the test if w.Code != want.
func (ts *testServer) assertStatus(w *httptest.ResponseRecorder, want int) {
	ts.t.Helper()
	if w.Code != want {
		ts.t.Errorf("status: got %d, want %d (body: %s)", w.Code, want, truncate(w.Body.String(), 300))
	}
}

// assertContains fails the test if body does not contain needle.
func (ts *testServer) assertContains(w *httptest.ResponseRecorder, needle string) {
	ts.t.Helper()
	if !strings.Contains(w.Body.String(), needle) {
		ts.t.Errorf("body does not contain %q (body: %s)", needle, truncate(w.Body.String(), 300))
	}
}

// assertNotContains fails the test if body contains needle.
func (ts *testServer) assertNotContains(w *httptest.ResponseRecorder, needle string) {
	ts.t.Helper()
	if strings.Contains(w.Body.String(), needle) {
		ts.t.Errorf("body unexpectedly contains %q (body: %s)", needle, truncate(w.Body.String(), 300))
	}
}

// truncate caps s to n chars with an ellipsis for error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// keep bytes imported (used in rawBody below).
var _ = bytes.MinRead