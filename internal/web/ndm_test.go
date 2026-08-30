package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNDMHook_RejectsForwarded(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hooks/ndm", strings.NewReader("type=ifipchanged"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	s.handleNDMHook(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestNDMHook_LoopbackOK(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hooks/ndm", strings.NewReader("type=ifipchanged"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:1"
	s.handleNDMHook(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("got %d", rr.Code)
	}
}
