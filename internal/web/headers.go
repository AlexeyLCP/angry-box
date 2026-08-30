package web

import "net/http"

// SecurityHeadersMiddleware sets clickjacking and content-type defenses on
// every response. CSP allows 'unsafe-inline' scripts because the panel still
// has a few onclick= handlers and the i18n dictionary in base.templ; remote
// script/style origins are denied (HTMX and DaisyUI are self-hosted).
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'")
		next.ServeHTTP(w, r)
	})
}
