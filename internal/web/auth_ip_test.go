package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP_XFFOnlyFromLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:9"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("loopback XFF: got %q", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.1:9"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(r); got != "203.0.113.1" {
		t.Fatalf("non-loopback must ignore XFF: got %q", got)
	}
}

func TestClientIP_XFFRightmost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:9"
	r.Header.Set("X-Forwarded-For", "8.8.8.8, 203.0.113.9")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("got %q", got)
	}
}
