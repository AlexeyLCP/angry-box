package chain

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
	"golang.org/x/crypto/curve25519"
)

const (
	defaultUserPort      = 8443
	defaultTransportPort = 443
)

// Applier generates and pushes proxy configs to all nodes in a chain.
type Applier struct {
	factory ports.Factory
}

// NewApplier creates a chain applier.
func NewApplier(factory ports.Factory) *Applier {
	return &Applier{factory: factory}
}

// hopParams holds the generated Reality parameters for a transport inbound.
// The previous hop needs these to build its outbound.
type hopParams struct {
	UUID       string
	PrivateKey string // base64-encoded PKCS8 DER private key (sing-box 1.12+ format)
	ShortID    string // hex string
	ServerName string
	Port       int
}

// ApplyReport is the rich result of ApplyChain. It always contains per-node
// diagnostics and, when the chain uses AWG as user protocol, the key material
// needed to build a working client config.
type ApplyReport struct {
	ChainName string
	Profile   string
	Transport model.TransportType
	UserProto model.UserProtocol
	Nodes     []NodeResult
	AWG       *AWGClientMaterial `json:"awg,omitempty"`
}

// NodeResult describes the outcome for one hop.
type NodeResult struct {
	ID      string
	Success bool
	Error   string
}

// AWGClientMaterial contains everything needed for a working AmneziaWG client
// when the chain's user entry is AWG. If we auto-generated a sample, ClientPriv
// is populated so the user gets a ready-to-use config immediately.
//
// 2026 extension: now also carries the CPS/I1-I5 material that was actually
// baked into the server config (for pro_2026 / xhttp_max_stealth_2026 etc).
type AWGClientMaterial struct {
	ServerPub     string // the public key corresponding to the private_key written on the entry node
	ClientPubUsed string // what ended up in the "peers" array on the server
	ClientPriv    string // populated only for auto-generated samples (never persisted)
	Note          string `json:"note,omitempty"`

	// New 2026 fields (populated when using advanced presets)
	CPSLevel int    `json:"cps_level,omitempty"`
	Mimicry  string `json:"mimicry,omitempty"`
	I1Len    int    `json:"i1_len,omitempty"` // 1200 for QUIC etc.
	I1Type   string `json:"i1_type,omitempty"`
}

// publicKeyB64 returns the base64-encoded X25519 public key for Reality outbound.
// In sing-box 1.12.0+, the reality public_key field is a base64-encoded 32-byte public key.
func (h *hopParams) publicKeyB64() (string, error) {
	privBytes, err := base64.RawURLEncoding.DecodeString(h.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("chain: decode private key: %w", err)
	}
	if len(privBytes) != 32 {
		return "", fmt.Errorf("chain: private key is not 32 bytes")
	}

	var priv, pub [32]byte
	copy(priv[:], privBytes)
	// Clamp private key (X25519 requirement)
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	curve25519.ScalarBaseMult(&pub, &priv)
	return base64.RawURLEncoding.EncodeToString(pub[:]), nil
}

