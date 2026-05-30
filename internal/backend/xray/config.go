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
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// This file contains Xray-specific configuration generators.
// The style is intentionally kept similar to the sing-box backend for maintainability,
// even though Xray uses a different overall config schema.

type xrayConfig struct {
	Log       *xrayLog          `json:"log,omitempty"`
	Inbounds  []json.RawMessage `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
}

type xrayLog struct {
	LogLevel string `json:"loglevel"`
}

type xrayInbound struct {
	Port           int                 `json:"port"`
	Protocol       string              `json:"protocol"`
	Tag            string              `json:"tag"`
	Settings       xrayInboundSettings `json:"settings"`
	StreamSettings *xrayStreamSettings `json:"streamSettings,omitempty"`
}

type xrayInboundSettings struct {
	Clients    []xrayClient `json:"clients"`
	Decryption string       `json:"decryption,omitempty"`
}

type xrayClient struct {
	ID   string `json:"id"`
	Flow string `json:"flow,omitempty"`
}

type xrayOutbound struct {
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
}

type xrayStreamSettings struct {
	Network         string               `json:"network"`
	Security        string               `json:"security,omitempty"`
	RealitySettings *xrayRealitySettings `json:"realitySettings,omitempty"`
	HTTPSettings    *xrayHTTPSettings    `json:"httpSettings,omitempty"`
	WSSettings      *xrayWSSettings      `json:"wsSettings,omitempty"`
}

type xrayRealitySettings struct {
	Dest        string   `json:"dest"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIds    []string `json:"shortIds"`
}

type xrayWSSettings struct {
	Path string `json:"path"`
}

type xrayHTTPSettings struct {
	Path    string               `json:"path,omitempty"`
	Method  string               `json:"method,omitempty"`
	Headers map[string][]string  `json:"headers,omitempty"`
	Extra   *chain.XrayXHTTPExtra `json:"extra,omitempty"`
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

	preset := chain.GetDefaultPreset()

	// Use advanced XHTTP when the preset has rich XHTTP settings or explicitly requested
	useXHTTP := preset.XHTTP != nil && len(preset.XHTTP.Paths) > 0
	if v, ok := params.Extra["transport"].(string); ok && strings.ToLower(v) == "xhttp" {
		useXHTTP = true
	}

	var streamSettings *xrayStreamSettings
	if useXHTTP {
		// Advanced XHTTP + Reality using our new generators (padding, XMUX, realistic headers, etc.)
		path := "/api/v1/" + shortIDHex[:4]
		host := ""
		if preset.XHTTP != nil && len(preset.XHTTP.Paths) > 0 {
			path = preset.XHTTP.Paths[0]
		}
		if preset.XHTTP != nil && len(preset.XHTTP.Hosts) > 0 {
			host = preset.XHTTP.Hosts[0]
		}
		headers := chain.GenerateRealisticHeaders(host)
		if preset.XHTTP != nil && len(preset.XHTTP.Headers) > 0 {
			headers = preset.XHTTP.Headers
		}

		httpSettings := &xrayHTTPSettings{
			Path:    path,
			Method:  "POST",
			Headers: headers,
			// Usually Xray expects extra in streamSettings, but for compatibility some patched versions might read it here.
			// Let's use the XHTTPExtra we built.
		}

		streamSettings = &xrayStreamSettings{
			Network:  "http",
			Security: "reality",
			RealitySettings: &xrayRealitySettings{
				Dest:        dest,
				ServerNames: []string{serverName},
				PrivateKey:  string(privKeyPEM),
				ShortIds:    []string{shortIDHex},
			},
			HTTPSettings: httpSettings,
		}
	} else {
		// Classic Reality + TCP
		streamSettings = &xrayStreamSettings{
			Network:  "tcp",
			Security: "reality",
			RealitySettings: &xrayRealitySettings{
				Dest:        dest,
				ServerNames: []string{serverName},
				PrivateKey:  string(privKeyPEM),
				ShortIds:    []string{shortIDHex},
			},
		}
	}

	inbound := xrayInbound{
		Port:     port,
		Protocol: "vless",
		Tag:      "transport-in",
		Settings: xrayInboundSettings{
			Clients: []xrayClient{
				{
					ID:   uuid,
					Flow: "xtls-rprx-vision",
				},
			},
			Decryption: "none",
		},
		StreamSettings: streamSettings,
	}

	inboundJSON, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("xray: marshal inbound: %w", err)
	}

	outbound := xrayOutbound{
		Protocol: "freedom",
		Tag:      "direct-out",
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

	inbound := xrayInbound{
		Port:     port,
		Protocol: "vless",
		Tag:      "user-in",
		Settings: xrayInboundSettings{
			Clients: []xrayClient{
				{
					ID: uuid,
				},
			},
			Decryption: "none",
		},
		StreamSettings: &xrayStreamSettings{
			Network:  "ws",
			Security: "none",
			WSSettings: &xrayWSSettings{
				Path: "/ws",
			},
		},
	}

	inboundJSON, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("xray: marshal inbound: %w", err)
	}

	outbound := xrayOutbound{
		Protocol: "freedom",
		Tag:      "direct-out",
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
