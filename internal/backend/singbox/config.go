package singbox

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"golang.org/x/crypto/curve25519"
)

// singBoxConfig is the top-level sing-box configuration structure.
type singBoxConfig struct {
	Log       *logConfig        `json:"log,omitempty"`
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

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("singbox: generate reality key: %w", err)
	}

	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("singbox: marshal private key: %w", err)
	}
	privKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	})

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
		inboundJSON = chain.BuildXHTTPTransportInboundForStandalone(port, uuid, string(privKeyPEM), shortIDHex, serverName, &preset)
	} else {
		// Classic Reality+TCP fallback
		inb := map[string]any{
			"type": "vless",
			"tag":  "transport-in",
			"listen": map[string]any{
				"address": "0.0.0.0",
				"port":    port,
			},
			"users": []map[string]any{{
				"name": "transport",
				"uuid": uuid,
				"flow": "xtls-rprx-vision",
			}},
			"tls": map[string]any{
				"enabled":     true,
				"server_name": map[string]any{"default": serverName},
				"reality": map[string]any{
					"enabled":     true,
					"private_key": string(privKeyPEM),
					"short_id":    []string{shortIDHex},
				},
			},
			"multiplex": map[string]any{"enabled": true},
			"transport": map[string]any{"type": "tcp"},
		}
		inboundJSON, _ = json.Marshal(inb)
	}

	outbound := map[string]any{
		"type": "direct",
		"tag":  "direct-out",
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
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("singbox: generate user reality key: %w", err)
	}
	privKeyBytes, _ := x509.MarshalPKCS8PrivateKey(privateKey)
	privKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privKeyBytes})

	shortID := make([]byte, 8)
	rand.Read(shortID)
	shortIDHex := hex.EncodeToString(shortID)

	serverName := "www.microsoft.com"
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		serverName = preset.Reality.ServerNames[0]
	}

	inbound := map[string]any{
		"type": "vless",
		"tag":  "user-in",
		"listen": map[string]any{
			"address": "0.0.0.0",
			"port":    port,
		},
		"users": []map[string]any{
			{
				"name": "user",
				"uuid": uuid,
				"flow": "xtls-rprx-vision",
			},
		},
		"tls": map[string]any{
			"enabled":     true,
			"server_name": serverName,
			"reality": map[string]any{
				"enabled":     true,
				"private_key": string(privKeyPEM),
				"short_id":    []string{shortIDHex},
			},
		},
	}

	inboundJSON, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("singbox: marshal inbound: %w", err)
	}

	outbound := map[string]any{
		"type": "direct",
		"tag":  "direct-out",
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

	inbound := map[string]any{
		"type": "tuic",
		"tag":  "user-in",
		"listen": map[string]any{
			"address": "0.0.0.0",
			"port":    port,
		},
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
		"tls": map[string]any{
			"enabled":     true,
			"server_name": serverName,
		},
	}

	inboundJSON, _ := json.Marshal(inbound)
	outboundJSON, _ := json.Marshal(map[string]any{"type": "direct", "tag": "direct-out"})

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
		awg = &chain.AWGPreset{}
	}

	// Always generate a fresh server keypair for this AWG entry point.
	privB64, pubB64, err := generateWireGuardKeypair()
	if err != nil {
		return nil, "", fmt.Errorf("generate awg keypair: %w", err)
	}

	peerPub := clientPubKey
	if peerPub == "" {
		peerPub = "CLIENT_PUBLIC_KEY_HERE"
	}

	jc, jmin, jmax, s1, s2, s3, s4, h1, h2, h3, h4 := awg.Concrete()
	chain.EnforceAWGInvariants(&jc, &jmin, &jmax, &s1, &s2, &s3, &s4, &h1, &h2, &h3, &h4)

	amn := map[string]any{
		"jc":   jc,
		"jmin": jmin,
		"jmax": jmax,
		"s1":   s1,
		"s2":   s2,
		"s3":   s3,
		"s4":   s4,
		"h1":   h1,
		"h2":   h2,
		"h3":   h3,
		"h4":   h4,
	}
	// SECURITY-FIRST: when preset requests cps_level==3 we always emit the full CPS chain
	// (no silent downgrade to "packet":"none"). Standalone generates fresh values each time.
	// For truly stable entry (recommended for production) use chain create + apply (persists keys + I*).
	if awg.CPSLevel > 0 {
		mim := awg.Mimicry
		if mim == "" {
			mim = "quic"
		}
		ii1, ii2, ii3, ii4, ii5, _ := chain.GenerateCPS(awg.CPSLevel, mim)
		if ii1 != "" {
			amn["i1"] = ii1
		}
		if ii2 != "" {
			amn["i2"] = ii2
		}
		if ii3 != "" {
			amn["i3"] = ii3
		}
		if ii4 != "" {
			amn["i4"] = ii4
		}
		if ii5 != "" {
			amn["i5"] = ii5
		}
	} else {
		amn["packet"] = "none"
	}

	inbound := map[string]any{
		"type": "wireguard",
		"tag":  "user-in",
		"listen": map[string]any{
			"address": "0.0.0.0",
			"port":    port,
		},
		"private_key": privB64,
		"peers": []map[string]any{
			{
				"public_key":  peerPub,
				"allowed_ips": []string{"0.0.0.0/0", "::/0"},
			},
		},
		"mtu":     1420,
		"amnezia": amn,
	}

	inboundJSON, _ := json.Marshal(inbound)
	outboundJSON, _ := json.Marshal(map[string]any{"type": "direct", "tag": "direct-out"})

	cfg := singBoxConfig{
		Log:       &logConfig{Level: "info", Output: "/var/log/sing-box/sing-box.log"},
		Inbounds:  []json.RawMessage{inboundJSON},
		Outbounds: []json.RawMessage{outboundJSON},
	}

	content, _ := json.MarshalIndent(cfg, "", "  ")
	return &model.Config{Content: string(content), Format: "json", Version: b.Version()}, pubB64, nil
}
