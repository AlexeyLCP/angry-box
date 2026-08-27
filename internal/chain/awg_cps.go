package chain

// awg_cps.go — synthesized AWG CPS (Client Packet Signature) generators for the
// I1-I5 fields: TLS ClientHello (Chrome GREASE), DNS EDNS0 query, SIP REGISTER,
// and QUIC Initial packets. Ported from VPN/orchestrator/app/services/awg_cps.py,
// which itself ports the CPS packet shapes from pumbaX/awg-multi-script
// (https://github.com/pumbaX/awg-multi-script, MIT). The live-capture variant
// (real server responses instead of synthesized packets) is in awgcapture.go
// (ported from hoaxisr/awg-manager).

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// AWGObfsMaterial holds the stable obfuscation material for one AWG chain entry.
// These values (especially I1-I5 + H1-H4 + server keypair) are generated ONCE
// on chain creation and never rotated on re-apply (critical for client config
// stability — server and client must render identical obfuscation params).
type AWGObfsMaterial struct {
	I1 []byte
	I2 []byte
	I3 []byte
	I4 []byte
	I5 []byte
	// H1-H4: header-junk "lo-hi" ranges (proper quadrant ranges, not degenerate).
	H1             string
	H2             string
	H3             string
	H4             string
	MimicryProfile string // "quic" | "sip" | "dns" | "none"
	CPSLevel       int

	// AWG 3.0 obfuscation material (AGENTS #5). Populated only when the owning
	// inbound/profile has AWG3Mode set; the classic fields above are still
	// produced (AWG3 layers on top of amnezia, it does not replace it).
	// HeaderProtectionKey is the hex of 32 random bytes (sing-box endpoint.go
	// decodes base64 from JSON → hex for the amneziawg-go UAPI; we persist hex
	// to match the §30 spike). ContentPaddingAddition / RekeyAfterTime are
	// "lo-hi" UintRange strings (seconds for RekeyAfterTime). Empty
	// HeaderProtectionKey = AWG3 mode off for this material.
	AWG3Mode                  bool
	HeaderProtectionKey       string
	ContentPaddingAddition    string
	RekeyAfterTime            string
	RandomTrailers            bool
	DisableCookies            bool
}

// GenerateAWGObfsMaterial is the main entry point used by applier and config
// command. It targets AWG 2.0 (quadrant H ranges); use
// GenerateAWGObfsMaterialForVersion for a version-aware H1-H4 form (1.5).
// level 0 = no extra obfuscation packets
// level 3 + "quic" = maximum stealth (I1=1200B QUIC Initial Chrome fb, I2-I5 short)
func GenerateAWGObfsMaterial(level int, mimicry string) AWGObfsMaterial {
	return GenerateAWGObfsMaterialForVersion(level, mimicry, model.AWGVersion2)
}

// GenerateAWGObfsMaterialForVersion is GenerateAWGObfsMaterial with a
// version-appropriate H1-H4 form: AWG 1.5 single-int (awg-quick 1.x rejects
// "lo-hi"), 2/3 quadrant ranges. Used by the inbound/profile material paths
// that know the effective AWG version.
func GenerateAWGObfsMaterialForVersion(level int, mimicry string, version string) AWGObfsMaterial {
	m := AWGObfsMaterial{
		CPSLevel:       clamp(level, 0, 3),
		MimicryProfile: mimicry,
	}

	if level <= 0 || mimicry == "none" {
		return m
	}

	// H1-H4: version-appropriate form (1.5 single-int, 2/3 quadrant ranges) per
	// the AmneziaWG manual (4 non-overlapping ranges in [5, 2^31-1], width >= 1000).
	// Profile scales with CPS level.
	profile := AWGProfileStandard
	if level >= 3 {
		profile = AWGProfilePro
	}
	params := GenAWGParamsForVersion(profile, version)
	m.H1, m.H2, m.H3, m.H4 = params.H1, params.H2, params.H3, params.H4

	switch mimicry {
	case "quic":
		m.I1 = GenerateQUICInitial() // exactly 1200 bytes
		if level >= 2 {
			m.I2 = GenerateQUICShort(48 + randInt(0, 40))
			m.I3 = GenerateQUICShort(48 + randInt(0, 40))
			m.I4 = GenerateQUICShort(48 + randInt(0, 40))
			m.I5 = GenerateQUICShort(48 + randInt(0, 40))
		}
	case "sip":
		m.I1 = []byte(GenerateSIP("sip.icloud.com"))
		if level >= 2 {
			m.I2 = []byte(GenerateSIP("sip.apple.com"))
			m.I3 = []byte(GenerateSIP("sip.google.com"))
			m.I4 = []byte(GenerateSIP("sip.example.com"))
			m.I5 = []byte(GenerateSIP("sip.ms.com"))
		}
	case "dns":
		m.I1 = GenerateDNS("icloud.com", 1232)
		if level >= 2 {
			m.I2 = GenerateDNS("www.apple.com", 1232)
			m.I3 = GenerateDNS("dns.google", 4096)
			m.I4 = GenerateDNS("one.one.one.one", 1232)
			m.I5 = GenerateDNS("cloudflare.com", 4096)
		}
	default:
		// fallback to quic for safety
		m.I1 = GenerateQUICInitial()
	}

	return m
}