// ApplyChain generates configs for every node in the chain and pushes them via SSH.
//
// The global obfuscation profile (chosen once via config or UI — Russia/Iran/China/etc.)
// is the single source of truth for all obfuscation parameters (XHTTP headers/paths,
// AWG JC/JMIN/JMAX, TUIC settings, Reality SNI/fingerprint, etc.).
//
// Chain.Transport and Chain.UserProtocol control *which* protocol is used,
// while the global profile controls *how* it is obfuscated.
//
// When userProto == AWG and awgClientPubKey == "", a fresh sample client keypair
// is generated on the fly so that the pushed config is immediately usable.
// The sample private key + the generated server pub are returned in the report.
func (a *Applier) ApplyChain(ctx context.Context, chain *model.Chain, awgClientPubKey string) (*ApplyReport, error) {
	if len(chain.Nodes) < 1 {
		return nil, fmt.Errorf("chain: chain %q has no nodes", chain.Name)
	}

	_ = a.factory.Create() // sing-box-extended backend


	// Apply modern defaults if not specified on the chain
	if chain.Transport == "" {
		chain.Transport = model.TransportXHTTP
	}
	if chain.UserProtocol == "" {
		chain.UserProtocol = model.UserProtocolTUIC
	}

	n := len(chain.Nodes)

	// Effective profile (per-chain override wins)
	var preset ConnectionPreset
	if chain.ObfuscationProfile != "" {
		if p, ok := GetPreset(chain.ObfuscationProfile); ok {
			preset = p
		} else {
			preset = GetDefaultPreset()
		}
	} else {
		preset = GetEffectivePreset(chain)
	}

	profileName := GetDefaultPresetName()
	if chain.ObfuscationProfile != "" {
		if _, ok := GetPreset(chain.ObfuscationProfile); ok {
			profileName = chain.ObfuscationProfile
		}
	}

	// AWG special handling for the entry node (i==0): ensure we have a real client pubkey
	// so the generated server config is usable.
	var awgMaterial *AWGClientMaterial
	effectiveClientPub := awgClientPubKey
	if chain.UserProtocol == model.UserProtocolAWG {
		if effectiveClientPub == "" {
			// Auto-generate a convenient sample so apply-chain "just works" for testing/demo.
			cPriv, cPub, genErr := generateWireGuardKeypair()
			if genErr == nil {
				effectiveClientPub = cPub
				awgMaterial = &AWGClientMaterial{
					ClientPubUsed: cPub,
					ClientPriv:    cPriv,
					Note:          "Sample client keypair auto-generated by apply-chain for convenience. Replace with your own for production.",
				}
			} else {
				// Extremely rare; fall back to placeholder (will be non-functional until user fixes)
				effectiveClientPub = ""
			}
		} else {
			awgMaterial = &AWGClientMaterial{
				ClientPubUsed: effectiveClientPub,
				Note:          "Used client public key supplied via --client-pubkey.",
			}
		}
	}

	// Generate hop params (Reality keys for the vless layer) from last to first.
	params := make([]*hopParams, n)
	for i := n - 1; i >= 0; i-- {
		node := &chain.Nodes[i]
		if node.Port == 0 {
			node.Port = defaultTransportPort
		}
		p, err := generateHopParams(node.Port, &preset)
		if err != nil {
			return nil, fmt.Errorf("chain: node %q: generate params: %w", node.ID, err)
		}
		params[i] = p
	}

	// Build + push loop with rich per-node results.
	results := make([]NodeResult, 0, n)
	var entryAWGServerPub string

	for i := 0; i < n; i++ {
		node := &chain.Nodes[i]

		// Special case for entry node + AWG: we must use the (possibly auto-generated) client pub
		// and capture the server pub that was generated for the user-in inbound.
		var cfg string
		var buildErr error
		if i == 0 && chain.UserProtocol == model.UserProtocolAWG {
			// STABLE AWG user entry creds (the big change for "works like clockwork"):
			// Reuse the keypair that was generated once at chain creation time (stored on the Chain).
			// This way client configs do not break when you re-apply the chain after changing transport hops.
			// Only transport (inter-hop) Reality keys rotate on every apply.
			serverPrivForAWG := chain.AWGEntryServerPriv
			serverPubForAWG := chain.AWGEntryServerPub

			if serverPrivForAWG == "" {
				// Fallback: first time or legacy chain without stored creds → generate once and we should persist it
				// (in practice this is now done at creation time in CLI/UI).
				if priv, pub, kerr := generateWireGuardKeypair(); kerr == nil {
					serverPrivForAWG = priv
					serverPubForAWG = pub
				}
			}

			// Build the node config but force the correct client pub for the user inbound
			cfg, buildErr = buildNodeConfigWithAWGClient(node, i, n, params, chain.Nodes, &preset, chain.Transport, chain.UserProtocol, effectiveClientPub, serverPrivForAWG, chain.Strategy)
			if buildErr == nil && serverPubForAWG != "" {
				entryAWGServerPub = serverPubForAWG
			}
		} else {
			cfg, buildErr = buildNodeConfig(node, i, n, params, chain.Nodes, &preset, chain.Transport, chain.UserProtocol, chain.Strategy)
		}

		if buildErr != nil {
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "build config: " + buildErr.Error()})
			continue
		}

		client, err := sshclient.Connect(node.Addr, node.User, node.KeyPath)
		if err != nil {
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "ssh connect: " + err.Error()})
			continue
		}
		_, err = pushConfig(client, cfg, chain.UserProtocol)
		client.Close()
		if err != nil {
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "push config: " + err.Error()})
			continue
		}

		results = append(results, NodeResult{ID: node.ID, Success: true})
	}

	// Fill AWG material with server pub if we have it
	if awgMaterial != nil && entryAWGServerPub != "" {
		awgMaterial.ServerPub = entryAWGServerPub
	}

	// 2026: populate CPS / I1 info into the report so UI/CLI can show the user
	// exactly what stealth material was deployed (very useful for debugging).
	if awgMaterial != nil {
		level := 0
		mim := "none"
		if preset.CPSLevel > 0 {
			level = preset.CPSLevel
			mim = preset.AWGMimicry
		} else if preset.AWG != nil && preset.AWG.CPSLevel > 0 {
			level = preset.AWG.CPSLevel
			mim = preset.AWG.Mimicry
		}
		if level > 0 {
			awgMaterial.CPSLevel = level
			awgMaterial.Mimicry = mim
			// We know from the generator that QUIC level 3 gives 1200B I1
			if mim == "quic" && level >= 1 {
				awgMaterial.I1Len = 1200
				awgMaterial.I1Type = "quic-initial-chrome"
			}
		}
	}

	// Note: pushConfig already performed reload/restart + validation for every node.
	// We no longer do a second b.Reload here, because it was poisoning successful nodes
	// (the second reload could fail for transient reasons even though the config was already active).
	// This avoids the "double apply" problem identified in review.


	report := &ApplyReport{
		ChainName: chain.Name,
		Profile:   profileName,
		Transport: chain.Transport,
		UserProto: chain.UserProtocol,
		Nodes:     results,
		AWG:       awgMaterial,
	}

	// Any failure?
	failed := []string{}
	for _, r := range results {
		if !r.Success {
			failed = append(failed, fmt.Sprintf("%s: %s", r.ID, r.Error))
		}
	}
	if len(failed) > 0 {
		return report, fmt.Errorf("chain %q apply failed on nodes: %s", chain.Name, strings.Join(failed, "; "))
	}

	return report, nil
}

