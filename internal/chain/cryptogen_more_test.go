package chain

// cryptogen_more_test.go — covers the remaining credential generators in
// cryptogen.go (WGPresharedKey, Hysteria2/Obfs, TUIC uuid/password, VMess WS
// path, SSPassword). CTO-review C3 phase 5.

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestGenerateWGPresharedKey verifies a 32-byte base64 preshared key.
func TestGenerateWGPresharedKey(t *testing.T) {
	k, err := GenerateWGPresharedKey()
	if err != nil {
		t.Fatalf("GenerateWGPresharedKey: %v", err)
	}
	b, err := base64.StdEncoding.DecodeString(k)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("decoded len: got %d, want 32", len(b))
	}
}

// TestGenerateHysteria2Password verifies a url-safe base64 of 16 bytes.
func TestGenerateHysteria2Password(t *testing.T) {
	p := GenerateHysteria2Password()
	b, err := base64.URLEncoding.DecodeString(p)
	if err != nil {
		t.Fatalf("not valid url-safe base64: %v", err)
	}
	if len(b) != 16 {
		t.Errorf("decoded len: got %d, want 16", len(b))
	}
}

// TestGenerateHysteria2ObfsPassword verifies it returns a non-empty password.
func TestGenerateHysteria2ObfsPassword(t *testing.T) {
	if p := GenerateHysteria2ObfsPassword(); p == "" {
		t.Error("expected non-empty obfs password")
	}
}

// TestGenerateTUICUUID verifies a v4 UUID shape.
func TestGenerateTUICUUID(t *testing.T) {
	u := GenerateTUICUUID()
	if !strings.Contains(u, "-") || len(u) != 36 {
		t.Errorf("got %q, want a 36-char UUID with dashes", u)
	}
}

// TestGenerateTUICPassword verifies a url-safe base64 of 16 bytes.
func TestGenerateTUICPassword(t *testing.T) {
	p := GenerateTUICPassword()
	b, err := base64.URLEncoding.DecodeString(p)
	if err != nil {
		t.Fatalf("not valid url-safe base64: %v", err)
	}
	if len(b) != 16 {
		t.Errorf("decoded len: got %d, want 16", len(b))
	}
}

// TestGenerateVMessWSPath verifies the path starts with "/" and has 8 base64
// chars after it.
func TestGenerateVMessWSPath(t *testing.T) {
	p := GenerateVMessWSPath()
	if !strings.HasPrefix(p, "/") {
		t.Errorf("got %q, want leading /", p)
	}
	if len(p) < 2 {
		t.Errorf("path too short: %q", p)
	}
}

// TestGenerateSSPassword_2022 verifies a 2022-blake3 cipher yields base64 bytes
// of the cipher's key length.
func TestGenerateSSPassword_2022(t *testing.T) {
	p := GenerateSSPassword("2022-blake3-aes-128-gcm")
	if p == "" {
		t.Fatal("expected non-empty password")
	}
	if _, err := base64.StdEncoding.DecodeString(p); err != nil {
		t.Errorf("2022 cipher password not base64: %v", err)
	}
}

// TestGenerateSSPassword_UnknownCipher verifies an unknown cipher falls back to
// the default (non-empty password).
func TestGenerateSSPassword_UnknownCipher(t *testing.T) {
	p := GenerateSSPassword("not-a-cipher")
	if p == "" {
		t.Fatal("expected non-empty password after fallback")
	}
}

// TestGenerateSSPassword_Legacy verifies a legacy cipher yields 32 hex chars.
func TestGenerateSSPassword_Legacy(t *testing.T) {
	p := GenerateSSPassword("aes-256-gcm")
	if p == "" {
		t.Fatal("expected non-empty password")
	}
}
// TestEnsureUserCreds_GeneratesPerProtocol verifies EnsureUserCreds fills the
// per-user credentials for each selected protocol and leaves existing creds
// untouched (stable across applies).
func TestEnsureUserCreds_GeneratesPerProtocol(t *testing.T) {
	u := &model.User{ID: "u1", Name: "alice", Protocols: []string{"tuic", "hysteria2", "vless-reality"}}
	EnsureUserCreds(u)
	if u.TUICUUID == "" {
		t.Error("TUICUUID not generated")
	}
	if u.TUICPassword == "" {
		t.Error("TUICPassword not generated")
	}
	if u.TUICUUID == u.TUICPassword {
		t.Error("TUICUUID must differ from TUICPassword (independent secrets)")
	}
	if u.Hysteria2Password == "" {
		t.Error("Hysteria2Password not generated")
	}
	if u.VLESSUUID == "" {
		t.Error("VLESSUUID not generated")
	}
}

func TestEnsureUserCreds_PreservesExisting(t *testing.T) {
	// Existing creds must be preserved (not regenerated) on re-apply.
	u := &model.User{
		ID:              "u2",
		Name:            "bob",
		Protocols:       []string{"tuic"},
		TUICUUID:        "fixed-uuid",
		TUICPassword:    "fixed-password",
		Hysteria2Password: "existing-hy2",
	}
	EnsureUserCreds(u)
	if u.TUICUUID != "fixed-uuid" {
		t.Errorf("TUICUUID overwritten: got %q want fixed-uuid", u.TUICUUID)
	}
	if u.TUICPassword != "fixed-password" {
		t.Errorf("TUICPassword overwritten: got %q want fixed-password", u.TUICPassword)
	}
	// Hysteria2 not in Protocols -> must NOT be generated/touched.
	if u.Hysteria2Password != "existing-hy2" {
		t.Errorf("Hysteria2Password touched despite hysteria2 not in Protocols: got %q", u.Hysteria2Password)
	}
}

func TestEnsureUserCreds_NilSafeAndEmptyProtocols(t *testing.T) {
	EnsureUserCreds(nil) // must not panic
	u := &model.User{ID: "u3", Name: "empty", Protocols: nil}
	EnsureUserCreds(u)
	if u.TUICUUID != "" || u.VLESSUUID != "" || u.Hysteria2Password != "" {
		t.Error("empty Protocols must not generate any creds")
	}
}