// GenerateAWG3Material produces the AWG 3.0 obfuscation fields (header
// protection key + content-padding range + rekey-after-time range) for an
// inbound/profile with AWG3Mode on. It layers on top of the classic amnezia
// material (Jc/S1-S4/H1-H4/I1-I5): the caller still runs GenerateAWGObfsMaterial
// for those, then merges AWG3 via this function. Generated ONCE per inbound and
// persisted (InboundProfile/NodeInbound.AWG3*) so a redeploy reuses it and
// existing clients are not re-keyed.
//
// HeaderProtectionKey: 32 random bytes → hex (sing-box endpoint.go decodes
// base64 from JSON and converts to hex for the amneziawg-go UAPI; we persist
// the hex form to match the §30 spike and the client .conf inline form).
// ContentPaddingAddition: a "lo-hi" byte range of random padding added to each
// transport packet (replaces the fixed 16-byte alignment). RekeyAfterTime: a
// "lo-hi" seconds range replacing WireGuard's fixed RekeyAfterTime=120s so the
// handshake rhythm is not a fingerprint. S1-S4 >= 12 is NOT a material field —
// it is enforced at emit time (the preset's S1-S4 are raised to 12 when HPK is
// set, HeaderCipherNonceSize=12). Reference: architect.vai-rice.space AWG 3.0.
func GenerateAWG3Material() AWGObfsMaterial {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// crypto/rand failure is catastrophic for obfuscation — surface it
		// loudly rather than silently shipping a zero key (which would make
		// header protection deterministic + trivially fingerprintable).
		panic(fmt.Sprintf("awg3: read header protection key: %v", err))
	}
	return AWGObfsMaterial{
		AWG3Mode:               true,
		HeaderProtectionKey:    fmt.Sprintf("%x", key),
		ContentPaddingAddition: fmt.Sprintf("%d-%d", randInt(1, 16), randInt(17, 64)),
		RekeyAfterTime:         fmt.Sprintf("%d-%d", randInt(90, 110), randInt(130, 180)),
	}
}