func generateHopParams(port int, preset *ConnectionPreset) (*hopParams, error) {
	// sing-box 1.12.0+ uses 32-byte random keys for Reality (X25519), not RSA.
	privKeyBytes := make([]byte, 32)
	if _, err := rand.Read(privKeyBytes); err != nil {
		return nil, fmt.Errorf("generate reality key: %w", err)
	}
	privKeyB64 := base64.RawURLEncoding.EncodeToString(privKeyBytes)

	shortID := make([]byte, 8)
	if _, err := rand.Read(shortID); err != nil {
		return nil, fmt.Errorf("generate shortId: %w", err)
	}

	// Prefer Reality preset, fallback to XHTTP host, then random
	serverName := "www.microsoft.com"
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		serverName = preset.Reality.ServerNames[0] // можно добавить рандомизацию позже
	} else if preset.XHTTP != nil && len(preset.XHTTP.Hosts) > 0 {
		serverName = preset.XHTTP.Hosts[0]
	}

	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	return &hopParams{
		UUID:       fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]),
		PrivateKey: privKeyB64,
		ShortID:    hex.EncodeToString(shortID),
		ServerName: serverName,
		Port:       port,
	}, nil
}

// buildNodeConfig constructs the full sing-box config for a node at position i of n.
func buildNodeConfig(node *model.ChainNode, i, n int, params []*hopParams, nodes []model.ChainNode, preset *ConnectionPreset, transport model.TransportType, userProto model.UserProtocol, strategy model.Strategy) (string, error) {
	var inbounds []json.RawMessage
	var outbounds []json.RawMessage
	var endpoints []json.RawMessage
	tags := []string{}

	// Pre-build outbounds so we know their tags for detour references.
	if i < n-1 {
		next := params[i+1]
		nextAddr := extractHost(nodes[i+1].Addr)
		tag := fmt.Sprintf("out-to-%s", next.ServerName)
		var outb json.RawMessage
		var err error
		if transport == model.TransportXHTTP {
			outb, err = buildXHTTPTransportOutbound(next, nextAddr, tag, preset)
		} else {
			outb, err = buildTransportOutbound(next, nextAddr, tag)
		}
		if err != nil {
			return "", fmt.Errorf("build outbound to next hop: %w", err)
		}
		outbounds = append(outbounds, outb)
	}
	if i == n-1 {
		outbounds = append(outbounds, buildDirectOutbound("direct-out"))
	}

	// Every node except the first (entry) gets a transport inbound for the previous hop.
	if i > 0 {
		tag := "transport-in"
		tags = append(tags, tag)
		var inb json.RawMessage
		if transport == model.TransportXHTTP {
			inb = buildXHTTPTransportInbound(params[i], tag, preset)
		} else {
			inb = buildTransportInbound(params[i], tag)
		}
		inbounds = append(inbounds, inb)
	}

	// The first node gets a user-facing entry.
	if i == 0 {
		port := defaultUserPort // always use user port for entry
		tag := "user-in"
		tags = append(tags, tag)

		switch userProto {
		case model.UserProtocolTUIC:
			inb := buildTUICUserInbound(port, params[i].UUID, tag, preset)
			inbounds = append(inbounds, inb)
		case model.UserProtocolAWG:
			ep, tunInb, _, err := buildAWGUserInbound(port, params[i].UUID, tag, preset, "", "")
			if err != nil {
				return "", fmt.Errorf("build awg endpoint: %w", err)
			}
			endpoints = append(endpoints, ep)
			inbounds = append(inbounds, tunInb)
		default:
			inb := buildUserInbound(port, params[i].UUID, tag)
			inbounds = append(inbounds, inb)
		}
	}

	// Build routing + strategy + DNS
	var route interface{}
	var strategyTag string
	if len(outbounds) > 0 {
		var firstOut map[string]any
		json.Unmarshal(outbounds[0], &firstOut)
		outTag, _ := firstOut["tag"].(string)

		// Apply chain strategy: wrap outbounds in urltest/selector/failover
		stratOut := BuildStrategyOutbound(string(strategy), []string{outTag})
		if stratOut != nil {
			stratJSON, _ := json.Marshal(stratOut)
			outbounds = append(outbounds, stratJSON)
			strategyTag = stratOut.Tag
		} else {
			strategyTag = outTag
		}

		// Generate full routing section from preset
		routingSection := BuildRoutingSection(preset, strategyTag)
		route = routingSection

		// Add block/direct outbounds as needed
		if len(routingSection.Rules) > 0 {
			hasBlock := false
			hasDirect := false
			for _, r := range routingSection.Rules {
				if r.Outbound == "block" {
					hasBlock = true
				}
				if r.Outbound == "direct-out" {
					hasDirect = true
				}
			}
			if hasBlock {
				blockOut := map[string]any{"type": "block", "tag": "block"}
				blockJSON, _ := json.Marshal(blockOut)
				outbounds = append(outbounds, blockJSON)
			}
			if hasDirect {
				found := false
				for _, ob := range outbounds {
					var m map[string]any
					json.Unmarshal(ob, &m)
					if m["tag"] == "direct-out" {
						found = true
						break
					}
				}
				if !found {
					outbounds = append(outbounds, buildDirectOutbound("direct-out"))
				}
			}
		}
	}

	cfg := map[string]any{
		"log": map[string]any{
			"level":  "info",
			"output": "/var/log/sing-box/sing-box.log",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
	}
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}

	if route != nil {
		cfg["route"] = route
		// DNS with detour through strategy outbound
		dnsTag := strategyTag
		if dnsTag == "" {
			dnsTag = "direct-out"
		}
		cfg["dns"] = BuildDNSWithDetour(dnsTag, preset.Routing.DirectDomains)
	}

	// cache_file for rule_set
	cfg["experimental"] = map[string]any{
		"cache_file": map[string]any{
			"enabled": true,
		},
	}

	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	return string(content), nil
}

