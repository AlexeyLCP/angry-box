package web

// recover_test.go — verifies the auth-middleware recover wrapper prevents a
// handler panic from crashing the test process / orchestrator and returns a
// 500. CTO-review finding: generator panics (mustMarshal /
// cryptogen.Generate*) in the request path could take down the process; those
// generators now return errors (CTO-review #3), and the recover in auth()
// remains the safety net for any residual panic.

import (
	"net/http"
	"testing"
)

func TestAuth_RecoversHandlerPanic(t *testing.T) {
	ts := newTestServer(t)
	// Register a panic-y handler on the server's mux directly.
	panicHandler := func(w http.ResponseWriter, r *http.Request) {
		panic("boom from test handler")
	}
	ts.mux.HandleFunc("GET /ui/test-panic", ts.srv.auth(http.HandlerFunc(panicHandler)))
	w := ts.get("/ui/test-panic")
	// The recover must convert the panic into a 500 (not crash the test).
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from recovered panic, got %d (body: %s)", w.Code, w.Body.String())
	}
}