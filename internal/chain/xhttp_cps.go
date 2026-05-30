package chain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/singbox/config"
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

// XrayXMUX represents Xray-core XMUX configuration.
type XrayXMUX struct {
	Enabled        bool   `json:"enabled"`
	MaxConcurrency string `json:"max_concurrency,omitempty"`
	MaxConnections int    `json:"max_connections,omitempty"`
	HMaxReusable   string `json:"h_max_reusable,omitempty"`
	HMaxRequests   string `json:"h_max_requests,omitempty"`
	KeepAlive      string `json:"keep_alive,omitempty"`
}

// XrayFragmentation represents Xray-core fragmentation config.
type XrayFragmentation struct {
	Enabled    bool `json:"enabled"`
	MinPackets int  `json:"min_packets,omitempty"`
	MaxPackets int  `json:"max_packets,omitempty"`
}

// XrayXHTTPExtra represents the extra XHTTP block for Xray.
type XrayXHTTPExtra struct {
	Mode          string              `json:"mode,omitempty"`
	XPaddingBytes string              `json:"x_padding_bytes,omitempty"`
	Headers       map[string][]string `json:"headers,omitempty"`
	XMUX          *XrayXMUX           `json:"xmux,omitempty"`
	Fragmentation *XrayFragmentation  `json:"fragmentation,omitempty"`
}

// GenerateXMUX returns a multiplexing control struct with random ranges.
// Directly inspired by Xray XHTTP XMUX (maxConcurrency, hMaxReusableSecs, etc.).
func GenerateXMUX() *XrayXMUX {
	return &XrayXMUX{
		Enabled:        true,
		MaxConcurrency: fmt.Sprintf("%d-%d", RandRange(4, 12), RandRange(16, 48)),
		MaxConnections: 0, // unlimited or controlled
		HMaxReusable:   fmt.Sprintf("%d-%d", RandRange(1800, 3600), RandRange(7200, 14400)),
		HMaxRequests:   fmt.Sprintf("%d-%d", RandRange(400, 900), RandRange(800, 2000)),
		KeepAlive:      "30s",
	}
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
// advanced Xray configs. This is one of the most powerful things we took from the Xray XHTTP research.
func GenerateXHTTPExtra(stealthLevel int, host string) *XrayXHTTPExtra {
	mode := GenerateXHTTPMode(stealthLevel)

	extra := &XrayXHTTPExtra{
		Mode:          mode,
		XPaddingBytes: fmt.Sprintf("%d-%d", RandRange(200, 700), RandRange(900, 1800)),
		Headers:       GenerateRealisticHeaders(host),
	}

	// Strong multiplexing controls for high stealth
	if stealthLevel >= 2 {
		extra.XMUX = &XrayXMUX{
			Enabled:        true,
			MaxConcurrency: fmt.Sprintf("%d-%d", RandRange(3, 10), RandRange(12, 40)),
			HMaxReusable:   fmt.Sprintf("%d-%d", RandRange(1200, 3000), RandRange(5000, 12000)),
			HMaxRequests:   fmt.Sprintf("%d-%d", RandRange(300, 700), RandRange(600, 1500)),
		}
	}

	// Add fragmentation-style hint (inspired by Gecko thinking)
	if stealthLevel >= 3 {
		extra.Fragmentation = &XrayFragmentation{
			Enabled:    true,
			MinPackets: 2,
			MaxPackets: 6,
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


