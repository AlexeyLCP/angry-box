package web

import (
	"net/http"

	"github.com/alexeylcp/angry-box/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// BasicAuthMiddleware wraps an http.Handler with Basic Authentication.
func BasicAuthMiddleware(next http.Handler, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != cfg.AuthUsername {
			unauthorized(w)
			return
		}

		// Compare password against the hash
		err := bcrypt.CompareHashAndPassword([]byte(cfg.AuthPasswordHash), []byte(pass))
		if err != nil {
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
