package chain

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// This file ports the best AWG CPS / I1-I5 generators and helpers from
// https://github.com/pumbaX/awg-multi-script (awg2.sh, May 2026 best practices).
// QUIC Chrome-like 1200B Initial, realistic SIP REGISTER, DNS+EDNS0, full Pro ranges etc.
// All generation is pure-Go, no external python, suitable for orchestrator "head".

// Domain pools (curated for 2026 RU/IR/CN stealth from the script)
var (
	quicDomainPool = []string{
		"google.com", "github.com", "gitlab.com", "stackoverflow.com",
		"microsoft.com", "apple.com", "amazon.com",
		"mozilla.org", "cdn.jsdelivr.net", "unpkg.com", "pypi.org",
		"ubuntu.com", "debian.org", "hetzner.com", "ovhcloud.com",
		"digitalocean.com",
	}
	sipDomainPool = []string{
		"sipgate.de", "sip.ovh.net", "sip.voipfone.co.uk", "sip.linphone.org",
		"sip.zadarma.com", "sip.dus.net", "sip.easybell.de", "sip.1und1.de",
		"sip.voys.nl", "sip.antisip.com", "sip.iptel.org", "sip.voipgate.com",
	}
	dnsDomainPool = quicDomainPool // reuse for DNS
	sipUAPool    = []string{
		"Linphone/5.2.5 (belle-sip/5.2.0)",
		"Zoiper rv2.10.20.4",
		"MicroSIP/3.21.4",
		"Bria 6.5.1",
		"PortSIP UA 16.4",
	}
)

// cryptoRandInt returns uniform random int in [min, max] inclusive using crypto/rand.
func cryptoRandInt(min, max int) int {
	if min == max {
		return min
	}
	if max < min {
		min, max = max, min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		// Fallback (extremely rare): use time-based jitter
		return min + int(time.Now().UnixNano()%int64(max-min+1))
	}
	return min + int(n.Int64())
}

