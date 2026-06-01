package singbox

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
func (b *Backend) GenerateConfig(cfgType model.ConfigType, params model.ConfigParams) (*model.Config, error) {
	switch cfgType {
	case model.ConfigTransport:
		return b.generateTransport(params)
	case model.ConfigUser:
		return b.generateUser(params)
	case model.ConfigStandaloneNode:
		return b.generateStandaloneNode(params)
	default:
		return nil, fmt.Errorf("singbox: unknown config type %s", cfgType)
	}
}

func (b *Backend) generateTransport(params model.ConfigParams) (*model.Config, error) {
	port := params.Port
	if port == 0 {
		port = 443
	}

	// Use global default profile for good obfuscation settings.
	// The --transport flag (when passed to `config`) is forwarded via params.Extra["transport"].
	preset := chain.GetDefaultPreset()

	transportKind := "xhttp"
	if v, ok := params.Extra["transport"].(string); ok && v != "" {
		transportKind = strings.ToLower(v)
	} else if preset.XHTTP == nil || len(preset.XHTTP.Paths) == 0 {
		transportKind = "reality"
	}

	// sing-box 1.12.0+ uses 32-byte X25519 keys for Reality
	privKeyBytes := make([]byte, 32)
	if _, err := rand.Read(privKeyBytes); err != nil {
		return nil, fmt.Errorf("singbox: generate reality key: %w", err)
	}
	privKeyB64 := base64.RawURLEncoding.EncodeToString(privKeyBytes)

	shortID := make([]byte, 8)
	if _, err := rand.Read(shortID); err != nil {
		return nil, fmt.Errorf("singbox: generate shortId: %w", err)
	}
	shortIDHex := hex.EncodeToString(shortID)

	serverName := "www.microsoft.com"
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		serverName = preset.Reality.ServerNames[0]
	} else if preset.XHTTP != nil && len(preset.XHTTP.Hosts) > 0 {
		serverName = preset.XHTTP.Hosts[0]
	}

	uuid := generateUUID()

	var inboundJSON json.RawMessage
	if transportKind == "xhttp" {
		inboundJSON = chain.BuildXHTTPTransportInboundForStandalone(port, uuid, privKeyB64, shortIDHex, serverName, &preset)
	} else {
		// Classic Reality+TCP fallback
		inb := config.VLESSInbound{
			Type:       "vless",
			Tag:        "transport-in",
			Listen:     "0.0.0.0",
			ListenPort: port,
			Users: []config.VLESSUser{{
				Name: "transport",
				UUID: uuid,
				Flow: "xtls-rprx-vision",
			}},
			TLS: &config.InboundTLSOptions{
				Enabled:    true,
				ServerName: serverName,
				Reality: &config.InboundRealityOptions{
					Enabled:    true,
					PrivateKey: privKeyB64,
					ShortID:    []string{shortIDHex},
				},
			},
			Multiplex: &config.MultiplexOptions{Enabled: true},
			Transport: &config.TransportOptions{Type: "tcp"},
		}
		inboundJSON, _ = json.Marshal(inb)
	}

	outbound := config.DirectOutbound{
		Type: "direct",
		Tag:  "direct-out",
	}

	outboundJSON, err := json.Marshal(outbound)
	if err != nil {
		return nil, fmt.Errorf("singbox: marshal outbound: %w", err)
	}

	cfg := singBoxConfig{
		Log: &logConfig{
			Level:  "info",
			Output: "/var/log/sing-box/sing-box.log",
		},
		Inbounds:  []json.RawMessage{inboundJSON},
		Outbounds: []json.RawMessage{outboundJSON},
	}

	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("singbox: marshal config: %w", err)
	}

	return &model.Config{
		Content: string(content),
		Format:  "json",
		Version: b.Version(),
	}, nil
}

