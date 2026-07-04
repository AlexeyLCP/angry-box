package chain

// quic_initial_aead.go — builds a real, AEAD-encrypted QUIC v1 Initial packet
// carrying a genuine TLS 1.3 ClientHello with SNI=domain. Ported from
// hoaxisr/awg-manager (internal/signature/capture.go, MIT) with attribution;
// this file is part of Angry-box, distributed under the project's PolyForm
// Noncommercial license (see /LICENSE).
//
// Why: the previous live-capture path reused the synthesized
// GenerateQUICInitialWithSNI — a shape-fake with no real QUIC cryptography.
// Some QUIC servers drop packets whose Initial isn't properly AEAD-encrypted
// per RFC 9001, so the capture got no response. A real Initial (correct HKDF
// key derivation from the DCID, AES-128-GCM-encrypted CRYPTO frame carrying a
// real ClientHello, header-protection mask applied) makes every QUIC server
// reply — yielding genuine I1-I5 packets indistinguishable from real Chrome
// traffic to that domain.
//
// Encryption follows RFC 9001 §5.2: Initial keys are derived from the DCID via
// HKDF-Extract(initial_salt, DCID) + HKDF-Expand-Label; the payload (CRYPTO
// frame + padding) is AES-128-GCM-sealed with the header as AAD; the header is
// then masked (AES-ECB of a ciphertext sample, RFC 9001 §5.4).

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

// quicV1InitialSalt is the QUIC v1 Initial salt (RFC 9001 §5.2).
var quicV1InitialSalt = mustHexDecode("38762cf7f55934b34d179ae6a4c80cadccbb7f0a")

func mustHexDecode(s string) []byte {
	b := make([]byte, len(s)/2)
	for i := range b {
		fmt.Sscanf(s[i*2:i*2+2], "%x", &b[i])
	}
	return b
}

// buildQUICInitialAEAD constructs a properly AEAD-encrypted QUIC v1 Initial
// packet containing a real TLS 1.3 ClientHello with SNI=domain. Returns the
// full datagram (ready to send over UDP) and the DCID used (for diagnostics).
func buildQUICInitialAEAD(domain string) ([]byte, []byte, error) {
	clientHello, err := buildTLSClientHello(domain)
	if err != nil {
		return nil, nil, fmt.Errorf("build TLS ClientHello: %w", err)
	}
	// Strip the TLS record header (5 bytes: type + version + length) — the QUIC
	// CRYPTO frame carries the ClientHello handshake message, not the record.
	if len(clientHello) < 5 {
		return nil, nil, fmt.Errorf("ClientHello too short: %d bytes", len(clientHello))
	}
	chPayload := clientHello[5:]

	// Random DCID (8 bytes — the length Chrome uses for Initial).
	dcid := make([]byte, 8)
	if _, err := rand.Read(dcid); err != nil {
		return nil, nil, err
	}

	clientKey, clientIV, clientHP, err := deriveInitialKeys(dcid)
	if err != nil {
		return nil, nil, fmt.Errorf("derive initial keys: %w", err)
	}

	// CRYPTO frame: type(0x06) + offset(0x00, varint) + length(varint) + ClientHello.
	cryptoFrame := []byte{0x06, 0x00}
	cryptoFrame = append(cryptoFrame, quicVarint(len(chPayload))...)
	cryptoFrame = append(cryptoFrame, chPayload...)

	// Packet number = 0 (4 bytes). QUIC Initial starts at PN 0.
	pktNum := []byte{0x00, 0x00, 0x00, 0x00}
	pktNumLen := len(pktNum)

	// Pad the plaintext (CRYPTO frame + padding) so the whole packet is 1200
	// bytes — QUIC Initial datagrams must be >=1200 to be well-formed.
	headerSizeEstimate := 1 + 4 + 1 + len(dcid) + 1 + 1 + 2 + pktNumLen
	minPayloadBytes := 1200 - headerSizeEstimate - 16 // minus AEAD tag
	plaintext := cryptoFrame
	if len(plaintext) < minPayloadBytes {
		plaintext = append(plaintext, make([]byte, minPayloadBytes-len(plaintext))...)
	}

	// AES-128-GCM encrypt the payload with the header as AAD.
	block, err := aes.NewCipher(clientKey)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	// Nonce = IV XOR packet number (PN is 0, so nonce = IV padded to 12 bytes).
	nonce := make([]byte, 12)
	copy(nonce, clientIV)
	for i := 0; i < pktNumLen; i++ {
		nonce[12-pktNumLen+i] ^= pktNum[i]
	}

	// Build the unprotected header (used as AAD).
	var header []byte
	header = append(header, 0xC3)                   // Long Header + Fixed + Initial + PN length 4
	header = append(header, 0x00, 0x00, 0x00, 0x01) // QUIC v1
	header = append(header, byte(len(dcid)))
	header = append(header, dcid...)
	header = append(header, 0x00) // SCID length = 0
	header = append(header, 0x00) // Token length = 0
	lengthVal := pktNumLen + len(plaintext) + gcm.Overhead()
	header = append(header, quicVarint(lengthVal)...)
	header = append(header, pktNum...)

	ciphertext := gcm.Seal(nil, nonce, plaintext, header)

	// Header protection (RFC 9001 §5.4): sample = first 16 bytes of ciphertext
	// (PN is 4 bytes, so the sample starts at offset 0 of the ciphertext after
	// the PN would be — but since PN=0 and is the last header bytes, the sample
	// is the first 16 bytes of the encrypted payload).
	sample := ciphertext[0:16]
	hpBlock, err := aes.NewCipher(clientHP)
	if err != nil {
		return nil, nil, err
	}
	mask := make([]byte, aes.BlockSize)
	hpBlock.Encrypt(mask, sample)

	protectedHeader := make([]byte, len(header))
	copy(protectedHeader, header)
	// Long header: mask the lower 4 bits of the first byte.
	protectedHeader[0] ^= mask[0] & 0x0F
	// Mask the packet-number bytes.
	pnOffset := len(header) - pktNumLen
	for i := 0; i < pktNumLen; i++ {
		protectedHeader[pnOffset+i] ^= mask[1+i]
	}

	pkt := append(protectedHeader, ciphertext...)
	return pkt, dcid, nil
}