// buildNodeConfigWithAWGClient is like buildNodeConfig but forces a specific clientPubKey
// for the user-facing AWG inbound on the entry node (i==0). Used by ApplyChain so that
// auto-generated or supplied client keys are actually used in the pushed config.
func buildNodeConfigWithAWGClient(node *model.ChainNode, i, n int, params []*hopParams, nodes []model.ChainNode, preset *ConnectionPreset, transport model.TransportType, userProto model.UserProtocol, awgClientPub string, serverAWGPriv string, strategy model.Strategy) (string, error) {
	// For non-entry or non-AWG we can delegate.
	if i != 0 || userProto != model.UserProtocolAWG {
		return buildNodeConfig(node, i, n, params, nodes, preset, transport, userProto, strategy)
	}

	var inbounds []json.RawMessage
	var outbounds []json.RawMessage
	var endpoints []json.RawMessage
	tags := []string{}

	// Pre-build outbounds so we know the detour tag for the endpoint.
	if i < n-1 {
		next := params[i+1]
		nextAddr := extractHost(nodes[i+1].Addr)
		outTag := fmt.Sprintf("out-to-%s", next.ServerName)
		var outb json.RawMessage
		var err error
		if transport == model.TransportXHTTP {
			outb, err = buildXHTTPTransportOutbound(next, nextAddr, outTag, preset)
		} else {
			outb, err = buildTransportOutbound(next, nextAddr, outTag)
		}
		if err != nil {
			return "", fmt.Errorf("build outbound: %w", err)
		}
		outbounds = append(outbounds, outb)
	} else {
		outbounds = append(outbounds, buildDirectOutbound("direct-out"))
	}

	// Entry node + AWG: wireguard endpoint + TUN inbound
	port := defaultUserPort // always use user port for entry
	tag := "user-in"
	tags = append(tags, tag)

	ep, tunInb, _, err := buildAWGUserInbound(port, params[i].UUID, tag, preset, awgClientPub, serverAWGPriv)
	if err != nil {
		return "", fmt.Errorf("build awg endpoint: %w", err)
	}
	endpoints = append(endpoints, ep)
	inbounds = append(inbounds, tunInb)

	// Routing + strategy + DNS
	var route interface{}
	var strategyTag string
	if len(outbounds) > 0 {
		var firstOut map[string]any
		json.Unmarshal(outbounds[0], &firstOut)
		outTag, _ := firstOut["tag"].(string)

		// Apply chain strategy: wrap outbounds
		stratOut := BuildStrategyOutbound(string(strategy), []string{outTag})
		if stratOut != nil {
			stratJSON, _ := json.Marshal(stratOut)
			outbounds = append(outbounds, stratJSON)
			strategyTag = stratOut.Tag
		} else {
			strategyTag = outTag
		}

		routingSection := BuildRoutingSection(preset, strategyTag)
		route = routingSection

		hasBlock, hasDirect := false, false
		for _, r := range routingSection.Rules {
			if r.Outbound == "block" { hasBlock = true }
			if r.Outbound == "direct-out" { hasDirect = true }
		}
		if hasBlock {
			blockOut := map[string]any{"type": "block", "tag": "block"}
			blockJSON, _ := json.Marshal(blockOut)
			outbounds = append(outbounds, blockJSON)
		}
		if hasDirect {
			found := false
			for _, ob := range outbounds {
				var m map[string]any
				json.Unmarshal(ob, &m)
				if m["tag"] == "direct-out" { found = true; break }
			}
			if !found {
				outbounds = append(outbounds, buildDirectOutbound("direct-out"))
			}
		}
	}

	cfg := map[string]any{
		"log": map[string]any{
			"level":  "info",
			"output": "/var/log/sing-box/sing-box.log",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
	}
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}
	if route != nil {
		cfg["route"] = route
		dnsTag := strategyTag
		if dnsTag == "" { dnsTag = "direct-out" }
		cfg["dns"] = BuildDNSWithDetour(dnsTag, preset.Routing.DirectDomains)
	}
	cfg["experimental"] = map[string]any{
		"cache_file": map[string]any{"enabled": true},
	}

	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil { return "", err }
	return string(content), nil
}