// randHex returns n random bytes as lowercase hex (no 0x).
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// randBytes returns n random bytes.
func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// u16BE returns big-endian 2-byte encoding.
func u16BE(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

// --- QUIC generators (exact ports of gen_quic_initial / gen_quic_short) ---

// GenerateQUICInitial produces a 1200-byte QUIC Long Header Initial packet
// mimicking Chrome (fb 0xC0/0xC3, DCID/SCID 8B, proper varint length, random payload).
// This is the strongest I1 for Russia 2026 per pumbaX research.
func GenerateQUICInitial(domain string) string {
	const target = 1200
	fb := []byte{0xC0, 0xC0, 0xC0, 0xC3}[cryptoRandInt(0, 3)]
	pnLen := int((fb & 0x03) + 1)

	dcid := randBytes(8)
	scid := randBytes(8)

	// header base exactly matches pumbaX reference Python: 26 bytes before varint+pn (fb+ver+dcidlen+dcid+scidlen+scid+tok0 + 2B varint)
	headerBase := 26
	encSize := target - headerBase - pnLen
	if encSize < 1 {
		encSize = 1
	}
	plenVal := pnLen + encSize
	// varint: 0x4000 | value for 2-byte form (simplified, matches script)
	plVarint := u16BE(0x4000 | uint16(plenVal))

	pn := randBytes(pnLen)
	payload := randBytes(encSize)

	pkt := append([]byte{fb}, []byte{0x00, 0x00, 0x00, 0x01}...)
	pkt = append(pkt, 0x08)
	pkt = append(pkt, dcid...)
	pkt = append(pkt, 0x08)
	pkt = append(pkt, scid...)
	pkt = append(pkt, 0x00)
	pkt = append(pkt, plVarint...)
	pkt = append(pkt, pn...)
	pkt = append(pkt, payload...)

	if len(pkt) < target {
		pkt = append(pkt, randBytes(target-len(pkt))...)
	} else if len(pkt) > target {
		pkt = pkt[:target]
	}
	return hex.EncodeToString(pkt)
}

// GenerateQUICShort produces a realistic QUIC Short Header (1-RTT) packet (I2-I5).
// Spin/key_phase/pn_len bits are randomized (after HP they look random to DPI).
func GenerateQUICShort() string {
	pnLen := cryptoRandInt(1, 4)
	spin := cryptoRandInt(0, 1) << 5
	keyp := cryptoRandInt(0, 1) << 2
	fb := byte(0x40 | spin | keyp | (pnLen - 1))

	dcid := randBytes(8)
	pn := randBytes(pnLen)
	data := randBytes(cryptoRandInt(40, 90))

	pkt := append([]byte{fb}, dcid...)
	pkt = append(pkt, pn...)
	pkt = append(pkt, data...)
	return hex.EncodeToString(pkt)
}

// --- SIP generator ---

func randPrivateIP() string {
	// Common private ranges seen in real SIP clients
	return fmt.Sprintf("192.168.%d.%d", cryptoRandInt(0, 255), cryptoRandInt(1, 254))
}

// GenerateSIP produces a full realistic SIP REGISTER (Linphone/Zoiper style)
// with User-Agent, Allow, Supported, Expires etc. Excellent for VoIP-mimicry I1.
func GenerateSIP() string {
	host := sipDomainPool[cryptoRandInt(0, len(sipDomainPool)-1)]
	user := []string{"alice", "bob", "100", "200", "sip", "user", "client"}[cryptoRandInt(0, 6)] +
		fmt.Sprintf("%d", cryptoRandInt(10, 9999))
	lip := randPrivateIP()
	lport := []int{5060, 5062, 5080, 5160, cryptoRandInt(10000, 65000)}[cryptoRandInt(0, 4)]
	branch := "z9hG4bK" + randHex(7)
	tag := randHex(4)
	callid := fmt.Sprintf("%s@%s", randHex(8), host)
	cseq := cryptoRandInt(1, 50)
	transport := []string{"udp", "udp", "udp", "udp", "tcp"}[cryptoRandInt(0, 4)]
	ua := sipUAPool[cryptoRandInt(0, len(sipUAPool)-1)]
	expires := []int{300, 600, 1800, 3600}[cryptoRandInt(0, 3)]

	lines := []string{
		fmt.Sprintf("REGISTER sip:%s SIP/2.0", host),
		fmt.Sprintf("Via: SIP/2.0/%s %s:%d;branch=%s;rport", strings.ToUpper(transport), lip, lport, branch),
		"Max-Forwards: 70",
		fmt.Sprintf("From: <sip:%s@%s>;tag=%s", user, host, tag),
		fmt.Sprintf("To: <sip:%s@%s>", user, host),
		fmt.Sprintf("Call-ID: %s", callid),
		fmt.Sprintf("CSeq: %d REGISTER", cseq),
		fmt.Sprintf("Contact: <sip:%s@%s:%d;transport=%s>", user, lip, lport, transport),
		fmt.Sprintf("User-Agent: %s", ua),
		"Allow: INVITE, ACK, CANCEL, BYE, REFER, OPTIONS, NOTIFY, SUBSCRIBE, PRACK, MESSAGE, INFO, UPDATE",
		"Supported: replaces, outbound, gruu, path",
		fmt.Sprintf("Expires: %d", expires),
		"Content-Length: 0",
		"",
		"",
	}
	return strings.Join(lines, "\r\n")
}

// --- DNS generator (with EDNS0) ---

// GenerateDNS produces a DNS A query + EDNS0 OPT-RR (modern resolver style).
// TXID is random. Compact and effective for many DPI scenarios.
func GenerateDNS(domain string) string {
	if domain == "" {
		domain = dnsDomainPool[cryptoRandInt(0, len(dnsDomainPool)-1)]
	}
	// TXID (2) + flags (2) + counts (8) + QNAME + QTYPE + QCLASS + OPT
	txid := randBytes(2)
	flags := []byte{0x01, 0x00} // QR=0, RD=1
	counts := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}

	qname := []byte{}
	for _, lbl := range strings.Split(domain, ".") {
		b := []byte(lbl)
		if len(b) > 63 {
			b = b[:63]
		}
		qname = append(qname, byte(len(b)))
		qname = append(qname, b...)
	}
	qname = append(qname, 0x00)

	qtype := []byte{0x00, 0x01}
	qclass := []byte{0x00, 0x01}

	udpSize := []uint16{1232, 4096}[cryptoRandInt(0, 1)]
	doBit := []uint16{0x0000, 0x8000}[cryptoRandInt(0, 1)]
	opt := append([]byte{0x00}, []byte{0x00, 0x29}...)
	opt = append(opt, u16BE(udpSize)...)
	opt = append(opt, []byte{0x00, 0x00}...)
	opt = append(opt, u16BE(doBit)...)
	opt = append(opt, []byte{0x00, 0x00}...)

	pkt := append(txid, flags...)
	pkt = append(pkt, counts...)
	pkt = append(pkt, qname...)
	pkt = append(pkt, qtype...)
	pkt = append(pkt, qclass...)
	pkt = append(pkt, opt...)

	return hex.EncodeToString(pkt)
}

