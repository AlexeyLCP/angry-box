package chain

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
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
	PrivateKey string // PEM-encoded PKCS8 private key
	ShortID    string // hex string
	ServerName string
	Port       int
}

// publicKeyDER returns the DER-encoded raw public key for Reality.
func (h *hopParams) publicKeyDER() ([]byte, error) {
	block, _ := pem.Decode([]byte(h.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("chain: failed to decode PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("chain: parse private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("chain: private key is not RSA")
	}
	return x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
}

// publicKeyHex returns the hex-encoded SHA256 of the DER public key (sing-box format).
func (h *hopParams) publicKeyHex() (string, error) {
	der, err := h.publicKeyDER()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(der)
	return hex.EncodeToString(hash[:]), nil
}

// ApplyChain generates configs for every node in the chain and pushes them via SSH.
// Configs are generated from the last node (exit) to the first (entry), so that
// each hop's outbound correctly references the next hop's Reality parameters.
func (a *Applier) ApplyChain(ctx context.Context, chain *model.Chain) error {
	if len(chain.Nodes) < 1 {
		return fmt.Errorf("chain: chain %q has no nodes", chain.Name)
	}

	b, err := a.factory.Create(model.SingBox)
	if err != nil {
		return fmt.Errorf("chain: create backend: %w", err)
	}

	n := len(chain.Nodes)

	// Generate hop params from last to first.
	params := make([]*hopParams, n)
	for i := n - 1; i >= 0; i-- {
		node := &chain.Nodes[i]
		if node.Port == 0 {
			node.Port = defaultTransportPort
		}
		p, err := generateHopParams(node.Port)
		if err != nil {
			return fmt.Errorf("chain: node %q: %w", node.ID, err)
		}
		params[i] = p
	}

	// Build and push configs from first to last.
	for i := 0; i < n; i++ {
		node := &chain.Nodes[i]
		cfg, err := buildNodeConfig(node, i, n, params, chain.Nodes)
		if err != nil {
			return fmt.Errorf("chain: node %q: build config: %w", node.ID, err)
		}

		client, err := sshclient.Connect(node.Addr, node.User, node.KeyPath)
		if err != nil {
			return fmt.Errorf("chain: node %q: %w", node.ID, err)
		}

		pushed, err := pushConfig(client, cfg)
		client.Close()
		if err != nil {
			return fmt.Errorf("chain: node %q: %w", node.ID, err)
		}

		_ = pushed
	}

	// Reload all nodes after configs are in place.
	for i := 0; i < n; i++ {
		node := &chain.Nodes[i]
		host := node.Host()
		if err := b.Reload(ctx, host); err != nil {
			return fmt.Errorf("chain: node %q: reload: %w", node.ID, err)
		}
	}

	return nil
}

func generateHopParams(port int) (*hopParams, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}

	privKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	})

	shortID := make([]byte, 8)
	if _, err := rand.Read(shortID); err != nil {
		return nil, fmt.Errorf("generate shortId: %w", err)
	}

	serverNames := []string{"swupdateapp.unraid.net", "discord.com", "www.microsoft.com"}
	sn, _ := rand.Int(rand.Reader, big.NewInt(int64(len(serverNames))))

	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	return &hopParams{
		UUID:       fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]),
		PrivateKey: string(privKeyPEM),
		ShortID:    hex.EncodeToString(shortID),
		ServerName: serverNames[sn.Int64()],
		Port:       port,
	}, nil
}

// buildNodeConfig constructs the full sing-box config for a node at position i of n.
func buildNodeConfig(node *model.ChainNode, i, n int, params []*hopParams, nodes []model.ChainNode) (string, error) {
	var inbounds []json.RawMessage
	var outbounds []json.RawMessage
	tags := []string{}

	// Every node except the first (entry) gets a transport inbound for the previous hop.
	if i > 0 {
		tag := "transport-in"
		tags = append(tags, tag)
		inb := buildTransportInbound(params[i], tag)
		inbounds = append(inbounds, inb)
	}

	// The first node gets a user-facing inbound.
	if i == 0 {
		port := node.Port
		if port == 0 {
			port = defaultUserPort
		}
		tag := "user-in"
		tags = append(tags, tag)
		inb := buildUserInbound(port, params[i].UUID, tag)
		inbounds = append(inbounds, inb)
	}

	// Every node except the last (exit) gets an outbound to the next hop.
	if i < n-1 {
		next := params[i+1]
		nextAddr := extractHost(nodes[i+1].Addr)
		tag := fmt.Sprintf("out-to-%s", next.ServerName)
		outb, err := buildTransportOutbound(next, nextAddr, tag)
		if err != nil {
			return "", fmt.Errorf("build outbound to next hop: %w", err)
		}
		outbounds = append(outbounds, outb)
	}

	// The last node gets a direct outbound (exit to internet).
	if i == n-1 {
		tag := "direct-out"
		outb := buildDirectOutbound(tag)
		outbounds = append(outbounds, outb)
	}

	// Build routing rule: route all inbound traffic to the first outbound.
	var route *routeConfig
	if len(tags) > 0 && len(outbounds) > 0 {
		// Parse the first outbound to get its tag.
		var firstOut map[string]any
		json.Unmarshal(outbounds[0], &firstOut)
		outTag, _ := firstOut["tag"].(string)

		route = &routeConfig{
			Rules: []routeRule{
				{
					Inbound:  tags,
					Outbound: outTag,
				},
			},
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

	if route != nil {
		cfg["route"] = route
	}

	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	return string(content), nil
}

type routeConfig struct {
	Rules []routeRule `json:"rules"`
}

type routeRule struct {
	Inbound  []string `json:"inbound"`
	Outbound string   `json:"outbound"`
}

func buildTransportInbound(p *hopParams, tag string) json.RawMessage {
	inb := map[string]any{
		"type": "vless",
		"tag":  tag,
		"listen": map[string]any{
			"address": "0.0.0.0",
			"port":    p.Port,
		},
		"users": []map[string]any{
			{
				"name": tag,
				"uuid": p.UUID,
				"flow": "xtls-rprx-vision",
			},
		},
		"tls": map[string]any{
			"enabled": true,
			"server_name": map[string]any{
				"default": p.ServerName,
			},
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
		"listen": map[string]any{
			"address": "0.0.0.0",
			"port":    port,
		},
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
	pubKeyHex, err := next.publicKeyHex()
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
				"enabled":   true,
				"public_key": pubKeyHex,
				"short_id":  next.ShortID,
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

// pushConfig writes the config to the remote host, validates, and restarts sing-box.
func pushConfig(client *sshclient.Client, cfgContent string) (string, error) {
	var js json.RawMessage
	if err := json.Unmarshal([]byte(cfgContent), &js); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	writeCmd := fmt.Sprintf("mkdir -p /etc/sing-box && cat > /etc/sing-box/config.json << 'CONFIG_EOF'\n%s\nCONFIG_EOF", cfgContent)
	if _, err := client.Run(writeCmd); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}

	if _, err := client.Run("sing-box check -c /etc/sing-box/config.json"); err != nil {
		return "", fmt.Errorf("config validation failed: %w", err)
	}

	out, err := client.Run("systemctl restart sing-box")
	if err != nil {
		return "", fmt.Errorf("restart: %w", err)
	}

	return out, nil
}