type routeConfig struct {
	Rules               []routeRule `json:"rules"`
	Final               string      `json:"final,omitempty"`
	AutoDetectInterface bool        `json:"auto_detect_interface,omitempty"`
}

type routeRule struct {
	Inbound  []string `json:"inbound,omitempty"`
	Outbound string   `json:"outbound"`
}

func buildTransportInbound(p *hopParams, tag string) json.RawMessage {
	inb := map[string]any{
		"type": "vless",
		"tag":  tag,
		"listen":      "0.0.0.0",
		"listen_port": p.Port,
		"users": []map[string]any{
			{
				"name": tag,
				"uuid": p.UUID,
				"flow": "xtls-rprx-vision",
			},
		},
		"tls": map[string]any{
			"enabled": true,
			"server_name": p.ServerName,
			"reality": map[string]any{
				"enabled":     true,
				"private_key": p.PrivateKey,
				"short_id":    []string{p.ShortID},
			},
		},
		"multiplex": map[string]any{
			"enabled": true,
		},
		"transport": map[string]any{
			"type": "tcp",
		},
	}

	data, _ := json.Marshal(inb)
	return data
}

func buildUserInbound(port int, uuid, tag string) json.RawMessage {
	inb := map[string]any{
		"type": "vless",
		"tag":  tag,
		"listen":      "0.0.0.0",
		"listen_port": port,
		"users": []map[string]any{
			{
				"name": tag,
				"uuid": uuid,
				"flow": "xtls-rprx-vision",
			},
		},
		"tls": map[string]any{
			"enabled": false,
		},
		"transport": map[string]any{
			"type":        "ws",
			"ws_settings": map[string]any{"path": "/ws"},
		},
	}

	data, _ := json.Marshal(inb)
	return data
}

func buildTransportOutbound(next *hopParams, serverAddr, tag string) (json.RawMessage, error) {
	pubKeyHex, err := next.publicKeyB64()
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}

	out := map[string]any{
		"type":        "vless",
		"tag":         tag,
		"server":      serverAddr,
		"server_port": next.Port,
		"uuid":        next.UUID,
		"flow":        "xtls-rprx-vision",
		"tls": map[string]any{
			"enabled":     true,
			"server_name": next.ServerName,
			"utls": map[string]any{
				"enabled":     true,
				"fingerprint": "chrome",
			},
			"reality": map[string]any{
				"enabled":    true,
				"public_key": pubKeyHex,
				"short_id":   next.ShortID,
			},
		},
		"multiplex": map[string]any{
			"enabled": true,
		},
		"transport": map[string]any{
			"type": "tcp",
		},
	}

	data, _ := json.Marshal(out)
	return data, nil
}

func buildDirectOutbound(tag string) json.RawMessage {
	out := map[string]any{
		"type": "direct",
		"tag":  tag,
	}
	data, _ := json.Marshal(out)
	return data
}

// extractHost strips the port from an address like "1.2.3.4:22" or returns the string as-is.
func extractHost(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}

