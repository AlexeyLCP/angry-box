package web

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"

	"github.com/alexeylcp/angry-box/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// BasicAuthMiddleware wraps an http.Handler with Basic Authentication.
//
// Authentication failures are logged at WARN with the remote address and the
// attempted username so brute-force / credential-stuffing attempts leave an
// audit trail (CTO-review M12). Passwords are never logged.
//
// A per-IP rate limiter (defaultAuthLimiter) returns 429 after repeated
// failures within the window, throttling brute-force (CTO-review L3).
func BasicAuthMiddleware(next http.Handler, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)
		if !defaultAuthLimiter.allow(ip) {
			slog.Warn("auth: rate limited",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			// Do NOT count missing credentials as a rate-limit failure: every
			// bare browser GET issues a Basic-Auth challenge with no creds.
			// Counting those locked external IPs after ~5 page loads while
			// SSH-tunnel (127.0.0.1) still worked — the tester's "endless login
			// from outside, OK via tunnel" report.
			slog.Warn("auth: missing credentials",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			unauthorized(w)
			return
		}

		// Constant-time username comparison so an attacker cannot timing-
		// distinguish a wrong username from a wrong password (CTO-review L4).
		// The password is already compared via bcrypt (constant-time internally).
		if subtle.ConstantTimeCompare([]byte(user), []byte(cfg.AuthUsername)) != 1 {
			defaultAuthLimiter.recordFailure(ip)
			slog.Warn("auth: unknown user",
				"remote_addr", r.RemoteAddr,
				"username", user,
				"path", r.URL.Path,
			)
			unauthorized(w)
			return
		}

		// Compare password against the hash
		err := bcrypt.CompareHashAndPassword([]byte(cfg.AuthPasswordHash), []byte(pass))
		if err != nil {
			defaultAuthLimiter.recordFailure(ip)
			slog.Warn("auth: wrong password",
				"remote_addr", r.RemoteAddr,
				"username", user,
				"path", r.URL.Path,
			)
			unauthorized(w)
			return
		}

		// Correct password — clear any prior failures so a recovered admin is
		// not stuck behind a half-full window from earlier typos.
		defaultAuthLimiter.clear(ip)
		next.ServeHTTP(w, r)
	}
}

// clientIP extracts the remote IP from r.RemoteAddr ("host:port"), falling back
// to the raw value if parsing fails.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Angry-BOX"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
