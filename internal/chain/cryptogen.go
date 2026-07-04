package chain

// cryptogen.go — on-demand credential generators for the UI "Generate" buttons
// and the /ui/api/crypto/* / /ui/api/protocols/generate endpoints. Ported from
// VPN/orchestrator/app/services/crypto.py + protocol_presets.py generators.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"golang.org/x/crypto/curve25519"
)

// GenerateRealityKeypair returns (private, public) base64-url X25519 keys,
// suitable for a REALITY inbound. Reuses the same X25519 as WireGuard.
func GenerateRealityKeypair() (priv, pub string, err error) {
	return generateWGKeypair()
}

// generateWGKeypair returns base64 WireGuard/AWG/Reality keys (clamped X25519).
func generateWGKeypair() (priv, pub string, err error) {
	privBytes := make([]byte, 32)
	if _, err = rand.Read(privBytes); err != nil {
		return "", "", err
	}
	privBytes[0] &= 248
	privBytes[31] &= 127
	privBytes[31] |= 64
	pubBytes, err := curve25519.X25519(privBytes, curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(privBytes), base64.RawURLEncoding.EncodeToString(pubBytes), nil
}

// GenerateWGPresharedKey returns a base64-encoded 32-byte preshared key.
func GenerateWGPresharedKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// GenerateRealityShortID returns a random short id of 0..8 bytes (0..16 hex
// chars). n==0 → "" (empty is a valid REALITY short id).
func GenerateRealityShortID() string {
	n := randInt(0, 8)
	if n == 0 {
		return ""
	}
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b) // always even-length
}

// GenerateRealityShortIDs returns count short ids, the first always "".
func GenerateRealityShortIDs(count int) []string {
	ids := make([]string, 0, count)
	ids = append(ids, "")
	for i := 1; i < count; i++ {
		ids = append(ids, GenerateRealityShortID())
	}
	return ids
}

// GenerateTrojanPassword returns a base64 of 16 random bytes (~24 chars).
func GenerateTrojanPassword() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// GenerateSSPassword returns a password for the given Shadowsocks cipher. 2022
// ciphers (key length > 0) get base64 of N random bytes; legacy ciphers get 32
// hex chars.
func GenerateSSPassword(cipher string) string {
	keyLen := SS_CIPHERS[cipher]
	if keyLen == 0 {
		cipher = SS_DEFAULT_CIPHER
		keyLen = SS_CIPHERS[cipher]
	}
	if keyLen > 0 {
		b := make([]byte, keyLen)
		_, _ = rand.Read(b)
		return base64.StdEncoding.EncodeToString(b)
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateHysteria2Password returns a url-safe base64 of 16 random bytes.
func GenerateHysteria2Password() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// GenerateHysteria2ObfsPassword returns the obfs password (same shape as the
// main password).
func GenerateHysteria2ObfsPassword() string { return GenerateHysteria2Password() }

// GenerateTUICUUID returns a v4 UUID string.
func GenerateTUICUUID() string { return generateStableUUID() }

// GenerateInboundTag returns a stable, short, lowercase tag for a standalone
// inbound (e.g. "sa-awg-a1b2c3d4"). Used as the sing-box inbound/endpoint tag
// and as the users-by-inbound map key — stable across inbound reorders, unlike
// the legacy index-based "sa-<i>-<proto>". proto is sanitized to alphanumerics.
func GenerateInboundTag(proto string) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("cryptogen: crypto/rand failed for inbound tag: " + err.Error())
	}
	safe := []byte{}
	for _, c := range strings.ToLower(proto) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			safe = append(safe, byte(c))
		}
	}
	if len(safe) == 0 {
		safe = []byte("in")
	}
	return fmt.Sprintf("sa-%s-%x", safe, b)
}

// GenerateTUICPassword returns a url-safe base64 of 16 random bytes. It is an
// INDEPENDENT secret from GenerateTUICUUID: a TUIC link exposes both identity
// and credential, so they must not share a value (CTO-review M7).
func GenerateTUICPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should not fail in practice; if it does, refuse to emit a
		// predictable/empty password rather than degrade silently.
		panic("cryptogen: crypto/rand failed for TUIC password: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)
}

// GenerateVMessWSPath returns "/<8 url-safe chars>".
func GenerateVMessWSPath() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "/" + base64.URLEncoding.EncodeToString(b)
}

// GenerateMTProxySecret returns 16 random bytes hex-encoded (32 chars).
func GenerateMTProxySecret() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// MTProxyFullSecret composes the sing-box/mtg FakeTLS secret:
//
//	"ee" + secret_hex + hex(fake_tls_domain)
//
// secret_hex must be 32 chars (16 bytes).
func MTProxyFullSecret(secretHex, fakeTLSDomain string) (string, error) {
	if len(secretHex) != 32 {
		return "", fmt.Errorf("mtproxy secret must be 32 hex chars, got %d", len(secretHex))
	}
	return "ee" + secretHex + hex.EncodeToString([]byte(fakeTLSDomain)), nil
}

// GenerateProxyPassword returns a 16-char ASCII password (a-zA-Z0-9) sampled
// uniformly via crypto/rand. It uses rejection sampling through big.Int so the
// distribution is unbiased — the previous int(c)%len(alphabet) approach skewed
// the first symbols because 256 is not a multiple of 62 (CTO-review L8).
func GenerateProxyPassword() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 16)
	for i := range out {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			// crypto/rand should not fail in practice; if it does, refuse to
			// emit a biased character rather than silently degrading.
			panic("cryptogen: crypto/rand failed for proxy password: " + err.Error())
		}
		out[i] = alphabet[idx.Int64()]
	}
	return string(out)
}

