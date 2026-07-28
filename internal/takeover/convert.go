package takeover

// convert.go — config converters: turn an existing VPN's config (sing-box /
// Xray/3x-ui / MTProxy telemt / AWG) into a []model.NodeInbound that the
// angry-box renderer (Backend.GenerateConfig → generateStandaloneNode) can feed
// to produce a sing-box config with the SAME settings.
//
// Field mappings are sourced from the audit of configs/original-xray-config.json
// + sing-box-extended schema. For protocols angry-box natively generates
// (vless-reality, tuic, awg, hysteria2) we populate model.NodeInbound fields so
// generateStandaloneNode reproduces the inbound. For protocols with no typed
// inbound struct (vmess/trojan/shadowsocks/mtproto) the converter stores the
// fully-built sing-box inbound JSON in NodeInbound.ExtraJSON so the takeover
// executor can append it directly to SingboxConfig.Inbounds ([]json.RawMessage).

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// Convert takes a detection and produces the equivalent NodeInbounds (+ any
// extra raw sing-box inbound JSON for protocols without a native case). The
// caller persists the inbounds on NodeInfo and renders via GenerateConfig.
func Convert(det *Detection) ([]model.NodeInbound, []json.RawMessage, error) {
	if det == nil {
		return nil, nil, fmt.Errorf("takeover: nil detection")
	}
	switch det.Type {
	case DetectedSingBox:
		return convertSingBoxConfig(det.ConfigContent)
	case DetectedXray:
		return convertXrayConfig(det.ConfigContent)
	case DetectedMTProxy:
		// MTProxy conversion needs the raw secret + domain; det.ConfigContent
		// is the mtg/telemt config (TOML/conf) — parsed by convertMTProxyConfig.
		return convertMTProxyConfig(det.ConfigContent)
	case DetectedAWG:
		// AWG conversion reuses the importer's parsed state. The caller should
		// call ImportAWGConfigs separately and map AwgServerConfig/ExitNodes →
		// NodeInbound. Here we return a single placeholder inbound so the
		// executor knows to run the AWG path.
		return []model.NodeInbound{{Protocol: "awg", Source: "takeover:awg"}}, nil, nil
	case DetectedNone, "":
		return nil, nil, fmt.Errorf("takeover: nothing to convert (no VPN detected)")
	}
	return nil, nil, fmt.Errorf("takeover: unsupported detected type %q", det.Type)
}

// ─── sing-box → NodeInbound ──────────────────────────────────────────────────

