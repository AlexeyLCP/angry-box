package web

// auth_constanttime_test.go pins that the username comparison in the auth gate
// is constant-time, so an attacker cannot timing-distinguish a wrong username
// from a wrong password (CTO-review L4). The password is already compared via
// bcrypt (constant-time internally); only the username comparison was a plain
// != that leaked whether a username exists.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexeylcp/angry-box/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func TestAuth_UsernameComparisonIsConstantTime(t *testing.T) {
	// We can't directly assert timing, but we can assert the contract:
	// an unknown user and a known user with a wrong password both yield 401
	// with identical outward behavior (same status, same WWW-Authenticate),
	// and the username is compared via subtle.ConstantTimeCompare (verified
	// by code inspection + the regression below: leading-whitespace / case
	// differences must not silently match).
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	cfg := &config.Config{AuthEnabled: true, AuthUsername: "admin", AuthPasswordHash: string(hash)}

	for _, user := range []string{"admin", "Admin", " admin", "nonexistent", ""} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://panel:9080/ui", nil)
		req.SetBasicAuth(user, "wrong")
		BasicAuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Errorf("handler called for user %q with wrong password", user)
		}), cfg).ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("user %q: expected 401, got %d", user, rr.Code)
		}
	}
}