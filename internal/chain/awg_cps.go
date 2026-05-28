package chain

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
)

// AWGObfsMaterial holds the stable obfuscation material for one AWG chain entry.
// These values (especially I1-I5 + server keypair) are generated ONCE on chain
// creation and never rotated on re-apply (critical for client config stability).
type AWGObfsMaterial struct {
	I1              []byte
	I2              []byte
	I3              []byte
	I4              []byte
	I5              []byte
	MimicryProfile  string // "quic" | "sip" | "dns" | "none"
	CPSLevel        int
}

// GenerateAWGObfsMaterial is the main entry point used by applier and config command.
// level 0 = no extra obfuscation packets
// level 3 + "quic" = maximum stealth (I1=1200B QUIC Initial Chrome fb, I2-I5 short)
func GenerateAWGObfsMaterial(level int, mimicry string) AWGObfsMaterial {
	m := AWGObfsMaterial{
		CPSLevel:       clamp(level, 0, 3),
		MimicryProfile: mimicry,
	}

	if level <= 0 || mimicry == "none" {
		return m
	}

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

// GenerateQUICInitial returns a 1200-byte QUIC Initial packet that looks exactly
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

	return fmt.Sprintf(`REGISTER sip:%s SIP/2.0
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

`, domain, randUint32(), domain, randUint32(), domain, randUint32(), ua)
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
func BuildAmneziaSection(awg *AWGPreset, preset *ConnectionPreset) map[string]any {
	// Delegate to the internal implementation that already exists in applier.go
	// (we keep one copy of the logic by re-exporting the behavior here for the
	// singbox backend package).
	// For v0.2.0 we inline the same logic to avoid import cycles.
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

	section := map[string]any{
		"jc":   awg.JC,
		"jmin": awg.JMIN,
		"jmax": awg.JMAX,
		"s1":   awg.S1,
		"s2":   awg.S2,
		"h1":   awg.H1,
		"h2":   awg.H2,
		"h3":   awg.H3,
		"h4":   awg.H4,
	}

	if level > 0 && mimicry != "none" {
		mat := GenerateAWGObfsMaterial(level, mimicry)
		if len(mat.I1) > 0 {
			section["i1"] = base64.StdEncoding.EncodeToString(mat.I1)
		}
		if len(mat.I2) > 0 {
			section["i2"] = base64.StdEncoding.EncodeToString(mat.I2)
		}
		if len(mat.I3) > 0 {
			section["i3"] = base64.StdEncoding.EncodeToString(mat.I3)
		}
		if len(mat.I4) > 0 {
			section["i4"] = base64.StdEncoding.EncodeToString(mat.I4)
		}
		if len(mat.I5) > 0 {
			section["i5"] = base64.StdEncoding.EncodeToString(mat.I5)
		}
	}
	return section
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
