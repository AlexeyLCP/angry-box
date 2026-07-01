package singbox

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
	"golang.org/x/crypto/curve25519"
)

// singBoxConfig is the top-level sing-box configuration structure.
type singBoxConfig struct {
	Log       *logConfig        `json:"log,omitempty"`
	Endpoints []json.RawMessage `json:"endpoints,omitempty"`
	Inbounds  []json.RawMessage `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
}

type logConfig struct {
	Level    string `json:"level"`
	Output   string `json:"output"`
	Disabled bool   `json:"disabled"`
}

// GenerateConfig produces a sing-box configuration file for the given type and parameters.
// It uses the global default obfuscation profile (set via config or UI) for best results.
//
// The CLI-facing types (ConfigTransport / ConfigUser) now route through the
// unified role renderer RenderProxyNode (VLESS REALITY+XHTTP max obfuscation)
// instead of the old two divergent branches that produced "fake" configs
// (e.g. a TUIC request returning a wireguard/AWG endpoint). ConfigStandaloneNode
// keeps its multi-inbound path (used by the UI/chain applier).
func (b *Backend) GenerateConfig(cfgType model.ConfigType, params model.ConfigParams) (*model.Config, error) {
	switch cfgType {
	case model.ConfigTransport, model.ConfigUser:
		return b.renderStandaloneFromParams(params)
	case model.ConfigStandaloneNode:
		return b.generateStandaloneNode(params)
	default:
		return nil, fmt.Errorf("singbox: unknown config type %s", cfgType)
	}
}

// renderStandaloneFromParams is the single CLI config path. It honours the
// -protocol/-transport flags and the global default preset to pick a role:
//   - awg        -> userspace AWG wireguard endpoint with amnezia (RenderAWGHop)
//   - tuic       -> TUIC inbound on self-signed TLS (real TUIC, not WG)
//   - else       -> VLESS REALITY+XHTTP max obfuscation (RenderProxyNode)
//
// Credentials are generated fresh here for the CLI's `config` command (which
// prints to stdout); the UI/chain paths persist credentials in the store and
// pass them through NodeInbound instead.
func (b *Backend) renderStandaloneFromParams(params model.ConfigParams) (*model.Config, error) {
	port := params.Port
	if port == 0 {
		port = 443
	}

	protocol := strings.ToLower(params.Protocol)
	if v, ok := params.Extra["transport"].(string); ok && v != "" && protocol == "" {
		// Old CLI sometimes put xhttp/reality in -transport with default VLESS.
		protocol = "vless-reality"
	}
	if protocol == "" || protocol == "vless" {
		protocol = "vless-reality"
	}

	switch protocol {
	case "awg":
		priv, pub, err := generateWGKeypair()
		if err != nil {
			return nil, fmt.Errorf("singbox: generate AWG keys: %w", err)
		}
		// CLI standalone AWG: single peer allowed_ips 0.0.0.0/0, no endpoint
		// (it's a server-side endpoint). clientPub is unused server-side.
		dp := chain.GetDefaultPreset()
		amnezia := chain.BuildAWGAmnezia(dp.AWG, &dp)
		content, err := RenderAWGHop(AWGHopParams{
			Tag:         "awg-in",
			ListenPort:  port,
			Address:     []string{"10.8.0.1/24"},
			PrivateKey:  priv,
			PeerPubKey:  pub,
			Amnezia:     amnezia,
		})
		if err != nil {
			return nil, err
		}
		return &model.Config{Content: string(content), Format: "json", Version: b.Version()}, nil

	case "tuic":
		uuid := generateUUID()
		password := generateUUID()
		inb := config.TUICInbound{
			Type:              "tuic",
			Tag:               "tuic-in",
			Listen:            "0.0.0.0",
			ListenPort:        port,
			Users:             []config.TUICUser{{UUID: uuid, Password: password}},
			CongestionControl: "bbr",
			AuthTimeout:       "3s",
			ZeroRTTHandshake:  true,
			Heartbeat:         "10s",
			TLS: &config.InboundTLSOptions{
				Enabled:         true,
				ServerName:      defaultRealitySNI,
				CertificatePath: "/etc/sing-box/cert.pem",
				KeyPath:         "/etc/sing-box/key.pem",
			},
		}
		inbJSON, _ := json.Marshal(inb)
		cfg := config.SingboxConfig{
			Log:      &config.LogOptions{Level: "info", Timestamp: true},
			Inbounds: []json.RawMessage{inbJSON},
			Outbounds: []json.RawMessage{
				mustMarshal(config.DirectOutbound{Type: "direct", Tag: "direct"}),
				mustMarshal(config.BlockOutbound{Type: "block", Tag: "block"}),
			},
			Route: &config.RoutingSection{
				Rules:               []config.RouteRuleEntry{{Action: "sniff"}, {Protocol: []string{"dns"}, Action: "hijack-dns"}},
				Final:               "direct",
				AutoDetectInterface: true,
			},
		}
		content, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return nil, err
		}
		return &model.Config{Content: string(content), Format: "json", Version: b.Version()}, nil

	default: // vless-reality
		sni := defaultRealitySNI
		if p := chain.GetDefaultPreset(); p.Reality != nil && len(p.Reality.ServerNames) > 0 {
			sni = p.Reality.ServerNames[0]
		}
		content, err := RenderProxyNode(ProxyNodeParams{
			ListenPort: port,
			SNIDomain:  sni,
		})
		if err != nil {
			return nil, err
		}
		return &model.Config{Content: string(content), Format: "json", Version: b.Version()}, nil
	}
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// local copy for standalone generation (to avoid import cycles)
func generateWireGuardKeypair() (privateKeyB64, publicKeyB64 string, err error) {
	var privateKey [32]byte
	if _, err = rand.Read(privateKey[:]); err != nil {
		return "", "", fmt.Errorf("generate wireguard private key: %w", err)
	}
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64

	var publicKey [32]byte
	curve25519.ScalarBaseMult(&publicKey, &privateKey)

	privateKeyB64 = base64.StdEncoding.EncodeToString(privateKey[:])
	publicKeyB64 = base64.StdEncoding.EncodeToString(publicKey[:])
	return privateKeyB64, publicKeyB64, nil
}

// generateAWGUser generates an AmneziaWG user config using the given preset.
// Returns the server-side config + the server's public key (needed for client configs).
func (b *Backend) generateAWGUser(params model.ConfigParams, preset *chain.ConnectionPreset, uuid string, port int, clientPubKey string) (*model.Config, string, error) {
	awg := preset.AWG
	if awg == nil {
		awg = &chain.AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}

	privB64 := ""
	pubB64 := ""
	if v, ok := params.Extra["serverPrivKey"].(string); ok && v != "" {
		privB64 = v
		// We could derive pubB64, but we don't strictly need it here to build the config, 
		// though returning it is nice. If missing, we'll just return what we have.
	} else {
		var err error
		privB64, pubB64, err = generateWireGuardKeypair()
		if err != nil {
			return nil, "", fmt.Errorf("generate awg keypair: %w", err)
		}
	}

	peerPub := clientPubKey
	if peerPub == "" {
		peerPub = "CLIENT_PUBLIC_KEY_HERE"
	}

	// sing-box-extended: WireGuard SERVER endpoint (listen_port, no detour).
	// TUN inbound captures decrypted traffic for routing.
	endpoint := config.WireGuardEndpoint{
		Type:       "wireguard",
		Tag:        "wg-ep",
		System:     false,
		MTU:        1420,
		Address:    []string{"10.8.0.1/32"},
		PrivateKey: privB64,
		ListenPort: port,
		Peers: []config.WireGuardPeer{
			{
				PublicKey:  peerPub,
				AllowedIPs: []string{"10.8.0.2/32"},
			},
		},
		Amnezia: chain.BuildAWGAmnezia(awg, preset),
	}

	epJSON, _ := json.Marshal(endpoint)
	outboundJSON, _ := json.Marshal(config.DirectOutbound{Type: "direct", Tag: "direct-out"})

	tunInbound := config.TUNInbound{
		Type:      "tun",
		Tag:       "tun-in",
		Address:   []string{"172.16.250.1/30"},
		AutoRoute: true,
	}
	inbJSON, _ := json.Marshal(tunInbound)

	cfg := singBoxConfig{
		Log:       &logConfig{Level: "info", Output: "/var/log/sing-box/sing-box.log"},
		Endpoints: []json.RawMessage{epJSON},
		Inbounds:  []json.RawMessage{inbJSON},
		Outbounds: []json.RawMessage{outboundJSON},
	}

	content, _ := json.MarshalIndent(cfg, "", "  ")
	return &model.Config{Content: string(content), Format: "json", Version: b.Version()}, pubB64, nil
}

func (b *Backend) generateStandaloneNode(params model.ConfigParams) (*model.Config, error) {
	inboundsData, ok := params.Extra["inbounds"].([]model.NodeInbound)
	if !ok {
		return nil, fmt.Errorf("singbox: missing or invalid inbounds for standalone node")
	}

	preset := chain.GetDefaultPreset()

	var finalEndpoints []json.RawMessage
	var finalInbounds []json.RawMessage

	for i, ib := range inboundsData {
		uuid := ib.UUID
		tag := fmt.Sprintf("inbound-%d-%s", i, ib.Protocol)
		
		switch ib.Protocol {
		case "awg":
			clientPub := ib.AWGClientPub
			if clientPub == "" {
				// Generate a sample client keypair for this standalone AWG inbound so server has a valid peer.
				if priv, pub, kerr := chain.GenerateWireGuardKeypair(); kerr == nil {
					clientPub = pub
					ib.AWGClientPub = pub
					ib.AWGClientPriv = priv // store priv for use in client configs
				}
			}
			paramsAWG := model.ConfigParams{Extra: map[string]any{"serverPrivKey": ib.ServerPrivKey}}
			cfg, _, err := b.generateAWGUser(paramsAWG, &preset, uuid, ib.Port, clientPub)
			if err != nil {
				continue
			}
			var scfg singBoxConfig
			if err := json.Unmarshal([]byte(cfg.Content), &scfg); err == nil {
				finalEndpoints = append(finalEndpoints, scfg.Endpoints...)
				finalInbounds = append(finalInbounds, scfg.Inbounds...)
			}
		case "tuic":
			serverName := "www.microsoft.com"
			if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
				serverName = preset.Reality.ServerNames[0]
			}

			tls := &config.InboundTLSOptions{
				Enabled:    true,
				ServerName: serverName,
			}

			if ib.TLSCertificate != "" && ib.TLSPrivateKey != "" {
				tls.Certificate = ib.TLSCertificate
				tls.Key = ib.TLSPrivateKey
			}

			inb := config.TUICInbound{
				Type:              "tuic",
				Tag:               tag,
				Listen:            "0.0.0.0",
				ListenPort:        ib.Port,
				Users:             []config.TUICUser{{UUID: uuid, Password: ib.ServerPrivKey}},
				CongestionControl: "bbr",
				AuthTimeout:       "3s",
				ZeroRTTHandshake:  true,
				Heartbeat:         "10s",
				TLS:               tls,
			}
			data, _ := json.Marshal(inb)
			finalInbounds = append(finalInbounds, data)
		case "vless-reality":
			privKeyB64 := ib.ServerPrivKey
			shortIDHex := ib.ShortID
			
			serverName := "www.microsoft.com"
			if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
				serverName = preset.Reality.ServerNames[0]
			}
			inb := config.VLESSInbound{
				Type: "vless", Tag: tag, Listen: "0.0.0.0", ListenPort: ib.Port,
				Users: []config.VLESSUser{{Name: "user", UUID: uuid, Flow: "xtls-rprx-vision"}},
				TLS: &config.InboundTLSOptions{
					Enabled: true, ServerName: serverName,
					Reality: &config.InboundRealityOptions{
						Enabled: true, PrivateKey: privKeyB64, ShortID: []string{shortIDHex},
						Handshake: &config.RealityHandshake{
							Server:     serverName,
							ServerPort: 443,
						},
					},
				},
			}
			data, _ := json.Marshal(inb)
			finalInbounds = append(finalInbounds, data)
		case "xhttp":
			// Basic XHTTP standalone
			inb := config.VLESSInbound{
				Type: "vless", Tag: tag, Listen: "0.0.0.0", ListenPort: ib.Port,
				Users: []config.VLESSUser{{Name: "user", UUID: uuid}},
				TLS: &config.InboundTLSOptions{Enabled: false}, // Assume offloaded or no TLS for raw test
				Transport: &config.TransportOptions{
					Type: "http", Path: "/api", Method: "POST",
					Headers: map[string][]string{"Content-Type": {"application/json"}},
				},
			}
			data, _ := json.Marshal(inb)
			finalInbounds = append(finalInbounds, data)
		case "hysteria2":
			serverName := "www.microsoft.com"
			if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
				serverName = preset.Reality.ServerNames[0]
			}

			hysteria := config.Hysteria2Inbound{
				Type:      "hysteria2",
				Tag:       tag,
				Listen:    "::",
				ListenPort: ib.Port,
				Users:     []config.Hysteria2User{{Password: ib.ServerPrivKey}},
				UpMbps:    1000,
				DownMbps:  1000,
				Obfs:      &config.Hysteria2Obfs{Type: "salamander", Password: ib.ObfsPassword},
			}

			if ib.TLSCertificate != "" && ib.TLSPrivateKey != "" {
				hysteria.TLS = &config.InboundTLSOptions{
					Enabled:     true,
					ServerName:  serverName,
					Certificate: ib.TLSCertificate,
					Key:         ib.TLSPrivateKey,
				}
			}

			data, _ := json.Marshal(hysteria)
			finalInbounds = append(finalInbounds, data)
		default:
			// Fallback user inbound (ws)
			inb := config.VLESSInbound{
				Type: "vless", Tag: tag, Listen: "0.0.0.0", ListenPort: ib.Port,
				Users: []config.VLESSUser{{Name: "user", UUID: uuid, Flow: "xtls-rprx-vision"}},
				TLS: &config.InboundTLSOptions{Enabled: false},
				Transport: &config.TransportOptions{Type: "ws", Path: "/ws"},
			}
			data, _ := json.Marshal(inb)
			finalInbounds = append(finalInbounds, data)
		}
	}
	
	outbound := config.DirectOutbound{Type: "direct", Tag: "direct-out"}
	outboundJSON, _ := json.Marshal(outbound)
	
	// Better routing support using the chain builder
	routingSection := chain.BuildRoutingSection(&preset, "direct-out")
	dnsSection := chain.BuildDNSWithDetour("direct-out", preset.Routing.DirectDomains)
	
	cfg := singBoxConfig{
		Log: &logConfig{Level: "info", Output: "/var/log/sing-box/sing-box.log"},
		Endpoints: finalEndpoints,
		Inbounds: finalInbounds,
		Outbounds: []json.RawMessage{outboundJSON},
	}
	
	type singBoxConfigWithRoute struct {
		singBoxConfig
		Route *config.RoutingSection `json:"route,omitempty"`
		DNS   *config.DNSConfig      `json:"dns,omitempty"`
	}
	
	fullCfg := singBoxConfigWithRoute{
		singBoxConfig: cfg,
		Route: &routingSection,
		DNS: dnsSection,
	}
	
	content, err := json.MarshalIndent(fullCfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return &model.Config{Content: string(content), Format: "json", Version: b.Version()}, nil
}
