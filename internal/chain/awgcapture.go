package chain

// awgcapture.go — live QUIC signature capture, ported from
// VPN/orchestrator/app/services/awg_capture.py (which itself was ported from
// hoaxisr/awg-manager — https://github.com/hoaxisr/awg-manager, MIT). The
// upstream algorithm is MIT-licensed and integrated here with attribution;
// this file as a whole is part of Angry-box, distributed under the project's
// PolyForm Noncommercial license (see /LICENSE).
//
// CPS capture modes (do not confuse):
//   - QUIC live capture (THIS FILE): UDP domain:443 → QUIC Initial → server
//     responses → I1-I5. Works with AWG. The Initial carries a real TLS 1.3
//     ClientHello inside a QUIC CRYPTO frame (via quic_initial_aead.go) — that
//     is normal QUIC, not a separate "TLS capture" mode.
//   - TCP TLS live capture: plain TLS handshake over TCP:443. awg-manager does
//     NOT implement this for AWG (incompatible, crashes). We do not port it.
//
// Connects to domain:443 over UDP, sends a real QUIC Initial (SNI=domain),
// captures up to 5 server response packets, and hex-encodes them as <b 0x...>
// for I1-I5. This yields a domain-accurate QUIC silhouette that DPI cannot
// distinguish from real traffic to that domain — stronger than the synthesized
// CPS packets.

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

const (
	captureTimeout   = 5 * time.Second
	captureMaxPacket = 1500
	captureMaxPkts   = 5
)

// CaptureResult mirrors the Python CaptureResult dataclass.
type CaptureResult struct {
	OK      bool     `json:"ok"`
	Source  string   `json:"source"`  // "quic" | "error"
	Packets []string `json:"packets"` // ["<b 0x...>", ...]
	Warning string   `json:"warning,omitempty"`
}

var domainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*\.[a-zA-Z]{2,}$`)

// NormalizeDomain strips scheme/path/port (skips IPv6 [..]:port), lowercases.
func NormalizeDomain(d string) string {
	d = strings.TrimSpace(d)
	lower := strings.ToLower(d)
	if strings.HasPrefix(lower, "https://") {
		d = d[8:]
	} else if strings.HasPrefix(lower, "http://") {
		d = d[7:]
	}
	if i := strings.IndexByte(d, '/'); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndexByte(d, ':'); i >= 0 {
		head := d[:i]
		if head != "" && !strings.HasPrefix(head, "[") {
			d = head
		}
	}
	return strings.TrimSpace(strings.ToLower(d))
}

// IsValidDomain reports whether d matches the vetted domain regex.
func IsValidDomain(d string) bool {
	return domainRe.MatchString(d)
}

// CaptureQUICSignature performs a live QUIC capture against domain. timeout<=0
// uses the default 5s. On any failure, returns a CaptureResult with Source=
// "error" and a human-readable Warning (no Go error — mirrors Python which
// always returns a result, not an exception).
func CaptureQUICSignature(domain string, timeout time.Duration) CaptureResult {
	if timeout <= 0 {
		timeout = captureTimeout
	}
	domain = NormalizeDomain(domain)
	if domain == "" {
		return CaptureResult{Source: "error", Warning: "Empty domain"}
	}
	if !IsValidDomain(domain) {
		return CaptureResult{Source: "error", Warning: fmt.Sprintf("Invalid domain: %s", domain)}
	}

	ip, err := net.ResolveIPAddr("ip", domain)
	if err != nil {
		return CaptureResult{Source: "error", Warning: fmt.Sprintf("Domain not found: %s", domain)}
	}

	// Build a real, AEAD-encrypted QUIC v1 Initial carrying a genuine TLS 1.3
	// ClientHello with SNI=domain (RFC 9001 key derivation + AES-128-GCM +
	// header protection — see quic_initial_aead.go). The previous path reused the
	// synthesized shape-fake GenerateQUICInitialWithSNI, which some QUIC servers
	// dropped (no real QUIC cryptography) → no response. A real Initial makes
	// every QUIC server reply, yielding genuine I1-I5 packets.
	initial, _, buildErr := buildQUICInitialAEAD(domain)
	if buildErr != nil {
		return CaptureResult{Source: "error", Warning: fmt.Sprintf("Failed to build QUIC Initial: %v", buildErr)}
	}

	sock, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: ip.IP, Port: 443})
	if err != nil {
		return CaptureResult{Source: "error", Warning: fmt.Sprintf("Failed to send to %s:443: %v", ip.IP, err)}
	}
	defer sock.Close()
	_ = sock.SetReadDeadline(time.Now().Add(timeout))

	if _, err := sock.Write(initial); err != nil {
		return CaptureResult{Source: "error", Warning: fmt.Sprintf("Failed to send to %s:443: %v", ip.IP, err)}
	}

	// I1 = the sent Initial; I2..I5 = server responses.
	packets := [][]byte{initial}
	buf := make([]byte, 2048)
	for len(packets) < captureMaxPkts+1 {
		n, _, err := sock.ReadFrom(buf)
		if err != nil {
			break // timeout or error → stop reading
		}
		if n > 0 {
			pkt := make([]byte, n)
			copy(pkt, buf[:n])
			packets = append(packets, pkt)
		}
	}

	if len(packets) < 2 {
		return CaptureResult{Source: "error", Warning: fmt.Sprintf("Domain %s does not support QUIC (UDP 443). Choose another domain.", domain)}
	}

	return CaptureResult{OK: true, Source: "quic", Packets: capturePacketsToCPS(packets)}
}

// capturePacketsToCPS converts the captured packet slice (I1 = the sent Initial,
// I2..I5 = server responses) to the CPS string slice used as amnezia I1-I5
// material. It is capped at captureMaxPkts entries and tolerant of a partial
// capture (fewer than captureMaxPkts packets) — the read loop breaks on
// timeout/loss, so packets may be short. Slicing packets[:captureMaxPkts] when
// len(packets) < captureMaxPkts would panic (slice out of range); min-clamp
// instead so a partial capture yields a partial CPS set, not a crash. Each
// packet is also clipped to captureMaxPacket bytes before CPS encoding.
func capturePacketsToCPS(packets [][]byte) []string {
	limit := len(packets)
	if limit > captureMaxPkts {
		limit = captureMaxPkts
	}
	cps := make([]string, 0, limit)
	for _, pkt := range packets[:limit] {
		if len(pkt) > captureMaxPacket {
			pkt = pkt[:captureMaxPacket]
		}
		cps = append(cps, CPSString(pkt, 0))
	}
	return cps
}
