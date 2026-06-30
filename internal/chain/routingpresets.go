package chain

// routingpresets.go — per-service routing presets (Telegram, YouTube, Netflix,
// …), ported from VPN/orchestrator/app/services/protocol_presets.py ROUTING_PRESETS.
// Each preset maps a service name to the domain set that should be routed
// through the proxy (or rejected, for the "ads" preset). The UI offers these as
// a "apply preset" shortcut when creating RouteRules.

// RoutingPreset is one named service→domains entry.
type RoutingPreset struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Domains  []string `json:"domains"`
	Action   string   `json:"action,omitempty"` // only "ads" sets "reject"
}

// ROUTING_PRESETS — the full catalog (grouped by category below).
var ROUTING_PRESETS = []RoutingPreset{
	// social
	{"telegram", "Telegram", "social", []string{"t.me", "telegram.org", "tdesktop.com"}, ""},
	{"discord", "Discord", "social", []string{"discord.com", "discord.gg", "discordapp.com"}, ""},
	{"youtube", "YouTube", "social", []string{"youtube.com", "googlevideo.com", "ytimg.com"}, ""},
	{"instagram", "Instagram", "social", []string{"instagram.com", "cdninstagram.com"}, ""},
	{"facebook", "Facebook", "social", []string{"facebook.com", "fbcdn.net"}, ""},
	{"reddit", "Reddit", "social", []string{"reddit.com", "redditmedia.com"}, ""},
	{"twitter", "Twitter / X", "social", []string{"twitter.com", "x.com", "twimg.com"}, ""},
	{"tiktok", "TikTok", "social", []string{"tiktok.com", "musical.ly"}, ""},
	{"whatsapp", "WhatsApp", "social", []string{"whatsapp.com", "whatsapp.net"}, ""},
	{"signal", "Signal", "social", []string{"signal.org", "whispersystems.org"}, ""},
	{"threads", "Threads", "social", []string{"threads.net", "threads.com"}, ""},
	{"bluesky", "Bluesky", "social", []string{"bsky.app", "bsky.social"}, ""},
	// media
	{"netflix", "Netflix", "media", []string{"netflix.com", "nflxvideo.net"}, ""},
	{"spotify", "Spotify", "media", []string{"spotify.com", "scdn.co"}, ""},
	{"soundcloud", "SoundCloud", "media", []string{"soundcloud.com", "sndcdn.com"}, ""},
	{"tidal", "Tidal", "media", []string{"tidal.com", "tidalhifi.com"}, ""},
	{"deezer", "Deezer", "media", []string{"deezer.com", "dzcdn.net"}, ""},
	{"vimeo", "Vimeo", "media", []string{"vimeo.com", "vimeocdn.com"}, ""},
	{"twitch", "Twitch", "media", []string{"twitch.tv", "ttvnw.net"}, ""},
	{"primevideo", "Prime Video", "media", []string{"primevideo.com", "amazonvideo.com"}, ""},
	{"disney", "Disney+", "media", []string{"disneyplus.com", "dssott.com"}, ""},
	{"hbo", "HBO Max", "media", []string{"hbomax.com", "hbo.com"}, ""},
	{"hulu", "Hulu", "media", []string{"hulu.com", "hulustream.com"}, ""},
	// ai
	{"openai", "OpenAI", "ai", []string{"openai.com", "chat.openai.com"}, ""},
	{"anthropic", "Anthropic", "ai", []string{"anthropic.com", "claude.ai"}, ""},
	{"gemini", "Gemini", "ai", []string{"gemini.google.com"}, ""},
	{"copilot", "GitHub Copilot", "ai", []string{"copilot.github.com"}, ""},
	{"perplexity", "Perplexity", "ai", []string{"perplexity.ai"}, ""},
	// developer
	{"github", "GitHub", "developer", []string{"github.com", "githubusercontent.com"}, ""},
	{"gitlab", "GitLab", "developer", []string{"gitlab.com", "gitlab.net"}, ""},
	{"docker", "Docker", "developer", []string{"docker.com", "docker.io"}, ""},
	{"npm", "npm", "developer", []string{"npmjs.com", "npmjs.org"}, ""},
	{"jetbrains", "JetBrains", "developer", []string{"jetbrains.com", "kotlinlang.org"}, ""},
	{"vercel", "Vercel", "developer", []string{"vercel.com", "vercel.app"}, ""},
	{"notion", "Notion", "developer", []string{"notion.so", "notion.com"}, ""},
	{"figma", "Figma", "developer", []string{"figma.com"}, ""},
	{"slack", "Slack", "developer", []string{"slack.com", "slackb.com"}, ""},
	{"zoom", "Zoom", "developer", []string{"zoom.us", "zoom.com"}, ""},
	// cloud
	{"cloudflare", "Cloudflare", "cloud", []string{"cloudflare.com", "cf-quic.com"}, ""},
	{"apple", "Apple", "cloud", []string{"apple.com", "icloud.com"}, ""},
	{"microsoft", "Microsoft", "cloud", []string{"microsoft.com", "live.com"}, ""},
	{"google", "Google", "cloud", []string{"google.com", "gstatic.com"}, ""},
	{"nvidia", "NVIDIA", "cloud", []string{"nvidia.com", "geforce.com"}, ""},
	// gaming
	{"steam", "Steam", "gaming", []string{"steampowered.com", "steamcommunity.com"}, ""},
	{"epicgames", "Epic Games", "gaming", []string{"epicgames.com", "unrealengine.com"}, ""},
	{"blizzard", "Blizzard", "gaming", []string{"blizzard.com", "battle.net"}, ""},
	{"ea", "EA", "gaming", []string{"ea.com", "origin.com"}, ""},
	// block
	{"ads", "Block Ads", "block", []string{}, "reject"},
}

// GetRoutingPresets returns presets, optionally filtered by category.
func GetRoutingPresets(category string) []RoutingPreset {
	if category == "" {
		return ROUTING_PRESETS
	}
	var out []RoutingPreset
	for _, p := range ROUTING_PRESETS {
		if p.Category == category {
			out = append(out, p)
		}
	}
	return out
}

// GetRoutingPresetDomains returns the domain list for a preset id (empty if not
// found).
func GetRoutingPresetDomains(id string) []string {
	for _, p := range ROUTING_PRESETS {
		if p.ID == id {
			return p.Domains
		}
	}
	return nil
}

// GetRoutingPreset returns a preset by id.
func GetRoutingPreset(id string) (RoutingPreset, bool) {
	for _, p := range ROUTING_PRESETS {
		if p.ID == id {
			return p, true
		}
	}
	return RoutingPreset{}, false
}

// RoutingPresetCategories is the ordered list of categories for UI grouping.
var RoutingPresetCategories = []string{"social", "media", "ai", "developer", "cloud", "gaming", "block"}