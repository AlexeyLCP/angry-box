package web

// csrf_test.go verifies the Origin / Sec-Fetch-Site based CSRF guard that
// protects all state-changing routes of the panel. The panel authenticates via
// HTTP Basic Auth, whose credentials are not shielded by cookie SameSite, so a
// cross-origin page can otherwise submit authenticated POSTs in an admin's
// session. These tests pin the security-relevant behavior: same-origin unsafe
// requests pass, cross-origin unsafe requests are rejected, and safe methods
// (GET/HEAD/OPTIONS) always pass regardless of origin.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCSRF_SafeMethodsAlwaysPass(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(m, "http://panel:9080/ui", nil)
		req.Host = "panel:9080"
		// Deliberately cross-site evidence — must be ignored for safe methods.
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rr := httptest.NewRecorder()
		ok := newCSRFTestHandler()
		CSRSMiddleware(ok).ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("safe method %s should pass, got %d", m, rr.Code)
		}
	}
}

func TestCSRF_SameOriginPostPasses(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://panel:9080/ui/settings", nil)
	req.Host = "panel:9080"
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	ok := newCSRFTestHandler()
	CSRSMiddleware(ok).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("same-origin POST should pass, got %d", rr.Code)
	}
}

func TestCSRF_CrossSiteSecFetchSiteRejected(t *testing.T) {
	// Simulates the historical attack: a forged empty POST to /ui/settings
	// submitted from a malicious page in an admin's Basic-Auth session.
	req := httptest.NewRequest(http.MethodPost, "http://panel:9080/ui/settings", nil)
	req.Host = "panel:9080"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rr := httptest.NewRecorder()
	ok := newCSRFTestHandler()
	CSRSMiddleware(ok).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST must be rejected with 403, got %d", rr.Code)
	}
}

func TestCSRF_CrossOriginHeaderRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://panel:9080/ui/nodes/n1/edit", nil)
	req.Host = "panel:9080"
	req.Header.Set("Origin", "http://evil.example.com")
	rr := httptest.NewRecorder()
	ok := newCSRFTestHandler()
	CSRSMiddleware(ok).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST must be rejected with 403, got %d", rr.Code)
	}
}

func TestCSRF_OriginMatchingHostPasses(t *testing.T) {
	// Older browser without Sec-Fetch-Site: Origin matches the panel host.
	req := httptest.NewRequest(http.MethodPost, "http://panel:9080/ui/settings", nil)
	req.Host = "panel:9080"
	req.Header.Set("Origin", "http://panel:9080")
	rr := httptest.NewRecorder()
	ok := newCSRFTestHandler()
	CSRSMiddleware(ok).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("same-origin (via Origin header) POST should pass, got %d", rr.Code)
	}
}

func TestCSRF_NoEvidenceRejectedFailClosed(t *testing.T) {
	// Unsafe method with neither Sec-Fetch-Site nor Origin/Referer: reject.
	req := httptest.NewRequest(http.MethodPost, "http://panel:9080/ui/settings", nil)
	req.Host = "panel:9080"
	rr := httptest.NewRecorder()
	ok := newCSRFTestHandler()
	CSRSMiddleware(ok).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("unevidenced unsafe request must be rejected fail-closed, got %d", rr.Code)
	}
}

func TestCSRF_HTTPSOriginAgainstHTTPPanelRejected(t *testing.T) {
	// The panel serves plain http; an https Origin against it is suspicious
	// (e.g. a TLS proxy fronting a different public host) and must be rejected.
	req := httptest.NewRequest(http.MethodPost, "http://panel:9080/ui/settings", nil)
	req.Host = "panel:9080"
	req.Header.Set("Origin", "https://panel:9080")
	rr := httptest.NewRecorder()
	ok := newCSRFTestHandler()
	CSRSMiddleware(ok).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("https-origin against http panel must be rejected, got %d", rr.Code)
	}
}

func TestCSRF_NullOriginRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://panel:9080/ui/settings", nil)
	req.Host = "panel:9080"
	req.Header.Set("Origin", "null")
	rr := httptest.NewRecorder()
	ok := newCSRFTestHandler()
	CSRSMiddleware(ok).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("'null' Origin must be rejected, got %d", rr.Code)
	}
}

func newCSRFTestHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}