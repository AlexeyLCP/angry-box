package chain

// awgcps_format.go — converts the raw I1-I5 byte payloads produced by
// GenerateAWGObfsMaterial into the AWG CPS string format used by sing-box and
// kernel awg-quick configs:
//
//   "<b 0x{hex}>"             — raw packet (tls/sip/quic profiles)
//   "<r N><b 0x{hex}>"         — repeated packet (dns profile sends it N times)
//
// Two important parity fixes vs the previous angry-box path:
//   1. The format was base64-StdEncoding; sing-box-extended and kernel AWG both
//      expect the `<b 0x...>` CPS string form. (See VPN/orchestrator awg_cps.py
//      _to_cps.)
//   2. Odd-length hex is padded with a leading zero — sing-box wireguard-go's
//      CPS parser rejects odd hex ("failed to parse I1: failed to build <b>:
//      odd amount of symbols") while the kernel AmneziaWG tolerates it.
//      (See VPN/docs/nuances-bugs-patches.md bug #2.)

import (
	"encoding/hex"
	"fmt"
)

// CPSString converts raw packet bytes to the AWG CPS string format.
// repeatCount > 0 prefixes "<r N>" (the dns profile sends the packet twice).
func CPSString(packet []byte, repeatCount int) string {
	hexStr := evenHex(hex.EncodeToString(packet))
	if repeatCount > 0 {
		return fmt.Sprintf("<r %d><b 0x%s>", repeatCount, hexStr)
	}
	return fmt.Sprintf("<b 0x%s>", hexStr)
}

// evenHex prefixes a leading "0" if the hex string has odd length, so sing-box
// wireguard-go's parser doesn't reject it.
func evenHex(h string) string {
	if len(h)%2 != 0 {
		return "0" + h
	}
	return h
}

// CPSMaterialString converts an AWGObfsMaterial's Ix field to a single CPS
// string (no repeat prefix).
func CPSMaterialString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return CPSString(b, 0)
}

// CPSMaterialStrings converts all five Ix fields of an AWGObfsMaterial to CPS
// strings, returning them in I1..I5 order. Empty payloads yield "".
func CPSMaterialStrings(m AWGObfsMaterial) [5]string {
	return [5]string{
		CPSMaterialString(m.I1),
		CPSMaterialString(m.I2),
		CPSMaterialString(m.I3),
		CPSMaterialString(m.I4),
		CPSMaterialString(m.I5),
	}
}

// CPSDNSString is the dns-profile variant: "<r 2><b 0x...>" (packet sent twice).
func CPSDNSString(packet []byte) string {
	if len(packet) == 0 {
		return ""
	}
	return CPSString(packet, 2)
}

// randInt for jitter lives in awg_cps.go (shared by the existing CPS generator).