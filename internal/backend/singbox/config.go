package singbox

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"

	"github.com/alexeylcp/angry-box/internal/domain/model"
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

	serverNames := []string{"swupdateapp.unraid.net", "discord.com", "www.microsoft.com"}
	sn, _ := rand.Int(rand.Reader, big.NewInt(int64(len(serverNames))))
	serverName := serverNames[sn.Int64()]

	destOverride := params.Extra["destOverride"]
	if destOverride == nil {
		destOverride = serverName
	}

	uuid := generateUUID()

	inbound := map[string]any{
		"type": "vless",
		"tag":  "transport-in",
		"listen": map[string]any{
			"address": "0.0.0.0",
			"port":    port,
		},
		"users": []map[string]any{
			{
				"name": "transport",
				"uuid": uuid,
				"flow": "xtls-rprx-vision",
			},
		},
		"tls": map[string]any{
			"enabled": true,
			"server_name": map[string]any{
				"default": serverName,
			},
			"reality": map[string]any{
				"enabled":   true,
				"private_key": string(privKeyPEM),
				"short_id":    []string{shortIDHex},
			},
		},
		"multiplex": map[string]any{
			"enabled": true,
		},
		"transport": map[string]any{
			"type": "tcp",
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

func (b *Backend) generateUser(params model.ConfigParams) (*model.Config, error) {
	port := params.Port
	if port == 0 {
		port = 8443
	}

	uuid := generateUUID()

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
			"enabled": false,
		},
		"transport": map[string]any{
			"type": "ws",
			"ws_settings": map[string]any{
				"path": "/ws",
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