func (b *Backend) generateUser(params model.ConfigParams) (*model.Config, error) {
	port := params.Port
	if port == 0 {
		port = 8443
	}

	preset := chain.GetDefaultPreset()
	uuid := generateUUID()

	// Respect the global default profile: prefer AWG or TUIC if the profile defines them
	if preset.AWG != nil {
		// For standalone generation, client public key can be passed via params.Extra["clientPubKey"]
		clientPub := ""
		if v, ok := params.Extra["clientPubKey"].(string); ok {
			clientPub = v
		}
		cfg, _, err := b.generateAWGUser(params, &preset, uuid, port, clientPub)
		if err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if preset.TUIC != nil {
		return b.generateTUICUser(params, &preset, uuid, port)
	}

	// Fallback to VLESS+Reality
	// sing-box 1.12.0+ uses 32-byte X25519 keys for Reality
	privKeyBytes := make([]byte, 32)
	if _, err := rand.Read(privKeyBytes); err != nil {
		return nil, fmt.Errorf("singbox: generate user reality key: %w", err)
	}
	privKeyB64 := base64.RawURLEncoding.EncodeToString(privKeyBytes)

	shortID := make([]byte, 8)
	rand.Read(shortID)
	shortIDHex := hex.EncodeToString(shortID)

	serverName := "www.microsoft.com"
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		serverName = preset.Reality.ServerNames[0]
	}

	inbound := config.VLESSInbound{
		Type:       "vless",
		Tag:        "user-in",
		Listen:     "0.0.0.0",
		ListenPort: port,
		Users: []config.VLESSUser{
			{
				Name: "user",
				UUID: uuid,
				Flow: "xtls-rprx-vision",
			},
		},
		TLS: &config.InboundTLSOptions{
			Enabled:    true,
			ServerName: serverName,
			Reality: &config.InboundRealityOptions{
				Enabled:    true,
				PrivateKey: privKeyB64,
				ShortID:    []string{shortIDHex},
			},
		},
	}

	inboundJSON, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("singbox: marshal inbound: %w", err)
	}

	outbound := config.DirectOutbound{
		Type: "direct",
		Tag:  "direct-out",
	}

	outboundJSON, err := json.Marshal(outbound)
	if err != nil {
		return nil, fmt.Errorf("singbox: marshal outbound: %w", err)
	}

	cfg := singBoxConfig{
		Log: &logConfig{
			Level:  "info",
			Output: "/var/log/sing-box/sing-box.log",
		},
		Inbounds:  []json.RawMessage{inboundJSON},
		Outbounds: []json.RawMessage{outboundJSON},
	}

	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("singbox: marshal config: %w", err)
	}

	return &model.Config{
		Content: string(content),
		Format:  "json",
		Version: b.Version(),
	}, nil
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

// generateTUICUser generates a TUIC user config using the given preset.
func (b *Backend) generateTUICUser(params model.ConfigParams, preset *chain.ConnectionPreset, uuid string, port int) (*model.Config, error) {
	tuic := preset.TUIC
	if tuic == nil {
		tuic = &chain.TUICPreset{CongestionControls: []string{"bbr"}, AuthTimeout: "3s"}
	}

	congestion := "bbr"
	if len(tuic.CongestionControls) > 0 {
		congestion = tuic.CongestionControls[0]
	}
	authTimeout := tuic.AuthTimeout
	if authTimeout == "" {
		authTimeout = "3s"
	}

	serverName := "www.microsoft.com"
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		serverName = preset.Reality.ServerNames[0]
	}

	password := uuid
	if v, ok := params.Extra["tuicPassword"].(string); ok && v != "" {
		password = v
	}

	inbound := config.TUICInbound{
		Type:       "tuic",
		Tag:        "user-in",
		Listen:     "0.0.0.0",
		ListenPort: port,
		Users: []config.TUICUser{
			{
				UUID:     uuid,
				Password: password,
			},
		},
		CongestionControl: congestion,
		AuthTimeout:       authTimeout,
		ZeroRTTHandshake:  true,
		Heartbeat:         "10s",
		TLS: &config.InboundTLSOptions{
			Enabled:    true,
			ServerName: serverName,
		},
	}

	inboundJSON, _ := json.Marshal(inbound)
	outboundJSON, _ := json.Marshal(config.DirectOutbound{Type: "direct", Tag: "direct-out"})

	cfg := singBoxConfig{
		Log:       &logConfig{Level: "info", Output: "/var/log/sing-box/sing-box.log"},
		Inbounds:  []json.RawMessage{inboundJSON},
		Outbounds: []json.RawMessage{outboundJSON},
	}

	content, _ := json.MarshalIndent(cfg, "", "  ")
	return &model.Config{Content: string(content), Format: "json", Version: b.Version()}, nil
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
		Amnezia: chain.BuildAmneziaSection(awg, preset),
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
			paramsAWG := model.ConfigParams{Extra: map[string]any{"serverPrivKey": ib.ServerPrivKey}}
			cfg, _, err := b.generateAWGUser(paramsAWG, &preset, uuid, ib.Port, "")
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
				Obfs:      &config.Hysteria2Obfs{Type: "salamander", Password: "salamander_pass"},
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