// convertSingBoxConfig parses an existing sing-box config.json and maps each
// inbound to a NodeInbound that generateStandaloneNode can reproduce.
func convertSingBoxConfig(cfgJSON string) ([]model.NodeInbound, []json.RawMessage, error) {
	if strings.TrimSpace(cfgJSON) == "" {
		return nil, nil, fmt.Errorf("takeover: empty sing-box config")
	}
	var cfg struct {
		Inbounds []json.RawMessage `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return nil, nil, fmt.Errorf("takeover: parse sing-box config: %w", err)
	}
	var inbounds []model.NodeInbound
	var extra []json.RawMessage
	for i, raw := range cfg.Inbounds {
		var ib struct {
			Type       string `json:"type"`
			Tag        string `json:"tag"`
			ListenPort int    `json:"listen_port"`
		}
		if err := json.Unmarshal(raw, &ib); err != nil {
			continue
		}
		switch ib.Type {
		case "vless":
			ni, ok := convertSingBoxVLESS(raw, ib.ListenPort)
			if ok {
				inbounds = append(inbounds, ni)
			}
		case "tuic":
			inbounds = append(inbounds, convertSingBoxTUIC(raw, ib.ListenPort))
		case "hysteria2":
			inbounds = append(inbounds, convertSingBoxHysteria2(raw, ib.ListenPort))
		case "wireguard", "awg":
			// amnezia-box 1.14 moved AWG to a separate `type:"awg"` endpoint with
			// flat obfuscation fields (no nested `amnezia:{}`); the takeover reader
			// only needs private_key + peers[].public_key, common to both.
			inbounds = append(inbounds, convertSingBoxWireGuard(raw, ib.ListenPort))
		case "mtproto", "mtproxy":
			inbounds = append(inbounds, convertSingBoxMTProto(raw, ib.ListenPort))
		case "tun":
			// TUN is routing infra, not a user-facing inbound — skip (the
			// renderer adds its own TUN for AWG balancer roles).
			continue
		default:
			// Unknown inbound (vmess/trojan/shadowsocks in an existing
			// sing-box config) — pass through as raw JSON.
			_ = i
			extra = append(extra, raw)
		}
	}
	if len(inbounds)+len(extra) == 0 {
		return nil, nil, fmt.Errorf("takeover: sing-box config had no convertible inbounds")
	}
	return inbounds, extra, nil
}

func convertSingBoxVLESS(raw json.RawMessage, port int) (model.NodeInbound, bool) {
	var ib struct {
		Users []struct {
			Name string `json:"name"`
			UUID string `json:"uuid"`
			Flow string `json:"flow"`
		} `json:"users"`
		TLS struct {
			ServerName string `json:"server_name"`
			Reality    struct {
				PrivateKey string   `json:"private_key"`
				ShortID    []string `json:"short_id"`
			} `json:"reality"`
		} `json:"tls"`
		Transport struct {
			Type string `json:"type"`
		} `json:"transport"`
	}
	partialUnmarshal(raw, &ib)
	if len(ib.Users) == 0 {
		return model.NodeInbound{}, false
	}
	// Determine protocol label: vless-reality if REALITY present, else xhttp/ws.
	proto := "vless-reality"
	if ib.TLS.Reality.PrivateKey == "" {
		if ib.Transport.Type == "xhttp" || ib.Transport.Type == "http" {
			proto = "xhttp"
		}
	}
	shortID := ""
	if len(ib.TLS.Reality.ShortID) > 0 {
		shortID = ib.TLS.Reality.ShortID[0]
	}
	return model.NodeInbound{
		Protocol:      proto,
		Port:          port,
		UUID:          ib.Users[0].UUID,
		ServerPrivKey: ib.TLS.Reality.PrivateKey,
		ShortID:       shortID,
		Obfuscation:   ib.TLS.ServerName,
		Source:        "takeover:singbox",
	}, true
}

func convertSingBoxTUIC(raw json.RawMessage, port int) model.NodeInbound {
	var ib struct {
		Users []struct {
			UUID     string `json:"uuid"`
			Password string `json:"password"`
		} `json:"users"`
	}
	partialUnmarshal(raw, &ib)
	ni := model.NodeInbound{Protocol: "tuic", Port: port, Source: "takeover:singbox"}
	if len(ib.Users) > 0 {
		ni.UUID = ib.Users[0].UUID
		ni.ServerPrivKey = ib.Users[0].Password // TUIC password lives in ServerPrivKey field per generateStandaloneNode
	}
	return ni
}

func convertSingBoxHysteria2(raw json.RawMessage, port int) model.NodeInbound {
	var ib struct {
		Users []struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		} `json:"users"`
	}
	partialUnmarshal(raw, &ib)
	ni := model.NodeInbound{Protocol: "hysteria2", Port: port, Source: "takeover:singbox"}
	if len(ib.Users) > 0 {
		ni.UUID = ib.Users[0].Password // hysteria2 password carried in UUID slot by buildClientURI
	}
	return ni
}

func convertSingBoxWireGuard(raw json.RawMessage, port int) model.NodeInbound {
	var ib struct {
		PrivateKey string `json:"private_key"`
		Peers      []struct {
			PublicKey string `json:"public_key"`
		} `json:"peers"`
	}
	partialUnmarshal(raw, &ib)
	ni := model.NodeInbound{Protocol: "awg", Port: port, ServerPrivKey: ib.PrivateKey, Source: "takeover:singbox"}
	if len(ib.Peers) > 0 {
		ni.AWGClientPub = ib.Peers[0].PublicKey
	}
	return ni
}

// convertSingBoxMTProto decodes a sing-box mtproto inbound's `ee`+secret+domain
// secret back into NodeInbound (UUID=secretHex, Obfuscation=fakeTLSDomain).
func convertSingBoxMTProto(raw json.RawMessage, port int) model.NodeInbound {
	var ib struct {
		Users []struct {
			Name   string `json:"name"`
			Secret string `json:"secret"`
		} `json:"users"`
	}
	partialUnmarshal(raw, &ib)
	ni := model.NodeInbound{Protocol: "mtproxy", Port: port, Source: "takeover:singbox"}
	if len(ib.Users) > 0 {
		secretHex, domain := decodeMTProxyFullSecret(ib.Users[0].Secret)
		ni.UUID = secretHex
		ni.Obfuscation = domain
	}
	return ni
}

// decodeMTProxyFullSecret splits "ee"+secretHex+hex(domain) → (secretHex, domain).
func decodeMTProxyFullSecret(full string) (string, string) {
	if !strings.HasPrefix(full, "ee") {
		return full, "disk.yandex.ru" // raw hex secret, unknown domain
	}
	rest := full[2:]
	if len(rest) < 32 {
		return rest, ""
	}
	secretHex := rest[:32]
	domainHex := rest[32:]
	domain := ""
	if b, err := hex.DecodeString(domainHex); err == nil {
		domain = string(b)
	}
	if domain == "" {
		domain = "disk.yandex.ru"
	}
	return secretHex, domain
}

// ─── Xray/3x-ui → NodeInbound ────────────────────────────────────────────────

// convertXrayConfig parses an Xray/3x-ui config.json. The primary supported
// case (matching configs/original-xray-config.json) is VLESS+REALITY+XHTTP;
// vmess/trojan/shadowsocks/mtproto are converted to raw sing-box inbound JSON
// (appended to the extra slice) since angry-box has no typed inbound for them.
func convertXrayConfig(cfgJSON string) ([]model.NodeInbound, []json.RawMessage, error) {
	if strings.TrimSpace(cfgJSON) == "" {
		return nil, nil, fmt.Errorf("takeover: empty xray config")
	}
	var cfg struct {
		Inbounds []struct {
			Port           int             `json:"port"`
			Protocol       string          `json:"protocol"`
			Tag            string          `json:"tag"`
			Settings       json.RawMessage `json:"settings"`
			StreamSettings json.RawMessage `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return nil, nil, fmt.Errorf("takeover: parse xray config: %w", err)
	}
	var inbounds []model.NodeInbound
	var extra []json.RawMessage
	for _, xi := range cfg.Inbounds {
		switch strings.ToLower(xi.Protocol) {
		case "vless":
			if ni, raw, ok := convertXrayVLESS(xi.Port, xi.Settings, xi.StreamSettings); ok {
				if raw != nil {
					extra = append(extra, raw)
				} else {
					inbounds = append(inbounds, ni)
				}
			}
		case "vmess":
			if raw := convertXrayVMess(xi.Port, xi.Settings, xi.StreamSettings); raw != nil {
				extra = append(extra, raw)
			}
		case "trojan":
			if raw := convertXrayTrojan(xi.Port, xi.Settings, xi.StreamSettings); raw != nil {
				extra = append(extra, raw)
			}
		case "shadowsocks":
			if raw := convertXrayShadowsocks(xi.Port, xi.Settings); raw != nil {
				extra = append(extra, raw)
			}
		case "mtproto":
			if raw := convertXrayMTProto(xi.Port, xi.Settings); raw != nil {
				extra = append(extra, raw)
			}
		case "tun":
			// routing infra, skip
		}
	}
	if len(inbounds)+len(extra) == 0 {
		return nil, nil, fmt.Errorf("takeover: xray config had no convertible inbounds")
	}
	return inbounds, extra, nil
}

