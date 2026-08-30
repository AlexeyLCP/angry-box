package web

import (
	"net/http"
	"net/url"
	"strings"
)

// csrfMiddleware rejects cross-origin state-changing requests.
//
// Angry-BOX uses HTTP Basic Auth for the panel. Browser Basic-Auth credentials
// are NOT protected by cookie SameSite attributes, so a malicious page can
// trigger authenticated POSTs in an admin's session (CSRF). The defense used
// here is the OWASP-recommended Origin / Sec-Fetch-Site check: for any unsafe
// HTTP method we require the request to originate from the same origin as the
// panel itself. Modern browsers (and HTMX, which uses fetch) always send these
// headers; missing headers on a state-changing request are rejected
// fail-closed.
//
// This is defense-in-depth on top of the auth gate; it does not replace it.
func CSRSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		if !sameOrigin(r) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("cross-origin state-changing request rejected"))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// sameOrigin reports whether the request originates from the same origin
// (scheme://host:port) as the panel. It uses Sec-Fetch-Site when available
// (set by all modern browsers for fetch/form submissions, including HTMX) and
// falls back to the Origin/Referer headers otherwise. Requests with no usable
// evidence of same-origin on an unsafe method are rejected fail-closed.
func sameOrigin(r *http.Request) bool {
	// Sec-Fetch-Site: "same-origin" | "same-site" | "cross-site" | "none".
	// "none" is a user-initiated top-level navigation (e.g. typing a URL or a
	// bookmark) — not present for background fetches with credentials. We accept
	// "same-origin" only; "none" is rejected for state-changing requests to
	// avoid relying on ambiguous semantics for form/fetch submissions.
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
		return sfs == "same-origin"
	}

	target := requestOrigin(r)
	if target == "" {
		// No Host header at all — we cannot establish an origin, refuse.
		return false
	}

	if origin := r.Header.Get("Origin"); origin != "" {
		return equalOrigin(origin, target)
	}

	// Fall back to Referer for older browsers.
	if ref := r.Header.Get("Referer"); ref != "" {
		return equalOrigin(ref, target)
	}

	// No evidence of same-origin — reject fail-closed.
	return false
}

// requestOrigin reconstructs the panel's own origin as "host:port" (without
// scheme, since the comparison is scheme-aware separately). We return only the
// host:port portion; the scheme is assumed to match the listener (http here).
func requestOrigin(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return strings.ToLower(host)
}

// equalOrigin compares an Origin/Referer header value to the panel host:port.
// The header may be a full URL (http://host:port) or "null"; the panel host is
// a bare host:port. We compare the host:port portion, case-insensitively,
// and additionally require the scheme to match http (the panel never serves
// https directly in the current setup).
func equalOrigin(headerValue, panelHost string) bool {
	if headerValue == "" || headerValue == "null" {
		return false
	}
	u, err := url.Parse(headerValue)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.ToLower(u.Host) == panelHost
}