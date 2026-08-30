package web

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexeylcp/angry-box/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// bcryptDummyHash is compared when the username is unknown so the cost of a
// miss matches a wrong password (no username oracle).
var bcryptDummyHash = mustDummyBcrypt()

func mustDummyBcrypt() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}

// authLockoutNotify, when set (the fleet bot is running), receives lockout
// alerts. Guarded by lockoutNotifyMu; one alert per IP per window so a
// hammering attacker cannot spam the operator's chat.
var (
	authLockoutNotify   func(ip string)
	lockoutNotifyMu     sync.Mutex
	lastLockoutNotify   = map[string]time.Time{}
	lockoutNotifyWindow = 15 * time.Minute
)

func notifyLockoutOnce(ip string) {
	lockoutNotifyMu.Lock()
	notify := authLockoutNotify
	if notify != nil {
		if last, ok := lastLockoutNotify[ip]; ok && time.Since(last) < lockoutNotifyWindow {
			notify = nil
		} else {
			lastLockoutNotify[ip] = time.Now()
		}
	}
	lockoutNotifyMu.Unlock()
	if notify != nil {
		notify(ip)
	}
}

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
			notifyLockoutOnce(ip)
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
			_ = bcrypt.CompareHashAndPassword(bcryptDummyHash, []byte(pass))
			defaultAuthLimiter.recordFailure(ip)
			slog.Warn("auth: unknown user",
				"remote_addr", r.RemoteAddr,
				"username", user,
				"path", r.URL.Path,
			)
			unauthorized(w)
			return
		}

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
		host = r.RemoteAddr
	}
	if isLoopbackHost(host) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			return xri
		}
	}
	return host
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackRemote(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return isLoopbackHost(host)
}

func forwardedByProxy(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-For") != "" ||
		r.Header.Get("X-Real-IP") != "" ||
		r.Header.Get("X-Forwarded-Proto") != ""
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Angry-BOX"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