// --- Serialization for sing-box amnezia section + native awg conf ---

// FormatI1 returns the string to put into "i1"/"i2"... in sing-box amnezia JSON
// or I1=... in native awg conf. Uses the <r N><b 0xHEX> form for DNS/SIP (TXID prefix),
// and raw hex for QUIC (1200B initial is already full packet, no extra TXID wrapper needed).
func FormatI1(mimicry, hexOrRaw string) string {
	switch mimicry {
	case "dns":
		// DNS: TXID randomizer prefix (first 2 bytes)
		return fmt.Sprintf("<r 2><b 0x%s>", hexOrRaw)
	case "sip":
		// SIP: plain hex payload (no TXID wrapper per pumbaX reference)
		return fmt.Sprintf("<b 0x%s>", hexOrRaw)
	default:
		// QUIC (and future): full packet as-is
		return hexOrRaw
	}
}

// GenerateCPS returns I1..I5 (as sing-box ready strings) + the mimicry used.
// level: 0=none, 1=only I1, 2=I1 + later randoms (compat), 3=full 5-packet chain.
// mimicry: "quic" (best for RU), "sip", "dns".
func GenerateCPS(level int, mimicry string) (i1, i2, i3, i4, i5, usedMimicry string) {
	if level <= 0 {
		return "", "", "", "", "", "none"
	}
	if mimicry == "" {
		mimicry = "quic"
	}
	usedMimicry = mimicry

	switch mimicry {
	case "quic":
		i1 = FormatI1("quic", GenerateQUICInitial(""))
		if level >= 2 {
			i2 = FormatI1("quic", GenerateQUICShort())
			i3 = FormatI1("quic", GenerateQUICShort())
			i4 = FormatI1("quic", GenerateQUICShort())
			i5 = FormatI1("quic", GenerateQUICShort())
		}
	case "sip":
		sipFull := GenerateSIP()
		i1 = FormatI1("sip", hex.EncodeToString([]byte(sipFull)))
		// SIP is large; for level>=3 we still only emit I1 (higher I* would bloat conf too much). Reference does the same.
		_ = level // higher levels for SIP intentionally produce only I1 in our port (matches script ergonomics)
	case "dns":
		i1 = FormatI1("dns", GenerateDNS(""))
		if level >= 2 {
			i2 = FormatI1("dns", GenerateDNS(""))
			i3 = FormatI1("dns", GenerateDNS(""))
			i4 = FormatI1("dns", GenerateDNS(""))
			i5 = FormatI1("dns", GenerateDNS(""))
		}
	default:
		i1 = FormatI1("quic", GenerateQUICInitial(""))
	}
	return
}

// EnforceAWGInvariants applies the script's S1+56 != S2 rule + Jmin < Jmax.
// Mutates the passed values (call with pointers or re-assign).
func EnforceAWGInvariants(jc, jmin, jmax, s1, s2, s3, s4, h1, h2, h3, h4 *int) {
	if *jmin >= *jmax {
		*jmax = *jmin + cryptoRandInt(100, 500)
	}
	// S1 + 56 != S2 (gap >= 10)
	gap := 10
	tries := 0
	for tries < 10 {
		diff := (*s1 + 56) - *s2
		if diff < 0 {
			diff = -diff
		}
		if diff >= gap {
			break
		}
		*s2 = cryptoRandInt(15, 150) // safe broad range, caller can narrow
		tries++
	}
	if (*s1+56) == *s2 {
		*s2 += gap
	}
	// H values are already huge unique quadrants from preset or generator.
	_ = h1
	_ = h2
	_ = h3
	_ = h4
	_ = jc
	_ = s3
	_ = s4
}