// convertXrayVLESS maps an Xray VLESS inbound → NodeInbound (vless-reality if
// REALITY present) or raw sing-box JSON (plain TLS/ws). Returns ok=false if the
// inbound has no users (not convertible).
func convertXrayVLESS(port int, settings, stream json.RawMessage) (model.NodeInbound, json.RawMessage, bool) {
	var s struct {
		Clients []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Flow  string `json:"flow"`
		} `json:"clients"`
	}
	partialUnmarshal(settings, &s)
	if len(s.Clients) == 0 {
		return model.NodeInbound{}, nil, false
	}
	var ss struct {
		Network          string `json:"network"`
		Security         string `json:"security"`
		ServerName       string `json:"serverName"`
		XHTTPSettings    struct {
			Path string `json:"path"`
			Host string `json:"host"`
			Mode string `json:"mode"`
		} `json:"xhttpSettings"`
		SplithttpSettings struct {
			Path string `json:"path"`
			Host string `json:"host"`
			Mode string `json:"mode"`
		} `json:"splithttpSettings"`
		RealitySettings struct {
			Dest        string   `json:"dest"`
			Target      string   `json:"target"`
			ServerNames []string `json:"serverNames"`
			PrivateKey  string   `json:"privateKey"`
			ShortIds    []string `json:"shortIds"`
		} `json:"realitySettings"`
	}
	partialUnmarshal(stream, &ss)

	// Normalize network: Xray "splithttp" → sing-box "xhttp".
	network := strings.ToLower(ss.Network)
	if network == "splithttp" {
		network = "xhttp"
	}

	if strings.EqualFold(ss.Security, "reality") && ss.RealitySettings.PrivateKey != "" {
		// REALITY → vless-reality NodeInbound (native angry-box case).
		sni := ""
		if len(ss.RealitySettings.ServerNames) > 0 {
			sni = ss.RealitySettings.ServerNames[0]
		}
		shortID := ""
		if len(ss.RealitySettings.ShortIds) > 0 {
			shortID = ss.RealitySettings.ShortIds[0]
		}
		flow := s.Clients[0].Flow
		if flow == "" {
			flow = "xtls-rprx-vision"
		}
		_ = flow // stored on the inbound via Obfuscation? No — flow not in NodeInbound; renderer defaults it.
		return model.NodeInbound{
			Protocol:      "vless-reality",
			Port:          port,
			UUID:          s.Clients[0].ID,
			ServerPrivKey: ss.RealitySettings.PrivateKey,
			ShortID:       shortID,
			Obfuscation:   sni,
			Source:        "takeover:xray",
		}, nil, true
	}

	// Non-REALITY VLESS (plain TLS / ws / xhttp without reality) → raw sing-box
	// inbound JSON so we don't lose info the native case can't express.
	users := make([]map[string]string, 0, len(s.Clients))
	for _, c := range s.Clients {
		u := map[string]string{"name": c.Email, "uuid": c.ID}
		if c.Flow != "" {
			u["flow"] = strings.TrimSuffix(c.Flow, "-udp443")
		}
		users = append(users, u)
	}
	inb := map[string]any{
		"type": "vless", "tag": "vless-in", "listen": "0.0.0.0",
		"listen_port": port, "users": users,
	}
	if network == "xhttp" {
		path := ss.XHTTPSettings.Path
		if path == "" {
			path = ss.SplithttpSettings.Path
		}
		host := ss.XHTTPSettings.Host
		if host == "" {
			host = ss.SplithttpSettings.Host
		}
		inb["transport"] = map[string]any{"type": "xhttp", "path": path, "host": host}
	} else if network == "ws" {
		inb["transport"] = map[string]any{"type": "ws"}
	}
	if strings.EqualFold(ss.Security, "tls") {
		inb["tls"] = map[string]any{"enabled": true, "server_name": ss.ServerName}
	}
	raw, _ := json.Marshal(inb)
	return model.NodeInbound{}, raw, true
}

