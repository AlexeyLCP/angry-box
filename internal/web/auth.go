package web

import (
	"log/slog"
	"net/http"

	"github.com/alexeylcp/angry-box/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// BasicAuthMiddleware wraps an http.Handler with Basic Authentication.
//
// Authentication failures are logged at WARN with the remote address and the
// attempted username so brute-force / credential-stuffing attempts leave an
// audit trail (CTO-review M12). Passwords are never logged.
func BasicAuthMiddleware(next http.Handler, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			slog.Warn("auth: missing credentials",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
			)
			unauthorized(w)
			return
		}

		if user != cfg.AuthUsername {
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
			slog.Warn("auth: wrong password",
				"remote_addr", r.RemoteAddr,
				"username", user,
				"path", r.URL.Path,
			)
			unauthorized(w)
			return
		}

		next.ServeHTTP(w, r)
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Angry-BOX"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