// EnsureUserCreds fills in any missing per-user protocol credentials on a User
// based on its Protocols list. It only generates what is empty — existing creds
// are preserved (stable across applies; rotated only explicitly via the UI).
// This is the basis for per-client routing: a multi-user inbound emits one
// Users[] entry per user carrying these per-user creds, and a route rule steers
// that user to a chosen exit. Empty Protocols leaves creds empty (legacy
// behavior — the user falls back to chain-wide / shared inbound creds).
//
// VLESS/TUIC/Hysteria2 are matched by auth_user. AWG (AmneziaWG) has no
// auth_user: each user is a WireGuard peer identified by a PublicKey + a unique
// tunnel IP. EnsureUserCreds only generates the AWG keypair here (context-free);
// the per-user tunnel IP must be allocated against the other users' IPs and is
// assigned separately via EnsureUserAWGAddress (which needs the store).
func EnsureUserCreds(u *model.User) {
	if u == nil {
		return
	}
	has := func(p string) bool {
		for _, proto := range u.Protocols {
			if proto == p {
				return true
			}
		}
		return false
	}
	if has("vless-reality") && u.VLESSUUID == "" {
		u.VLESSUUID = generateStableUUID()
	}
	if has("tuic") {
		if u.TUICUUID == "" {
			u.TUICUUID = GenerateTUICUUID()
		}
		if u.TUICPassword == "" {
			u.TUICPassword = GenerateTUICPassword()
		}
	}
	if has("hysteria2") && u.Hysteria2Password == "" {
		u.Hysteria2Password = GenerateHysteria2Password()
	}
	// AWG: generate the per-user WireGuard keypair (StdEncoding, as WireGuard
	// expects). The tunnel IP (AWGAddress) is NOT allocated here — it requires
	// the list of IPs already taken by other users (see EnsureUserAWGAddress).
	if has("awg") && u.AWGPrivateKey == "" {
		priv, pub, err := GenerateWireGuardKeypair()
		if err == nil {
			u.AWGPrivateKey = priv
			u.AWGPublicKey = pub
		}
	}
}

// EnsureUserAWGAddress allocates a unique AWG tunnel IP for the user when AWG
// is among their protocols and AWGAddress is still empty. existing is the set
// of AWGAddress values already taken by other users (caller gathers them from
// the store, e.g. via ListUsers). Allocation is deterministic — the first free
// address — so existing users keep their IP across re-applies. No-op when AWG
// is not a protocol or the address is already set.
func EnsureUserAWGAddress(u *model.User, existing []string) {
	if u == nil || u.AWGAddress != "" {
		return
	}
	has := false
	for _, p := range u.Protocols {
		if p == "awg" {
			has = true
			break
		}
	}
	if !has {
		return
	}
	u.AWGAddress = allocateAWGPeerIP(existing)
}

// allocateAWGPeerIP returns the first free address in 10.8.0.0/24 (host part
// 2..254; .1 is the server, .255 is broadcast). taken is the list of currently
// occupied addresses (any form — with or without /32 suffix). Returns "" only
// when the /24 is exhausted (unlikely for a single chain).
func allocateAWGPeerIP(taken []string) string {
	occupied := make(map[string]bool, len(taken))
	for _, a := range taken {
		occupied[awgIPKey(a)] = true
	}
	for host := 2; host <= 254; host++ {
		ip := fmt.Sprintf("10.8.0.%d/32", host)
		if !occupied[awgIPKey(ip)] {
			return ip
		}
	}
	return ""
}

// awgIPKey normalizes an AWG address to its bare IPv4 (strip /32 etc.) so that
// "10.8.0.3/32" and "10.8.0.3" compare equal.
func awgIPKey(a string) string {
	for i := 0; i < len(a); i++ {
		if a[i] == '/' {
			return a[:i]
		}
	}
	return a
}

// allocateAWGTransitIP returns the first free address in 10.9.0.0/24 (host part
// 2..254; .1 is reserved, .255 is broadcast) for an inter-node AWG transport
// link's client inner IP. Separate from allocateAWGPeerIP (which uses
// 10.8.0.0/24 for user-entry peers) so the two subnets never collide. taken is
// the list of TransitAWGAddress values already claimed by other nodes.
func allocateAWGTransitIP(taken []string) string {
	return allocateAWGHostIP("10.9.0", taken)
}

// allocateAWGExitIP returns the first free address in 10.10.0.0/24 for a
// balancer-side kernel AWG exit-link client interface (awg-exit-nX). Separate
// from user-entry (10.8.0.0/24) and inter-node transport (10.9.0.0/24).
func allocateAWGExitIP(taken []string) string {
	return allocateAWGHostIP("10.10.0", taken)
}

func allocateAWGHostIP(prefix string, taken []string) string {
	occupied := make(map[string]bool, len(taken))
	for _, a := range taken {
		occupied[awgIPKey(a)] = true
	}
	for host := 2; host <= 254; host++ {
		ip := fmt.Sprintf("%s.%d/32", prefix, host)
		if !occupied[awgIPKey(ip)] {
			return ip
		}
	}
	return ""
}