// pushConfig writes the config to the remote host, validates, and applies it.
// It tries to be smart about reload vs restart and gives better diagnostics.
func pushConfig(client *sshclient.Client, cfgContent string, userProtocol model.UserProtocol) (string, error) {
	var js json.RawMessage
	if err := json.Unmarshal([]byte(cfgContent), &js); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	// Backup existing config
	_, _ = client.Run("if [ -f /etc/sing-box/config.json ]; then cp /etc/sing-box/config.json /etc/sing-box/config.json.bak.$(date +%s); fi")

	writeCmd := fmt.Sprintf("mkdir -p /etc/sing-box && cat > /etc/sing-box/config.json << 'CONFIG_EOF'\n%s\nCONFIG_EOF", cfgContent)
	if _, err := client.Run(writeCmd); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}

	// Validate
	if _, err := client.Run("sing-box check -c /etc/sing-box/config.json"); err != nil {
		_, _ = client.Run(`latest=$(ls -t /etc/sing-box/config.json.bak.* 2>/dev/null | head -1); [ -n "$latest" ] && cp "$latest" /etc/sing-box/config.json`)
		return "", fmt.Errorf("config validation failed (rollback attempted): %w", err)
	}

	// Protocol-aware apply:
	// For TUIC and AWG (wireguard inbound) reload is usually enough and less disruptive.
	// For XHTTP/VLESS transport changes a full restart is often safer.
	// We try the gentlest option first, then escalate.
	reloadCmd := "sing-box reload -c /etc/sing-box/config.json 2>/dev/null || systemctl reload sing-box 2>/dev/null || systemctl restart sing-box"
	out, err := client.Run(reloadCmd)
	if err != nil {
		return "", fmt.Errorf("failed to apply config (protocol=%s): %w", userProtocol, err)
	}

	return out, nil
}

// ==================== XHTTP Transport Support ====================
// XHTTP provides better obfuscation for transport links between nodes.

func buildXHTTPTransportInbound(p *hopParams, tag string, preset *ConnectionPreset) json.RawMessage {
	xhttp := preset.XHTTP
	if xhttp == nil || len(xhttp.Methods) == 0 || len(xhttp.Paths) == 0 {
		xhttp = &XHTTPPreset{
			Methods: []string{"POST"},
			Paths:   []string{"/api/v1/" + p.ShortID[:4]},
			Hosts:   []string{p.ServerName},
			Headers: map[string][]string{
				"User-Agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
				"Content-Type":    {"application/json"},
				"Accept":          {"application/json, text/plain, */*"},
				"Accept-Language": {"en-US,en;q=0.9"},
			},
		}
	}

	// Use first option from the preset lists for determinism within a chain.
	path := xhttp.Paths[0]
	method := xhttp.Methods[0]
	headers := xhttp.Headers
	if len(headers) == 0 {
		headers = map[string][]string{
			"User-Agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
			"Content-Type":    {"application/json"},
			"Accept":          {"application/json, text/plain, */*"},
			"Accept-Language": {"en-US,en;q=0.9"},
		}
	}

	transport := map[string]any{
		"type":         "http",
		"host":         []string{p.ServerName},
		"path":         path,
		"method":       method,
		"headers":      headers,
		"idle_timeout": "15s",
		"ping_timeout": "15s",
	}

	// Apply advanced 2026 XHTTP obfuscation (padding, XMUX, realistic headers, upstream/downstream)
	ApplyXHTTPObfuscation(transport, xhttp)

	inb := map[string]any{
		"type": "vless",
		"tag":  tag,
		"listen":      "0.0.0.0",
		"listen_port": p.Port,
		"users": []map[string]any{
			{
				"name": tag,
				"uuid": p.UUID,
				"flow": "xtls-rprx-vision",
			},
		},
		"tls": map[string]any{
			"enabled": true,
			"server_name": p.ServerName,
			"reality": map[string]any{
				"enabled":     true,
				"private_key": p.PrivateKey,
				"short_id":    []string{p.ShortID},
			},
		},
		"transport": transport,
		"multiplex": map[string]any{
			"enabled": true,
		},
	}

	data, _ := json.Marshal(inb)
	return data
}