// deriveInitialKeys derives the client Initial key, IV, and HP key from a DCID
// per RFC 9001 §5.2: initial_secret = HKDF-Extract(initial_salt, DCID), then
// client_initial_secret = HKDF-Expand-Label(initial_secret, "client in", "", 32),
// then key/iv/hp via Expand-Label.
func deriveInitialKeys(dcid []byte) (key, iv, hp []byte, err error) {
	initialSecret := hkdfExtract(quicV1InitialSalt, dcid)
	clientSecret := hkdfExpandLabel(initialSecret, "client in", nil, 32)
	key = hkdfExpandLabel(clientSecret, "quic key", nil, 16)
	iv = hkdfExpandLabel(clientSecret, "quic iv", nil, 12)
	hp = hkdfExpandLabel(clientSecret, "quic hp", nil, 16)
	return key, iv, hp, nil
}

// hkdfExtract performs HKDF-Extract (RFC 5869) with HMAC-SHA256.
func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpandLabel performs HKDF-Expand-Label (TLS 1.3, RFC 8446 §7.1). The
// "tls13 " label prefix is added automatically.
func hkdfExpandLabel(secret []byte, label string, context []byte, length int) []byte {
	fullLabel := "tls13 " + label
	var hkdfLabel []byte
	hkdfLabel = append(hkdfLabel, byte(length>>8), byte(length))
	hkdfLabel = append(hkdfLabel, byte(len(fullLabel)))
	hkdfLabel = append(hkdfLabel, []byte(fullLabel)...)
	hkdfLabel = append(hkdfLabel, byte(len(context)))
	hkdfLabel = append(hkdfLabel, context...)
	return hkdfExpand(secret, hkdfLabel, length)
}

// hkdfExpand performs HKDF-Expand (RFC 5869) with HMAC-SHA256.
func hkdfExpand(prk, info []byte, length int) []byte {
	hashLen := sha256.Size
	n := (length + hashLen - 1) / hashLen
	var out, prev []byte
	for i := 1; i <= n; i++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{byte(i)})
		prev = mac.Sum(nil)
		out = append(out, prev...)
	}
	return out[:length]
}

// quicVarint encodes an integer as a QUIC variable-length integer (RFC 9000 §16).
func quicVarint(v int) []byte {
	if v < 64 {
		return []byte{byte(v)}
	}
	if v < 16384 {
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(v)|0x4000)
		return b[:]
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v)|0x80000000)
	return b[:]
}

// buildTLSClientHello uses crypto/tls with net.Pipe() to generate a real TLS
// 1.3 ClientHello with the given domain as SNI. The handshake writes the
// ClientHello to the pipe then fails (no real server) — we capture the raw
// ClientHello bytes. This produces a genuine Chrome-compatible ClientHello
// (real SNI, ALPN h3, TLS 1.3) that QUIC servers accept.
func buildTLSClientHello(domain string) ([]byte, error) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	var (
		chBytes []byte
		chErr   error
		wg      sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = serverConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		var buf bytes.Buffer
		tmp := make([]byte, 4096)
		for {
			n, err := serverConn.Read(tmp)
			buf.Write(tmp[:n])
			if err != nil {
				break
			}
		}
		if buf.Len() == 0 {
			chErr = fmt.Errorf("read ClientHello from pipe: no data")
			return
		}
		chBytes = buf.Bytes()
		serverConn.Close()
	}()

	tlsConn := tls.Client(clientConn, &tls.Config{
		ServerName:         domain,
		InsecureSkipVerify: true,
		NextProtos:         []string{"h3"},
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
	})
	_ = tlsConn.Handshake() // expected to fail after writing ClientHello
	wg.Wait()

	if chErr != nil {
		return nil, chErr
	}
	if len(chBytes) == 0 {
		return nil, fmt.Errorf("empty ClientHello")
	}
	return chBytes, nil
}