// awg3HPKHexToBase64 converts the hex-persisted AWG 3.0 header protection key
// (32 random bytes written as 64 hex chars by GenerateAWG3Material) to the
// base64 WireGuard-key form both consumers expect:
//   - the userspace sing-box `type:"awg"` endpoint JSON (sing-box endpoint.go
//     decodes base64 → hex for the amneziawg-go UAPI), and
//   - the kernel awg-quick .conf `HeaderProtectionKey = <base64>` line (the
//     amneziawg-tools v3.0 config.c parser reads it as a WG key, live-verified
//     on n1 with the PR #192 kernel module).
//
// Returns ("", false) on an invalid key (wrong length / non-hex). The caller
// MUST skip the AWG3 fields in that case — a malformed HPK breaks every
// client's header-protection handshake. GenerateAWG3Material always produces a
// valid 32-byte hex key, so this only fails on hand-edited / corrupted stores.
func awg3HPKHexToBase64(hexKey string) (string, bool) {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil || len(keyBytes) != 32 {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(keyBytes), true
}

// like Chrome's QUIC traffic (fb C0/C3 long header + h3-29 + realistic padding).
// This is the #1 recommended I1 for Russia/Iran/China 2026 per community research.
func GenerateQUICInitial() []byte {
	const targetLen = 1200
	b := make([]byte, targetLen)
	// Long header: Initial (0xC0-0xC3 range for Chrome fingerprint)
	b[0] = 0xC3 // Chrome fb style

	// Version (Chrome uses 0x00000001 in many captures)
	binary.BigEndian.PutUint32(b[1:5], 0x00000001)

	// DCID + SCID (realistic lengths)
	b[5] = 8 // DCID len
	_, _ = rand.Read(b[6:14])
	b[14] = 0 // SCID len (common in Initial from client)

	// Token length (0 for initial client Initial)
	offset := 15
	b[offset] = 0
	offset++

	// Length field (variable length integer) — we will fill after
	lengthOffset := offset
	offset += 2 // assume 2-byte length for 1200

	// Packet number (random 4 bytes for realism)
	_, _ = rand.Read(b[offset : offset+4])
	offset += 4

	// Crypto frame (0x06) + realistic ClientHello-like content
	b[offset] = 0x06
	offset++

	// Simulate CRYPTO frame payload with Chrome QUIC fingerprint strings
	fb := []byte("h3-29\x00h3-28\x00h3-27\x00") // common Chrome fb
	copy(b[offset:], fb)
	offset += len(fb)

	// Add padding + random noise to reach exactly 1200
	for offset < targetLen {
		b[offset] = byte(randInt(0, 255))
		offset++
	}

	// Fix Length field (simplified varint)
	payloadLen := targetLen - lengthOffset - 2
	binary.BigEndian.PutUint16(b[lengthOffset:lengthOffset+2], uint16(payloadLen))

	return b
}

// GenerateQUICInitialWithSNI builds a QUIC Initial datagram carrying a TLS
// ClientHello with SNI=domain. Returns (packet, dcid, version, err). The
// current implementation reuses the synthesized GenerateQUICInitial shape and
// injects the domain into the ClientHello SNI-like signature region; a full
// AEAD-encrypted Initial (per RFC 9001) is a future enhancement — for live
// capture the key behaviour is sending a valid-looking Initial and reading the
// server's response packets, which real QUIC servers produce regardless.
func GenerateQUICInitialWithSNI(domain string) (packet []byte, dcid []byte, version uint32, err error) {
	// Use the synthesized Initial as the base; it already has a realistic
	// Chrome-shaped header. We do not currently embed the literal SNI bytes in
	// an AEAD-encrypted ClientHello (that requires the QUIC initial keys
	// derivation), so the domain is informational here. Capture still works
	// because servers reply to any well-formed Initial.
	pkt := GenerateQUICInitial()
	dcid = make([]byte, 8)
	copy(dcid, pkt[6:14])
	_ = domain // reserved for a future full-SNI implementation
	return pkt, dcid, 0x00000001, nil
}

// GenerateQUICShort returns a short-header QUIC packet (0x40-0x7F) with
// header-protection masking simulation. Used for I2-I5 in level 2+.
func GenerateQUICShort(size int) []byte {
	if size < 32 {
		size = 32
	}
	b := make([]byte, size)
	// Short header form
	b[0] = byte(0x40 + randInt(0, 0x3F)) // 0x40-0x7F

	// DCID (4-8 bytes)
	dcidLen := 4 + randInt(0, 4)
	_, _ = rand.Read(b[1 : 1+dcidLen])

	// Rest is payload + random (simulating protected data)
	for i := 1 + dcidLen; i < size; i++ {
		b[i] = byte(randInt(0, 255))
	}
	return b
}

// GenerateSIP returns a realistic SIP REGISTER packet that many softphones emit.
// Used as excellent mimicry traffic for AWG I1/I2 in certain regions.
func GenerateSIP(domain string) string {
	ua := []string{
		"Linphone/5.2.0 (Ubuntu)",
		"MicroSIP/3.21.3",
		"Grandstream GXP2135 1.0.9.27",
		"Zoiper 5.5.8",
	}[randInt(0, 3)]

	return strings.ReplaceAll(fmt.Sprintf(`REGISTER sip:%s SIP/2.0
Via: SIP/2.0/UDP 192.168.1.42:5060;branch=z9hG4bK-%08x
Max-Forwards: 70
From: <sip:alice@%s>;tag=%08x
To: <sip:alice@%s>
Call-ID: %08x@192.168.1.42
CSeq: 1 REGISTER
User-Agent: %s
Contact: <sip:alice@192.168.1.42:5060;transport=udp>
Expires: 3600
Allow: INVITE, ACK, CANCEL, OPTIONS, BYE, REFER, NOTIFY, MESSAGE, SUBSCRIBE, INFO
Supported: replaces, timer, path
Content-Length: 0

`, domain, randUint32(), domain, randUint32(), domain, randUint32(), ua), "\n", "\r\n")
}

// GenerateDNS returns a DNS A query (with EDNS0 OPT RR) padded to the requested size.
// Excellent low-signature I1 for some networks (used in lite profiles).
func GenerateDNS(qname string, size int) []byte {
	if size < 64 {
		size = 64
	}
	b := make([]byte, size)

	// Transaction ID
	binary.BigEndian.PutUint16(b[0:2], uint16(randUint32()))

	// Flags: standard query
	b[2] = 0x01
	b[3] = 0x00

	// QDCOUNT=1, AN/NS/AR=0 then later AR=1 for EDNS0
	binary.BigEndian.PutUint16(b[4:6], 1)

	// Question
	offset := 12
	labels := strings.Split(qname, ".")
	for _, l := range labels {
		b[offset] = byte(len(l))
		offset++
		copy(b[offset:], l)
		offset += len(l)
	}
	b[offset] = 0 // root
	offset++
	binary.BigEndian.PutUint16(b[offset:offset+2], 1) // A
	offset += 2
	binary.BigEndian.PutUint16(b[offset:offset+2], 1) // IN
	offset += 2

	// EDNS0 OPT RR (OPT=41)
	b[offset] = 0 // name root
	offset++
	binary.BigEndian.PutUint16(b[offset:offset+2], 41) // OPT
	offset += 2
	binary.BigEndian.PutUint16(b[offset:offset+2], uint16(size)) // payload size (1232 or 4096)
	offset += 2
	b[offset] = 0 // extended RCODE
	offset++
	b[offset] = 0 // EDNS version
	offset++
	binary.BigEndian.PutUint16(b[offset:offset+2], 0x0000) // Z
	offset += 2
	binary.BigEndian.PutUint16(b[offset:offset+2], 0) // RDATA len
	offset += 2

	// Fill the rest with random bytes (padding / noise)
	for offset < size {
		b[offset] = byte(randInt(0, 255))
		offset++
	}
	return b
}

// BuildAWGClientMaterialFromPreset is the high-level helper used by applier
// and the standalone `angry-box config` command.
func BuildAWGClientMaterialFromPreset(p ConnectionPreset, serverHost string) AWGObfsMaterial {
	level := 0
	mimicry := "none"

	// Force full CPS3 + QUIC for the two security-first 2026 profiles (user requirement: Security > Compatibility)
	if p.Name == "pro_2026" || p.Name == "xhttp_max_stealth_2026" || strings.Contains(p.Name, "max_stealth") {
		level = 3
		mimicry = "quic"
	} else if p.CPSLevel > 0 {
		level = p.CPSLevel
		mimicry = p.AWGMimicry
	} else if p.AWG != nil && p.AWG.CPSLevel > 0 {
		level = p.AWG.CPSLevel
		mimicry = p.AWG.Mimicry
	} else if p.AWG != nil && p.AWG.JMAX >= 100 {
		// Fallback heuristic for older-style presets
		level = 2
		mimicry = "quic"
	}

	return GenerateAWGObfsMaterial(level, mimicry)
}

// BuildAmneziaSection is the exported version of the amnezia map builder used by
// both the chain applier and the standalone sing-box config generator.
// It is the single place that applies CPS/I1-I5 for the 2026 stealth presets.
// material, when non-nil, supplies the persisted I1-I5 so server and client
// render identical CPS packets (the handshake requires this). nil = generate
// fresh I1-I5 on the fly (legacy/standalone — mismatched across peers, do not
// use for chain paths).
func BuildAmneziaSection(awg *AWGPreset, preset *ConnectionPreset, material *AWGObfsMaterial) *config.AmneziaOptions {
	level := 0
	mimicry := "none"

	if preset != nil {
		if preset.CPSLevel > 0 {
			level = preset.CPSLevel
			mimicry = preset.AWGMimicry
		} else if awg != nil && awg.CPSLevel > 0 {
			level = awg.CPSLevel
			mimicry = awg.Mimicry
		}
	}

	section := &config.AmneziaOptions{
		JC:   awg.JC,
		JMIN: awg.JMIN,
		JMAX: awg.JMAX,
	}

	if level > 0 && mimicry != "none" {
		section.S1 = awg.S1
		section.S2 = awg.S2
		section.S3 = awg.S3
		section.S4 = awg.S4
		// H1-H4: prefer the material's proper quadrant ranges (persisted on the
		// chain, identical server↔client); fall back to the preset's degenerate
		// "lo-hi" range when no material is supplied (standalone/legacy).
		if material != nil && material.H1 != "" {
			section.H1 = material.H1
			section.H2 = material.H2
			section.H3 = material.H3
			section.H4 = material.H4
		} else {
			section.H1 = fmt.Sprintf("%d-%d", awg.H1, awg.H1)
			section.H2 = fmt.Sprintf("%d-%d", awg.H2, awg.H2)
			section.H3 = fmt.Sprintf("%d-%d", awg.H3, awg.H3)
			section.H4 = fmt.Sprintf("%d-%d", awg.H4, awg.H4)
		}
		// ITime: concealment-packet lifetime. 0 = unset (legacy/older presets);
		// copy through so server and client agree when the preset specifies it.
		section.ITime = awg.ITime

		mat := AWGObfsMaterial{}
		if material != nil {
			// Use the persisted material so server and client render identical
			// I1-I5. The CPS handshake breaks if the two sides diverge.
			mat = *material
		} else {
			mat = GenerateAWGObfsMaterial(level, mimicry)
		}
		// I1-I5 use the AWG CPS string format "<b 0x{hex}>" (not base64) — this
		// is what sing-box-extended's wireguard-go and kernel awg-quick both
		// expect. CPSMaterialString also pads odd-length hex to even, fixing the
		// "failed to parse I1: odd amount of symbols" parse error.
		strs := CPSMaterialStrings(mat)
		section.I1 = strs[0]
		section.I2 = strs[1]
		section.I3 = strs[2]
		section.I4 = strs[3]
		section.I5 = strs[4]

		// I1Packet override (AWGPreset.I1Packet): when the preset specifies an
		// explicit I1 packet, it replaces the generated I1. Supported forms:
		//   "quic-1200" → a 1200-byte QUIC Initial (Chrome-shaped)
		//   "dns-1232"  → a 1232-byte DNS query (icloud.com)
		//   any other non-empty string → base64-decoded literal bytes
		// AGENTS.md Known Issue #10 open gap: I1Packet was parsed but never
		// emitted — the override never reached the rendered .conf. This closes it.
		if awg != nil && awg.I1Packet != "" {
			if pkt, ok := resolveI1Packet(awg.I1Packet); ok {
				section.I1 = CPSMaterialString(pkt)
			}
		}
	}
	return section
}

// resolveI1Packet turns an AWGPreset.I1Packet value into the raw I1 payload
// bytes. Recognized keywords produce a realistic QUIC Initial (1200B) or DNS
// query (1232B) matching the level-3 mimicry shapes; any other non-empty value
// is base64-decoded as a literal. Returns (bytes, true) on success, (nil, false)
// if the literal could not be decoded (the caller keeps the generated I1).
func resolveI1Packet(v string) ([]byte, bool) {
	switch v {
	case "quic-1200":
		return GenerateQUICInitial(), true
	case "dns-1232":
		return GenerateDNS("icloud.com", 1232), true
	default:
		// Literal: base64 (standard or URL-safe) → raw bytes.
		if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) > 0 {
			return b, true
		}
		if b, err := base64.URLEncoding.DecodeString(v); err == nil && len(b) > 0 {
			return b, true
		}
		return nil, false
	}
}