func buildXHTTPTransportOutbound(next *hopParams, serverAddr, tag string, preset *ConnectionPreset) (json.RawMessage, error) {
	pubKeyHex, err := next.publicKeyB64()
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}

	xhttp := preset.XHTTP
	if xhttp == nil || len(xhttp.Methods) == 0 || len(xhttp.Paths) == 0 {
		xhttp = &XHTTPPreset{
			Methods: []string{"POST"},
			Paths:   []string{"/api/v1/" + next.ShortID[:4]},
			Hosts:   []string{next.ServerName},
			Headers: map[string][]string{
				"User-Agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
				"Content-Type":    {"application/json"},
				"Accept":          {"application/json, text/plain, */*"},
				"Accept-Language": {"en-US,en;q=0.9"},
			},
		}
	}

	// Use first option from the preset lists for determinism within a chain (can add per-hop randomization later if desired for extra stealth).
	path := xhttp.Paths[0]
	method := xhttp.Methods[0]
	headers := xhttp.Headers
	if len(headers) == 0 {
		headers = map[string][]string{
			"User-Agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
			"Content-Type":    {"application/json"},
			"Accept":          {"application/json, text/plain, */*"},
			"Accept-Language": {"en-US,en;q=0.9"},
		}
	}

	fingerprint := "chrome"
	if preset.Reality != nil && len(preset.Reality.Fingerprints) > 0 {
		fingerprint = preset.Reality.Fingerprints[0]
	}

	out := map[string]any{
		"type":        "vless",
		"tag":         tag,
		"server":      serverAddr,
		"server_port": next.Port,
		"uuid":        next.UUID,
		"flow":        "xtls-rprx-vision",
		"tls": map[string]any{
			"enabled":     true,
			"server_name": next.ServerName,
			"utls": map[string]any{
				"enabled":     true,
				"fingerprint": fingerprint,
			},
			"reality": map[string]any{
				"enabled":    true,
				"public_key": pubKeyHex,
				"short_id":   next.ShortID,
			},
		},
		"transport": map[string]any{
			"type":         "http",
			"host":         []string{next.ServerName},
			"path":         path,
			"method":       method,
			"headers":      headers,
			"idle_timeout": "15s",
			"ping_timeout": "15s",
		},
		"multiplex": map[string]any{
			"enabled": true,
		},
	}

	// Apply advanced 2026 XHTTP obfuscation on the outbound transport as well
	ApplyXHTTPObfuscation(out["transport"].(map[string]any), xhttp)

	data, _ := json.Marshal(out)
	return data, nil
}

// ==================== User Protocols (TUIC, AWG) ====================

func buildTUICUserInbound(port int, uuid, tag string, preset *ConnectionPreset) json.RawMessage {
	tuic := preset.TUIC
	if tuic == nil {
		tuic = &TUICPreset{
			CongestionControls: []string{"bbr"},
			AuthTimeout:        "3s",
		}
	}

	congestion := "bbr"
	if len(tuic.CongestionControls) > 0 {
		congestion = tuic.CongestionControls[0]
	}

	authTimeout := tuic.AuthTimeout
	if authTimeout == "" {
		authTimeout = "3s"
	}

	// Базовый TUIC + опционально Reality из пресета
	inb := map[string]any{
		"type": "tuic",
		"tag":  tag,
		"listen":      "0.0.0.0",
		"listen_port": port,
		"users": []map[string]any{
			{
				"uuid":     uuid,
				"password": uuid,
			},
		},
		"congestion_control": congestion,
		"auth_timeout":       authTimeout,
		"zero_rtt_handshake": true,
		"heartbeat":          "10s",
	}

	// Pull best server_name from the preset (Reality section if present — good for consistency with transport)
	serverName := "www.microsoft.com"
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		serverName = preset.Reality.ServerNames[0]
	}

	inb["tls"] = map[string]any{
		"enabled":     true,
		"server_name": serverName,
	}

	// If the chosen country preset has Reality settings defined, we can layer Reality on top of TUIC
	// (very strong combination in some environments). Keys are intentionally left for future generation logic.
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		// TUIC + Reality user entry combination is not supported in v0.2.0 (documented limitation).
	}

	data, _ := json.Marshal(inb)
	return data
}

func buildAWGUserInbound(port int, uuid, tag string, preset *ConnectionPreset, clientPubKey string, serverPrivKeyB64 string) (json.RawMessage, json.RawMessage, string, error) {
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{
			JC: 4, JMIN: 40, JMAX: 70,
			S1: 0, S2: 0,
			H1: 1, H2: 2, H3: 3, H4: 4,
		}
	}

	var privKeyB64, pubKeyB64 string
	var err error

	if serverPrivKeyB64 != "" {
		privKeyB64 = serverPrivKeyB64
		pubKeyB64, err = deriveWireGuardPublicFromPrivate(privKeyB64)
		if err != nil {
			return nil, nil, "", fmt.Errorf("derive awg pub from provided priv: %w", err)
		}
	} else {
		privKeyB64, pubKeyB64, err = generateWireGuardKeypair()
		if err != nil {
			return nil, nil, "", fmt.Errorf("generate awg server keypair: %w", err)
		}
	}

	peerPub := clientPubKey
	if peerPub == "" {
		peerPub = "CLIENT_PUBLIC_KEY_HERE"
	}

	// sing-box-extended: WireGuard SERVER endpoint (listen_port, no detour).
	// Endpoint and TUN use different subnets to avoid IP conflicts:
	// - Endpoint (AmneziaWG): 10.8.0.1/32, client peer: 10.8.0.2/32
	// - TUN inbound (internal routing): 10.8.1.1/30
	ep := map[string]any{
		"type":        "wireguard",
		"tag":         "wg-ep",
		"system":      false,
		"mtu":         1420,
		"address":     []string{"10.8.0.1/32"},
		"private_key": privKeyB64,
		"listen_port": port,
		"peers": []map[string]any{
			{
				"public_key":  peerPub,
				"allowed_ips": []string{"10.8.0.2/32"},
			},
		},
		"amnezia": BuildAmneziaSection(awg, preset),
	}

	epJSON, _ := json.Marshal(ep)

	// TUN inbound on separate subnet to capture routed traffic
	tunInb := map[string]any{
		"type":           "tun",
		"tag":            tag,
		"interface_name": "angry-tun",
		"address":        []string{"10.8.1.1/30"},
		"mtu":            1420,
		"stack":          "system",
		"auto_route":     true,
	}
	tunJSON, _ := json.Marshal(tunInb)

	return epJSON, tunJSON, pubKeyB64, nil
}

