package chain

// xhttp_cps.go — XHTTP transport obfuscation helpers used by the sing-box
// config builders. The realistic-header generation is inspired by NaiveProxy
// (https://github.com/SagerNet/naive, BSD-3); the XHTTP transport fields are
// sourced from the Xray team (RPRX) research. Adapted from
// VPN/orchestrator/app/services/xhttp_cps.py.
//
// Note: the XMUX / max-obfuscation / x_padding_* / cookie-placement fields are
// applied directly by xhttpTransportMap in internal/backend/singbox/roles.go
// (the live sing-box config path), NOT by the generators that used to live
// here. Those Xray-oriented generators (GenerateXMUX/GenerateXHTTPExtra/...)
// were removed as dead code once the Xray backend was dropped — they were only
// ever exercised by tests and the orphaned xray package.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// RandRange returns a random integer in [min, max] using crypto/rand.
func RandRange(min, max int) int {
	if min == max {
		return min
	}
	if max < min {
		min, max = max, min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		// Fallback (very rare)
		return min + int(time.Now().UnixNano()%(int64(max-min+1)))
	}
	return min + int(n.Int64())
}

// GeneratePadding returns a random padding string of the requested byte length
// (hex encoded) for use in headers.
func GeneratePadding(minBytes, maxBytes int) string {
	size := RandRange(minBytes, maxBytes)
	b := make([]byte, size)
	_, _ = rand.Read(b)
	// Return as hex for easy use in headers (common pattern)
	return hex.EncodeToString(b)[:size] // trim to exact size if needed
}

// GenerateRealisticHeaders returns a set of headers that look like real modern browser traffic.
// Inspired by NaiveProxy real Chromium behavior + common XHTTP stealth configs.
func GenerateRealisticHeaders(host string) map[string][]string {
	uaPool := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.4 Safari/605.1.15",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:138.0) Gecko/20100101 Firefox/138.0",
	}

	ua := uaPool[RandRange(0, len(uaPool)-1)]

	headers := map[string][]string{
		"User-Agent":      {ua},
		"Accept":          {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"},
		"Accept-Language": {"en-US,en;q=0.9", "ru-RU,ru;q=0.9,en-US;q=0.8"},
		"Accept-Encoding": {"gzip, deflate, br, zstd"},
		"Connection":      {"keep-alive"},
		"Sec-Fetch-Dest":  {"document"},
		"Sec-Fetch-Mode":  {"navigate"},
		"Sec-Fetch-Site":  {"none"},
	}

	// Add Referer with padding parameter (direct inspiration from Xray XHTTP best practices)
	if host != "" {
		padding := GeneratePadding(80, 400)
		headers["Referer"] = []string{fmt.Sprintf("https://%s/?x_padding=%s", host, padding)}
	}

	// Occasional extra realistic headers
	if RandRange(0, 3) == 0 {
		headers["Upgrade-Insecure-Requests"] = []string{"1"}
	}
	if strings.Contains(ua, "Chrome") {
		headers["sec-ch-ua"] = []string{`"Chromium";v="135", "Not;A=Brand";v="99"`}
		headers["sec-ch-ua-mobile"] = []string{"?0"}
		headers["sec-ch-ua-platform"] = []string{`"Windows"`}
	}

	return headers
}

// ApplyXHTTPObfuscation takes a base transport map and enriches it with
// the advanced obfuscation parameters from the preset + generators.
// This is the main integration point used by both applier and standalone generators.
func ApplyXHTTPObfuscation(transport *config.TransportOptions, preset *XHTTPPreset) {
	if preset == nil || transport == nil {
		return
	}

	// Rich realistic headers (Naive-inspired) — these are fully supported by sing-box
	if len(preset.Headers) == 0 {
		host := ""
		if len(preset.Hosts) > 0 {
			host = preset.Hosts[0]
		}
		transport.Headers = GenerateRealisticHeaders(host)
	}
}