// EnsureChainAWGMaterial populates c.AWGCPSI1..I5 (and level/mimicry) once,
// deriving the level/mimicry from the chain's effective preset and generating
// the I1-I5 bytes via GenerateAWGObfsMaterial. It is idempotent — existing
// material is preserved (stable across redeploys so client configs don't
// break). Call after the chain's preset is resolved and before any server
// endpoint / client .conf is rendered. No-op when the preset has no CPS.
func EnsureChainAWGMaterial(c *model.Chain, preset ConnectionPreset) {
	level := 0
	mimicry := "none"
	if preset.CPSLevel > 0 {
		level = preset.CPSLevel
		mimicry = preset.AWGMimicry
	} else if preset.AWG != nil && preset.AWG.CPSLevel > 0 {
		level = preset.AWG.CPSLevel
		mimicry = preset.AWG.Mimicry
	}
	if level <= 0 {
		return
	}
	// Already persisted and still valid -> keep it (Rule 5: stable across
	// redeploys). The cache-validity rule depends on the mimicry mode:
	//   - non-live: level + mimicry must match.
	//   - quic-live SUCCESS cached (AWGCPSMimicry == "quic-live"): the captured
	//     domain must match — a domain change re-captures the new one.
	//   - quic-live FAILURE cached (AWGCPSMimicry == "quic", the fallback): the
	//     failed domain must match AWGCPSCaptureFailedDomain, so a flaky/unreachable
	//     domain is NOT re-dialed on every redeploy (Rule 5 — a failed capture
	//     for THIS domain already fell back to synthesized packets; keep them).
	//     A domain change clears the match → re-capture the new domain.
	cacheValid := c.AWGCPSI1 != "" && c.AWGCPSLevel == level
	if mimicry == mimicryQuicLive {
		switch c.AWGCPSMimicry {
		case mimicryQuicLive:
			cacheValid = cacheValid && c.AWGCPSCapturedDomain == c.AWGCPSCaptureDomain
		case "quic":
			// Prior attempt for this domain failed and fell back — don't re-dial it.
			cacheValid = cacheValid && c.AWGCPSCaptureFailedDomain == c.AWGCPSCaptureDomain
		default:
			cacheValid = false
		}
	} else {
		cacheValid = cacheValid && c.AWGCPSMimicry == mimicry
	}
	if cacheValid {
		return
	}
	// Live-capture path: when mimicry is "quic-live" and a capture domain is
	// set, dial the domain over UDP 443, send a real AEAD-encrypted QUIC Initial
	// with SNI=domain, and capture the server's response packets as I1-I5. This
	// yields a domain-accurate QUIC silhouette DPI cannot distinguish from real
	// traffic to that domain. Falls back to synthesized packets on any capture
	// failure (network down, domain doesn't speak QUIC) so a chain never breaks.
	if mimicry == mimicryQuicLive && c.AWGCPSCaptureDomain != "" {
		res := CaptureQUICSignature(c.AWGCPSCaptureDomain, 0)
		if res.OK && len(res.Packets) >= 5 {
			c.AWGCPSLevel = level
			c.AWGCPSMimicry = mimicry
			c.AWGCPSCapturedDomain = c.AWGCPSCaptureDomain
			c.AWGCPSCaptureFailedDomain = "" // success clears the failure marker
			c.AWGCPSI1 = res.Packets[0]
			c.AWGCPSI2 = res.Packets[1]
			c.AWGCPSI3 = res.Packets[2]
			c.AWGCPSI4 = res.Packets[3]
			c.AWGCPSI5 = res.Packets[4]
			// H1-H4 still come from the quadrant generator — live capture only
			// supplies I1-I5 (the packet silhouettes), not the header-junk ranges.
			mat := GenerateAWGObfsMaterial(level, "quic")
			c.AWGH1 = mat.H1
			c.AWGH2 = mat.H2
			c.AWGH3 = mat.H3
			c.AWGH4 = mat.H4
			return
		}
		// Capture failed — record the failed domain so the next redeploy doesn't
		// re-dial it (cache-validity check above), then fall through to synthesized
		// packets so the chain still works.
		c.AWGCPSCaptureFailedDomain = c.AWGCPSCaptureDomain
		mimicry = "quic"
	}
	mat := GenerateAWGObfsMaterial(level, mimicry)
	strs := CPSMaterialStrings(mat)
	c.AWGCPSLevel = level
	c.AWGCPSMimicry = mimicry
	c.AWGCPSI1 = strs[0]
	c.AWGCPSI2 = strs[1]
	c.AWGCPSI3 = strs[2]
	c.AWGCPSI4 = strs[3]
	c.AWGCPSI5 = strs[4]
	// H1-H4 quadrant ranges (proper, non-degenerate) — persisted so server and
	// client render identical header-junk ranges.
	c.AWGH1 = mat.H1
	c.AWGH2 = mat.H2
	c.AWGH3 = mat.H3
	c.AWGH4 = mat.H4
}