// deriveWireGuardPublicFromPrivate takes a base64 WireGuard private key and returns the corresponding public key.
func deriveWireGuardPublicFromPrivate(privB64 string) (string, error) {
	privBytes, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return "", fmt.Errorf("decode priv: %w", err)
	}
	if len(privBytes) != 32 {
		return "", fmt.Errorf("invalid priv length")
	}

	var priv [32]byte
	copy(priv[:], privBytes)

	// Clamp (same as generation)
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	var pub [32]byte
	curve25519.ScalarBaseMult(&pub, &priv)

	return base64.StdEncoding.EncodeToString(pub[:]), nil
}

// BuildXHTTPTransportInboundForStandalone builds a vless+reality+xhttp inbound
// suitable for standalone "config -type transport" use. It pulls the obfuscation
// details (paths, methods, headers, fingerprint) from the given preset.
func BuildXHTTPTransportInboundForStandalone(port int, uuid, privKeyB64, shortID, serverName string, preset *ConnectionPreset) json.RawMessage {
	xhttp := preset.XHTTP
	if xhttp == nil || len(xhttp.Methods) == 0 || len(xhttp.Paths) == 0 {
		// Use the same rich fallback as the chain builders for consistency
		xhttp = &XHTTPPreset{
			Methods: []string{"POST"},
			Paths:   []string{"/api/v1/" + shortID[:4]},
			Hosts:   []string{serverName},
			Headers: map[string][]string{
				"User-Agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
				"Content-Type":    {"application/json"},
				"Accept":          {"application/json, text/plain, */*"},
				"Accept-Language": {"en-US,en;q=0.9"},
			},
		}
	}
	path := xhttp.Paths[0]
	method := xhttp.Methods[0]
	headers := xhttp.Headers
	if len(headers) == 0 {
		headers = map[string][]string{
			"User-Agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
			"Content-Type":    {"application/json"},
			"Accept":          {"application/json, text/plain, */*"},
			"Accept-Language": {"en-US,en;q=0.9"},
		}
	}

	inb := map[string]any{
		"type": "vless",
		"tag":  "transport-in",
		"listen":      "0.0.0.0",
		"listen_port": port,
		"users": []map[string]any{{
			"name": "transport",
			"uuid": uuid,
			"flow": "xtls-rprx-vision",
		}},
		"tls": map[string]any{
			"enabled":     true,
			"server_name": serverName,
			"reality": map[string]any{
				"enabled":     true,
	"private_key": privKeyB64,
				"short_id":    []string{shortID},
			},
		},
		"transport": map[string]any{
			"type":         "http",
			"host":         []string{serverName},
			"path":         path,
			"method":       method,
			"headers":      headers,
			"idle_timeout": "15s",
			"ping_timeout": "15s",
		},
		"multiplex": map[string]any{"enabled": true},
	}
	data, _ := json.Marshal(inb)
	return data
}

// GenerateWireGuardKeypair generates a proper Curve25519 keypair for WireGuard / AmneziaWG.
// Exported so CLI and other packages can generate consistent client samples.
// Returns base64-encoded private and public keys.
func GenerateWireGuardKeypair() (privateKeyB64, publicKeyB64 string, err error) {
	var privateKey [32]byte
	if _, err = rand.Read(privateKey[:]); err != nil {
		return "", "", fmt.Errorf("generate wireguard private key: %w", err)
	}

	// Clamp private key (WireGuard requirement)
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64

	var publicKey [32]byte
	curve25519.ScalarBaseMult(&publicKey, &privateKey)

	privateKeyB64 = base64.StdEncoding.EncodeToString(privateKey[:])
	publicKeyB64 = base64.StdEncoding.EncodeToString(publicKey[:])
	return privateKeyB64, publicKeyB64, nil
}

// generateWireGuardKeypair is the internal version (kept for backward compat inside package).
func generateWireGuardKeypair() (privateKeyB64, publicKeyB64 string, err error) {
	return GenerateWireGuardKeypair()
}

// GenerateStableTUICUserCreds generates stable UUID + password for a TUIC user entry at chain creation time.
func GenerateStableTUICUserCreds() (uuid, password string) {
	uuid = generateStableUUID()
	return uuid, uuid
}

// generateStableUUID is a small helper for creation-time stable user creds.
func generateStableUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
