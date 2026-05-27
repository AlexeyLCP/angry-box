package xray

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

type xrayConfig struct {
	Log       *xrayLog         `json:"log,omitempty"`
	Inbounds  []json.RawMessage `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
}

type xrayLog struct {
	LogLevel string `json:"loglevel"`
}

// GenerateConfig produces an xray configuration for the given type and parameters.
func (b *Backend) GenerateConfig(cfgType model.ConfigType, params model.ConfigParams) (*model.Config, error) {
	switch cfgType {
	case model.ConfigTransport:
		return b.generateTransport(params)
	case model.ConfigUser:
		return b.generateUser(params)
	default:
		return nil, fmt.Errorf("xray: unknown config type %s", cfgType)
	}
}

func (b *Backend) generateTransport(params model.ConfigParams) (*model.Config, error) {
	port := params.Port
	if port == 0 {
		port = 443
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("xray: generate reality key: %w", err)
	}

	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("xray: marshal private key: %w", err)
	}
	privKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	})

	shortID := make([]byte, 8)
	if _, err := rand.Read(shortID); err != nil {
		return nil, fmt.Errorf("xray: generate shortId: %w", err)
	}
	shortIDHex := hex.EncodeToString(shortID)

	serverNames := []string{"swupdateapp.unraid.net", "discord.com", "www.microsoft.com"}
	sn, _ := rand.Int(rand.Reader, big.NewInt(int64(len(serverNames))))
	serverName := serverNames[sn.Int64()]

	dest := serverName + ":443"
	if d, ok := params.Extra["destOverride"]; ok {
		if ds, ok := d.(string); ok {
			dest = ds + ":443"
		}
	}

	uuid := generateUUID()

	inbound := map[string]any{
		"port":     port,
		"protocol": "vless",
		"tag":      "transport-in",
		"settings": map[string]any{
			"clients": []map[string]any{
				{
					"id":   uuid,
					"flow": "xtls-rprx-vision",
				},
			},
			"decryption": "none",
		},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"dest":        dest,
				"serverNames": []string{serverName},
				"privateKey":  string(privKeyPEM),
				"shortIds":    []string{shortIDHex},
			},
		},
	}

	inboundJSON, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("xray: marshal inbound: %w", err)
	}

	outbound := map[string]any{
		"protocol": "freedom",
		"tag":      "direct-out",
	}

	outboundJSON, err := json.Marshal(outbound)
	if err != nil {
		return nil, fmt.Errorf("xray: marshal outbound: %w", err)
	}

	cfg := xrayConfig{
		Log:       &xrayLog{LogLevel: "info"},
		Inbounds:  []json.RawMessage{inboundJSON},
		Outbounds: []json.RawMessage{outboundJSON},
	}

	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("xray: marshal config: %w", err)
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
		"port":     port,
		"protocol": "vless",
		"tag":      "user-in",
		"settings": map[string]any{
			"clients": []map[string]any{
				{
					"id": uuid,
				},
			},
			"decryption": "none",
		},
		"streamSettings": map[string]any{
			"network":     "ws",
			"security":    "none",
			"wsSettings": map[string]any{
				"path": "/ws",
			},
		},
	}

	inboundJSON, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("xray: marshal inbound: %w", err)
	}

	outbound := map[string]any{
		"protocol": "freedom",
		"tag":      "direct-out",
	}

	outboundJSON, err := json.Marshal(outbound)
	if err != nil {
		return nil, fmt.Errorf("xray: marshal outbound: %w", err)
	}

	cfg := xrayConfig{
		Log:       &xrayLog{LogLevel: "info"},
		Inbounds:  []json.RawMessage{inboundJSON},
		Outbounds: []json.RawMessage{outboundJSON},
	}

	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("xray: marshal config: %w", err)
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