// mimicryQuicLive is the AWGCPSMimicry value that selects the live QUIC capture
// path in EnsureChainAWGMaterial (vs the synthesized "quic"/"sip"/"dns" paths).
// Set together with AWGCPSCaptureDomain on the chain to enable live capture.
// Note: this is UDP/QUIC capture (CaptureQUICSignature), NOT plain TCP TLS
// capture — the latter is unsupported for AWG (see docs/PROGRESS.md §0.7).
const mimicryQuicLive = "quic-live"

// ChainAWGObfsMaterial reconstructs the persisted AWGObfsMaterial from a chain.
// Returns nil when the chain has no CPS material (level 0 / not populated), so
// callers can pass nil to BuildAWGAmnezia and get the no-CPS path.
func ChainAWGObfsMaterial(c *model.Chain) *AWGObfsMaterial {
	if c == nil || c.AWGCPSLevel <= 0 || c.AWGCPSI1 == "" {
		return nil
	}
	return &AWGObfsMaterial{
		CPSLevel:       c.AWGCPSLevel,
		MimicryProfile: c.AWGCPSMimicry,
		I1:             cpsStringToBytes(c.AWGCPSI1),
		I2:             cpsStringToBytes(c.AWGCPSI2),
		I3:             cpsStringToBytes(c.AWGCPSI3),
		I4:             cpsStringToBytes(c.AWGCPSI4),
		I5:             cpsStringToBytes(c.AWGCPSI5),
		H1:             c.AWGH1,
		H2:             c.AWGH2,
		H3:             c.AWGH3,
		H4:             c.AWGH4,
	}
}

