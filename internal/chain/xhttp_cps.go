package chain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// xhttp_cps.go
// Advanced XHTTP obfuscation generators and helpers.
// Techniques ported/inspired from (with credits in README):
//   - Xray XHTTP (RPRX et al.) — header padding ranges, XMUX-style controls, stream/packet modes
//   - NaiveProxy (klzgrad) — realistic browser preamble / header patterns
//   - Hysteria2 Gecko ideas — fragmentation thinking applied to HTTP chunks
//   - Community research (TheyCallMeSecond, Hiddify configs, etc.)

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
// (hex encoded or raw — sing-box / xray usually accept the length or a header value).
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

// GenerateXMUX returns a multiplexing control map with random ranges.
// Directly inspired by Xray XHTTP XMUX (maxConcurrency, hMaxReusableSecs, etc.).
// These can be mapped to sing-box multiplex settings or passed via extra in Xray.
func GenerateXMUX() map[string]any {
	return map[string]any{
		"enabled":          true,
		"max_concurrency":  fmt.Sprintf("%d-%d", RandRange(4, 12), RandRange(16, 48)),
		"max_connections":  0, // unlimited or controlled
		"h_max_reusable":   fmt.Sprintf("%d-%d", RandRange(1800, 3600), RandRange(7200, 14400)),
		"h_max_requests":   fmt.Sprintf("%d-%d", RandRange(400, 900), RandRange(800, 2000)),
		"keep_alive":       "30s",
	}
}

// ApplyXHTTPObfuscation takes a base transport map and enriches it with
// the advanced obfuscation parameters from the preset + generators.
// This is the main integration point used by both applier and standalone generators.
func ApplyXHTTPObfuscation(transport map[string]any, preset *XHTTPPreset) {
	if preset == nil {
		return
	}

	// Padding (very important for breaking fixed-size fingerprints)
	if preset.PaddingBytes != "" {
		// Expect format "min-max" or single number
		transport["x_padding_bytes"] = preset.PaddingBytes // sing-box / xray compatible name in many setups
	} else {
		// Auto-generate strong random padding range
		minP := RandRange(120, 600)
		maxP := RandRange(800, 1600)
		transport["x_padding_bytes"] = fmt.Sprintf("%d-%d", minP, maxP)
	}

	// Multiplex controls (XMUX style)
	if preset.Multiplex {
		xmux := GenerateXMUX()
		if preset.MaxConcurrency != "" {
			xmux["max_concurrency"] = preset.MaxConcurrency
		}
		transport["multiplex"] = xmux
	}

	// Rich realistic headers (Naive-inspired)
	if len(preset.Headers) == 0 {
		host := ""
		if len(preset.Hosts) > 0 {
			host = preset.Hosts[0]
		}
		transport["headers"] = GenerateRealisticHeaders(host)
	}

	// Upstream / Downstream separation hints (powerful technique from Xray XHTTP research)
	if preset.UpstreamHost != "" || preset.DownstreamHost != "" {
		// These can be consumed by more advanced builders or passed via extra
		transport["upstream_host"] = preset.UpstreamHost
		transport["downstream_host"] = preset.DownstreamHost
		if preset.UpstreamAlpn != "" {
			transport["upstream_alpn"] = preset.UpstreamAlpn
		}
		if preset.DownstreamAlpn != "" {
			transport["downstream_alpn"] = preset.DownstreamAlpn
		}
	}
}

// GenerateXHTTPMode returns a recommended XHTTP mode based on stealth level.
// 0 = packet-up (max compat), 1-2 = mixed, 3 = stream-up + fragmentation style (max stealth).
func GenerateXHTTPMode(stealthLevel int) string {
	if stealthLevel >= 3 {
		return "stream-up" // aggressive, good with good middleboxes
	}
	if stealthLevel >= 2 {
		return "auto"
	}
	return "packet-up"
}

// GenerateXHTTPExtra produces a full "extra" object that can be dropped into
// advanced Xray or sing-box XHTTP configs. This is one of the most powerful
// things we took from the Xray XHTTP research.
func GenerateXHTTPExtra(stealthLevel int, host string) map[string]any {
	mode := GenerateXHTTPMode(stealthLevel)

	extra := map[string]any{
		"mode": mode,
		"x_padding_bytes": fmt.Sprintf("%d-%d", RandRange(200, 700), RandRange(900, 1800)),
		"headers": GenerateRealisticHeaders(host),
	}

	// Strong multiplexing controls for high stealth
	if stealthLevel >= 2 {
		extra["xmux"] = map[string]any{
			"max_concurrency": fmt.Sprintf("%d-%d", RandRange(3, 10), RandRange(12, 40)),
			"h_max_reusable":  fmt.Sprintf("%d-%d", RandRange(1200, 3000), RandRange(5000, 12000)),
			"h_max_requests":  fmt.Sprintf("%d-%d", RandRange(300, 700), RandRange(600, 1500)),
		}
	}

	// Add fragmentation-style hint (inspired by Gecko thinking)
	if stealthLevel >= 3 {
		extra["fragmentation"] = map[string]any{
			"enabled":     true,
			"min_packets": 2,
			"max_packets": 6,
		}
	}

	return extra
}

// GenerateRealisticPreamble simulates the kind of early traffic a real browser
// would send when opening a page (inspired by NaiveProxy preamble feature).
// Returns a list of "plausible first requests" that can be used for traffic masking or testing.
func GenerateRealisticPreamble(host string) []string {
	paths := []string{
		"/", "/search", "/api/v1/config", "/static/main.js", "/favicon.ico",
		"/_next/static/chunks/", "/cdn-cgi/", "/assets/",
	}
	out := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		p := paths[RandRange(0, len(paths)-1)]
		out = append(out, fmt.Sprintf("https://%s%s", host, p))
	}
	return out
}

// EnhanceTransportWithNaivePreamble adds early "browser-like" requests to the
// transport config when possible (for advanced setups that support it).
func EnhanceTransportWithNaivePreamble(transport map[string]any, host string) {
	if host == "" {
		return
	}
	preambles := GenerateRealisticPreamble(host)
	transport["preamble_urls"] = preambles // consumed by advanced consumers
}
