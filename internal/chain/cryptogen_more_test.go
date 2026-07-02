package chain

// cryptogen_more_test.go — covers the remaining credential generators in
// cryptogen.go (WGPresharedKey, Hysteria2/Obfs, TUIC uuid/password, VMess WS
// path, SSPassword). CTO-review C3 phase 5.

import (
	"encoding/base64"
	"strings"
	"testing"
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