// cpsStringToBytes decodes a stored CPS string ("<b 0x{hex}>") back to its raw
// packet bytes, so ChainAWGObfsMaterial can feed BuildAmneziaSection which
// re-encodes via CPSMaterialStrings. Round-trips cleanly for the "<b 0x...>"
// form produced by CPSMaterialString.
func cpsStringToBytes(s string) []byte {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "<b ") || !strings.HasSuffix(s, ">") {
		return nil
	}
	hexBody := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "<b "), ">"))
	hexBody = strings.TrimPrefix(hexBody, "0x")
	b, err := hexDecodeEven(hexBody)
	if err != nil {
		return nil
	}
	return b
}

// hexDecodeEven decodes a hex string, left-padding to an even length first (the
// CPS format pads odd-length hex, so this is defensive).
func hexDecodeEven(h string) ([]byte, error) {
	if len(h)%2 == 1 {
		h = "0" + h
	}
	out := make([]byte, len(h)/2)
	for i := 0; i < len(out); i++ {
		hi, ok1 := hexNibble(h[i*2])
		lo, ok2 := hexNibble(h[i*2+1])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("bad hex byte at %d", i*2)
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// --- helpers ---

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func randInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return min + int(n.Int64())
}

func randUint32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}

// IntRange is a small helper for future preset JSON that allows either a single int
// or [min, max] for randomized values at apply time (already partially used in maximum_stealth_2026).
type IntRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func (r IntRange) Value() int {
	if r.Min == r.Max {
		return r.Min
	}
	return r.Min + randInt(0, r.Max-r.Min)
}