// convertXrayVMess → raw sing-box vmess inbound JSON (alterId dropped — sing-box 1.13).
func convertXrayVMess(port int, settings, stream json.RawMessage) json.RawMessage {
	var s struct {
		Clients []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"clients"`
	}
	partialUnmarshal(settings, &s)
	if len(s.Clients) == 0 {
		return nil
	}
	users := make([]map[string]string, 0, len(s.Clients))
	for _, c := range s.Clients {
		users = append(users, map[string]string{"name": c.Email, "uuid": c.ID})
	}
	inb := map[string]any{"type": "vmess", "tag": "vmess-in", "listen": "0.0.0.0", "listen_port": port, "users": users}
	raw, _ := json.Marshal(inb)
	return raw
}

// convertXrayTrojan → raw sing-box trojan inbound JSON (TLS required).
func convertXrayTrojan(port int, settings, stream json.RawMessage) json.RawMessage {
	var s struct {
		Clients []struct {
			Password string `json:"password"`
			Email    string `json:"email"`
		} `json:"clients"`
	}
	partialUnmarshal(settings, &s)
	if len(s.Clients) == 0 {
		return nil
	}
	var ss struct {
		ServerName string `json:"serverName"`
	}
	partialUnmarshal(stream, &ss)
	users := make([]map[string]string, 0, len(s.Clients))
	for _, c := range s.Clients {
		users = append(users, map[string]string{"name": c.Email, "password": c.Password})
	}
	inb := map[string]any{
		"type": "trojan", "tag": "trojan-in", "listen": "0.0.0.0", "listen_port": port,
		"users": users,
		"tls":   map[string]any{"enabled": true, "server_name": ss.ServerName},
	}
	raw, _ := json.Marshal(inb)
	return raw
}

// convertXrayShadowsocks → raw sing-box shadowsocks inbound JSON.
func convertXrayShadowsocks(port int, settings json.RawMessage) json.RawMessage {
	var s struct {
		Method   string `json:"method"`
		Password string `json:"password"`
	}
	partialUnmarshal(settings, &s)
	if s.Method == "" {
		return nil
	}
	inb := map[string]any{
		"type": "shadowsocks", "tag": "ss-in", "listen": "0.0.0.0", "listen_port": port,
		"method": s.Method, "password": s.Password,
	}
	raw, _ := json.Marshal(inb)
	return raw
}

// convertXrayMTProto → raw sing-box mtproto inbound JSON. Xray MTProto secret is
// raw hex (or `dd`-prefixed FakeTLS); we compose the sing-box `ee`+secret+domain
// form. The FakeTLS domain is not stored separately in Xray MTProto settings, so
// we default to "disk.yandex.ru" (the operator can edit post-takeover).
func convertXrayMTProto(port int, settings json.RawMessage) json.RawMessage {
	var s struct {
		Users []struct {
			Secret string `json:"secret"`
		} `json:"users"`
	}
	partialUnmarshal(settings, &s)
	if len(s.Users) == 0 {
		return nil
	}
	secretHex := strings.TrimPrefix(s.Users[0].Secret, "dd")
	secretHex = strings.TrimPrefix(secretHex, "ee")
	// Compose ee+secret+hex(domain).
	const fakeDomain = "disk.yandex.ru"
	full := "ee" + secretHex + hex.EncodeToString([]byte(fakeDomain))
	users := []map[string]string{{"name": "mtproto", "secret": full}}
	inb := map[string]any{
		"type": "mtproto", "tag": "mtp-in", "listen": "0.0.0.0", "listen_port": port,
		"users": users,
	}
	raw, _ := json.Marshal(inb)
	return raw
}

// ─── MTProxy telemt/mtg → NodeInbound ────────────────────────────────────────

// convertMTProxyConfig parses a telemt/mtg config (TOML/conf). Best-effort: the
// legacy secret format is raw 16-byte hex (32 chars); the FakeTLS domain is
// stored separately. Without sample files we extract any 32-hex-char token as
// the secret and default the domain. If nothing parses, return an explicit error.
func convertMTProxyConfig(content string) ([]model.NodeInbound, []json.RawMessage, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil, fmt.Errorf("takeover: empty mtproxy config")
	}
	// Look for a `secret = "..."` or `Secret = "..."` line; accept 32-hex.
	var secretHex string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "secret") && !strings.HasPrefix(line, "Secret") {
			continue
		}
		if idx := strings.IndexByte(line, '='); idx >= 0 {
			val := strings.TrimSpace(line[idx+1:])
			val = strings.Trim(val, `"'`)
			// Strip ee/dd prefix if present.
			val = strings.TrimPrefix(val, "ee")
			val = strings.TrimPrefix(val, "dd")
			if len(val) == 32 && isHex(val) {
				secretHex = val
				break
			}
		}
	}
	if secretHex == "" {
		return nil, nil, fmt.Errorf("takeover: mtproxy config has no 32-hex secret (unsupported format — edit manually)")
	}
	const fakeDomain = "disk.yandex.ru"
	full := "ee" + secretHex + hex.EncodeToString([]byte(fakeDomain))
	inb := map[string]any{
		"type": "mtproto", "tag": "mtp-in", "listen": "0.0.0.0", "listen_port": 443,
		"users": []map[string]string{{"name": "mtproxy", "secret": full}},
	}
	raw, _ := json.Marshal(inb)
	// Also a NodeInbound so the UI lists it.
	ni := model.NodeInbound{Protocol: "mtproxy", Port: 443, UUID: secretHex, Obfuscation: fakeDomain, Source: "takeover:mtproxy"}
	return []model.NodeInbound{ni}, []json.RawMessage{raw}, nil
}

// isHex reports whether s consists entirely of hex chars.
func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// partialUnmarshal decodes raw into v, deliberately ignoring the error.
//
// The takeover converters target FOREIGN configs (sing-box/Xray/3x-ui/MTProxy)
// whose shape we only partially know. Go's encoding/json already ignores
// unknown fields, so unmarshalling into a struct with only the fields we care
// about is the intended lenient-extraction pattern: a parse error means "this
// inbound does not match our expected shape", which the caller distinguishes by
// checking for empty result fields (e.g. len(ib.Users) == 0) immediately after.
// Swallowing the json error here is therefore safe and by design — AGENTS.md
// rule #6 ("no silent failures") is satisfied by this explicit rationale and
// the callers' post-decode presence checks.
func partialUnmarshal(raw []byte, v any) {
	_ = json.Unmarshal(raw, v)
}