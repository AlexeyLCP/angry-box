package chain

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"log/slog"
	"math/big"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
	"golang.org/x/crypto/curve25519"
)

const (
	defaultUserPort      = 8443
	defaultTransportPort = 443
)

// Applier generates and pushes proxy configs to all nodes in a chain.
type Applier struct {
	factory   ports.Factory
	connector ports.SSHConnector
}

// NewApplier creates a chain applier. If connector is nil, the production SSH
// connector (ssh.DefaultConnector) is used; tests inject a fake to avoid real
// network connections.
func NewApplier(factory ports.Factory, connector ports.SSHConnector) *Applier {
	if connector == nil {
		connector = sshclient.DefaultConnector
	}
	return &Applier{factory: factory, connector: connector}
}

// ResolveHostKey returns a copy of host with KeyPath resolved against the
// panel default SSH key when KeyPath is empty. It NEVER mutates the caller's
// Host — the stored Host.KeyPath is not rewritten by this helper (the
// fallback is a deploy-time resolution, not a persisted rewrite). "password:"
// (empty-password marker) is treated as a real auth intent and does NOT
// trigger the default fallback; only a truly empty KeyPath does.
//
// Exported so the takeover package (which holds a *chain.Store) can share the
// same default-key fallback chokepoint as the applier.
func ResolveHostKey(st *Store, host *model.Host) *model.Host {
	h := *host
	if h.KeyPath == "" {
		if settings, err := st.GetSettings(); err == nil && settings.DefaultSSHKeyID != "" {
			h.KeyPath = settings.DefaultSSHKeyID
		}
	}
	return &h
}

// resolveHostKey is the package-internal alias used by applier call sites.
func resolveHostKey(st *Store, host *model.Host) *model.Host { return ResolveHostKey(st, host) }

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

// clampRealityPrivateKeyB64 clamps an X25519 private key per RFC 7748 and
// returns it as base64 RawURL. Reality inbound and outbound must share the same
// clamped scalar — deriving the public key from an unclamped stored private key
// breaks the handshake ("REALITY: processed invalid connection").
func clampRealityPrivateKeyB64(privB64 string) (string, error) {
	privBytes, err := base64.RawURLEncoding.DecodeString(privB64)
	if err != nil {
		return "", fmt.Errorf("chain: decode private key: %w", err)
	}
	if len(privBytes) != 32 {
		return "", fmt.Errorf("chain: private key is not 32 bytes")
	}
	privBytes[0] &= 248
	privBytes[31] &= 127
	privBytes[31] |= 64
	return base64.RawURLEncoding.EncodeToString(privBytes), nil
}

// publicKeyB64 returns the base64-encoded X25519 public key for Reality outbound.
// In sing-box 1.12.0+, the reality public_key field is a base64-encoded 32-byte public key.
func (h *hopParams) publicKeyB64() (string, error) {
	clamped, err := clampRealityPrivateKeyB64(h.PrivateKey)
	if err != nil {
		return "", err
	}
	privBytes, err := base64.RawURLEncoding.DecodeString(clamped)
	if err != nil {
		return "", fmt.Errorf("chain: decode private key: %w", err)
	}

	var priv, pub [32]byte
	copy(priv[:], privBytes)
	curve25519.ScalarBaseMult(&pub, &priv)
	return base64.RawURLEncoding.EncodeToString(pub[:]), nil
}

// ApplyChain generates configs for every node in the chain and pushes them via SSH.
// It persists transit keys on chain nodes, saves the chain to the store, then builds
// a MERGED config for each node (chain + standalone inbounds + other chains) to avoid
// overwriting existing node configuration.
func (a *Applier) ApplyChain(ctx context.Context, store *Store, chain *model.Chain, awgClientPubKey string) (*ApplyReport, error) {
	if len(chain.Nodes) < 1 {
		return nil, fmt.Errorf("chain: chain %q has no nodes", chain.Name)
	}

	_ = a.factory.Create()

	if chain.Transport == "" {
		chain.Transport = model.TransportXHTTP
	}
	if chain.UserProtocol == "" {
		chain.UserProtocol = model.UserProtocolAWG
	}

	// Pre-flight SSH check: verify connectivity to all nodes before touching any config.
	for _, node := range chain.Nodes {
		resolved := resolveHostKey(store, &model.Host{ID: node.ID, Addr: node.Addr, User: node.User, KeyPath: node.KeyPath})
		if resolved.KeyPath == "" {
			log.Printf("ssh: no key configured for node %s and no default key set", node.ID)
		}
		client, err := a.connector.Connect(resolved.Addr, resolved.User, resolved.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("pre-flight check failed: cannot connect to node %q (%s): %w", node.ID, node.Addr, err)
		}
		client.Close()
	}

	n := len(chain.Nodes)

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

	// Persist the AWG CPS obfuscation material (I1-I5) once on the chain so the
	// server endpoint and every client .conf render identical I1-I5. Without
	// this the CPS handshake breaks (each render generates fresh random I1-I5).
	// Idempotent: existing material is preserved across redeploys.
	EnsureChainAWGMaterial(chain, preset)

	profileName := GetDefaultPresetName()
	if chain.ObfuscationProfile != "" {
		if _, ok := GetPreset(chain.ObfuscationProfile); ok {
			profileName = chain.ObfuscationProfile
		}
	}

	// AWG client pubkey handling
	var awgMaterial *AWGClientMaterial
	effectiveClientPub := awgClientPubKey
	if chain.UserProtocol == model.UserProtocolAWG {
		if effectiveClientPub == "" {
			cPriv, cPub, genErr := generateWireGuardKeypair()
			if genErr == nil {
				effectiveClientPub = cPub
				awgMaterial = &AWGClientMaterial{
					ClientPubUsed: cPub,
					ClientPriv:    cPriv,
					Note:          "Sample client keypair auto-generated by apply-chain for convenience.",
				}
			}
		} else {
			awgMaterial = &AWGClientMaterial{
				ClientPubUsed: effectiveClientPub,
				Note:          "Used client public key supplied via --client-pubkey.",
			}
		}
		if chain.AWGEntryServerPriv == "" {
			if priv, pub, kerr := generateWireGuardKeypair(); kerr == nil {
				chain.AWGEntryServerPriv = priv
				chain.AWGEntryServerPub = pub
			}
		}
	}

	// Persist TUIC user-entry creds once so server config and RenderClientConfig agree.
	if chain.UserProtocol == model.UserProtocolTUIC {
		if chain.TUICEntryUserUUID == "" || chain.TUICEntryUserPassword == "" {
			uuid, password := GenerateStableTUICUserCreds()
			if chain.TUICEntryUserUUID == "" {
				chain.TUICEntryUserUUID = uuid
			}
			if chain.TUICEntryUserPassword == "" {
				chain.TUICEntryUserPassword = password
			}
		}
	}

	// Generate hop params and PERSIST them on chain nodes so buildMergedNodeConfig sees them.
	for i := n - 1; i >= 0; i-- {
		node := &chain.Nodes[i]
		if node.Port == 0 {
			node.Port = defaultTransportPort
		}
		// Reuse existing transit keys if present, otherwise generate new ones.
		if node.TransitPrivKey == "" || node.TransitShortID == "" || node.TransitUUID == "" {
			p, err := generateHopParams(node.Port, &preset)
			if err != nil {
				return nil, fmt.Errorf("chain: node %q: generate params: %w", node.ID, err)
			}
			if node.TransitPrivKey == "" {
				node.TransitPrivKey = p.PrivateKey
			}
			if node.TransitShortID == "" {
				node.TransitShortID = p.ShortID
			}
			if node.TransitUUID == "" {
				node.TransitUUID = p.UUID
			}
		}

		// AWG inter-node transport: per-link WireGuard keypairs. A transit node
		// (i > 0) listens with a server keypair; a node with an outbound (i < n-1)
		// dials the next hop with a client keypair + a unique inner tunnel IP
		// from 10.9.0.0/24. Reuse existing keys if present (Rule 5: stable across
		// redeploys). Only when chain.Transport == AWG.
		if chain.Transport == model.TransportAWG {
			if i > 0 && node.TransitAWGServerPriv == "" {
				priv, pub, err := GenerateWireGuardKeypair()
				if err != nil {
					return nil, fmt.Errorf("chain: node %q: generate awg server keypair: %w", node.ID, err)
				}
				node.TransitAWGServerPriv = priv
				node.TransitAWGServerPub = pub
			}
			if i < n-1 && node.TransitAWGClientPriv == "" {
				priv, pub, err := GenerateWireGuardKeypair()
				if err != nil {
					return nil, fmt.Errorf("chain: node %q: generate awg client keypair: %w", node.ID, err)
				}
				node.TransitAWGClientPriv = priv
				node.TransitAWGClientPub = pub
			}
			if i < n-1 && node.TransitAWGAddress == "" {
				node.TransitAWGAddress = allocateAWGTransitIP(transitAddresses(chain))
			}
			// Fixed source port for the AWG transport client endpoint. Without
			// it sing-box binds a random ephemeral port, which breaks on NAT'd
			// VPSes (GCloud): handshake responses map to a port that's gone after
			// a re-handshake. 51820 + nodeIndex + 1 keeps it out of the user-entry
			// range (user-entry listens on UserEntryPort, default 51820) and is
			// deterministic per node.
			if i < n-1 && node.TransitAWGClientPort == 0 {
				node.TransitAWGClientPort = 51820 + i + 1
			}
		}

		if err := ensureAWGExitLinks(chain, node); err != nil {
			return nil, fmt.Errorf("chain: node %q: ensure awg exit links: %w", node.ID, err)
		}
	}

	// Save chain to store so GetChainsForNode sees it.
	if err := store.SaveChain(chain); err != nil {
		return nil, fmt.Errorf("save chain: %w", err)
	}

	// Build + push merged config for each node.
	results := make([]NodeResult, 0, n)

	for i := 0; i < n; i++ {
		node := &chain.Nodes[i]

		nodeInfo, err := store.GetNodeInfo(node.ID)
		if err != nil {
			// Create a minimal NodeInfo from the host data we have.
			nodeInfo = &model.NodeInfo{
				Host: model.Host{ID: node.ID, Addr: node.Addr, User: node.User, KeyPath: node.KeyPath},
			}
		}

		// Get all chains for this node, replace/insert the current one.
		nodeChains, _ := store.GetChainsForNode(node.ID)
		replaced := false
		for j, c := range nodeChains {
			if c.Name == chain.Name {
				nodeChains[j] = chain
				replaced = true
				break
			}
		}
		if !replaced {
			nodeChains = append(nodeChains, chain)
		}

		// Fetch this node's MTProxy users (the node-level MTProxy inbound is
		// built from them in buildMergedNodeConfig). Empty for non-MTProxy nodes.
		mtproxyUsers := store.ListMTProxyUsersForNode(node.ID)
		cfg, _, buildErr := buildMergedNodeConfig(nodeInfo, nodeChains, usersByChainMap(store, nodeChains), usersByInboundMap(store, nodeInfo.Inbounds), mtproxyUsers)
		if buildErr != nil {
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "build config: " + buildErr.Error()})
			continue
		}

		cfgJSON, marshalErr := json.MarshalIndent(cfg, "", "  ")
		if marshalErr != nil {
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "marshal config: " + marshalErr.Error()})
			continue
		}

		// Deploy this node. Serialization of the critical backup→write→restart
		// section is handled INSIDE pushConfig via withHostLock(node.ID) — the
		// single chokepoint (CTO-review C2). Pre-flight Connect/Deploy/InstallAWG
		// run without the lock: they are idempotent and do not touch the rollback
		// chain, so holding the lock across them would only block other nodes.
		resolved := resolveHostKey(store, &model.Host{ID: node.ID, Addr: node.Addr, User: node.User, KeyPath: node.KeyPath})
		if resolved.KeyPath == "" {
			log.Printf("ssh: no key configured for node %s and no default key set", node.ID)
		}
		client, connErr := a.connector.Connect(resolved.Addr, resolved.User, resolved.KeyPath)
		if connErr != nil {
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "ssh connect: " + connErr.Error()})
			continue
		}

		backend := a.factory.Create()

		// Install AWG kernel module when AWG is the user-entry protocol OR the
		// inter-node transport. The transport case covers transit nodes that
		// carry an AWG link (chain.Transport == AWG) even when the user entry is
		// TUIC/VLESS — without this the transit AWG endpoint has no module.
		if chain.UserProtocol == model.UserProtocolAWG || chain.Transport == model.TransportAWG {
			if awgErr := backend.InstallAWGModuleWithOptions(ctx, node.Host(), model.DeployOptions{UseSudo: nodeInfo.UseSudo}); awgErr != nil {
				client.Close()
				results = append(results, NodeResult{ID: node.ID, Success: false, Error: "install awg module: " + awgErr.Error()})
				continue
			}
			// Enable IPv4 forwarding for EVERY AWG-chain node — not just nodes that
			// get an awg0.conf (user-entry/standalone/exit-server/balancer, handled
			// in pushAWGConfs), but ALSO AWG transit nodes (userspace transport
			// endpoint, no awg0.conf → falls through to plain pushConfig). A transit
			// node forwards packets between the transport-in endpoint and the
			// egress outbound; without ip_forward=1 the kernel drops them and
			// egress through the chain silently fails. Same condition as the module
			// install: UserProtocol==AWG || Transport==AWG.
			if fwdErr := ensureIPForward(client, nodeInfo.UseSudo); fwdErr != nil {
				client.Close()
				results = append(results, NodeResult{ID: node.ID, Success: false, Error: "enable ip_forward: " + fwdErr.Error()})
				continue
			}
		}

		// Deploy sing-box with the node's UseSudo flag — Deploy() alone assumes
		// root and cannot reinstall a root-owned binary on a non-root sudoer
		// node (CTO-review follow-up to H5: the chain apply path also needs the
		// options-aware deploy, not just the CLI).
		if _, deployErr := backend.DeployWithOptions(ctx, node.Host(), model.DeployOptions{UseSudo: nodeInfo.UseSudo}); deployErr != nil {
			client.Close()
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "deploy sing-box: " + deployErr.Error()})
			continue
		}

		// Render the kernel awg-quick .conf files this node needs under the
		// kernel-AWG architecture (user-entry awg0, multi-exit awg-exit-nX, exit
		// server awg0, or standalone awg0). Empty for non-AWG nodes —
		// pushConfigWithAWG then falls through to the plain pushConfig path.
		awgFiles := renderAWGConfsForDeploy(store, nodeInfo, nodeChains)

		_, pushErr := pushConfigWithAWG(client, node.ID, string(cfgJSON), awgFiles, nodeInfo.UseSudo)
		client.Close()
		if pushErr != nil {
			if strings.Contains(pushErr.Error(), "rollback successful") {
				errMsg := "ROLLBACK APPLIED: " + pushErr.Error()
				if i > 0 {
					errMsg = "WARNING: Chain state is out of sync. Rollback occurred on node " + node.Addr + ". " + errMsg
				}
				results = append(results, NodeResult{ID: node.ID, Success: false, Error: errMsg})
			} else {
				results = append(results, NodeResult{ID: node.ID, Success: false, Error: "push config: " + pushErr.Error()})
			}
			continue
		}

		recordDeploySuccess(store, node.ID, string(cfgJSON))
		WriteAudit(store, "deploy", "node", node.ID, AuditPayload{"chain": chain.Name, "transport": string(chain.Transport), "user_protocol": string(chain.UserProtocol)}, "operator")
		results = append(results, NodeResult{ID: node.ID, Success: true})
	}

	if awgMaterial != nil && chain.AWGEntryServerPub != "" {
		awgMaterial.ServerPub = chain.AWGEntryServerPub
	}

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
			if mim == "quic" && level >= 1 {
				awgMaterial.I1Len = 1200
				awgMaterial.I1Type = "quic-initial-chrome"
			}
		}
	}

	report := &ApplyReport{
		ChainName: chain.Name,
		Profile:   profileName,
		Transport: chain.Transport,
		UserProto: chain.UserProtocol,
		Nodes:     results,
		AWG:       awgMaterial,
	}

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
	privKeyB64, err := clampRealityPrivateKeyB64(base64.RawURLEncoding.EncodeToString(privKeyBytes))
	if err != nil {
		return nil, err
	}

	shortID := make([]byte, 8)
	if _, err := rand.Read(shortID); err != nil {
		return nil, fmt.Errorf("generate shortId: %w", err)
	}

	// Prefer Reality preset, fallback to XHTTP host, then random
	serverName := DefaultRealitySNI
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

func buildTransportInbound(p *hopParams, tag string) json.RawMessage {
	inb := config.VLESSInbound{
		Type:       "vless",
		Tag:        tag,
		Listen:     "0.0.0.0",
		ListenPort: p.Port,
		Users: []config.VLESSUser{
			{
				Name: tag,
				UUID: p.UUID,
				Flow: "",
			},
		},
		TLS: &config.InboundTLSOptions{
			Enabled:    true,
			ServerName: p.ServerName,
			Reality: &config.InboundRealityOptions{
				Enabled: true,
				Handshake: &config.RealityHandshake{
					Server:     p.ServerName,
					ServerPort: 443,
				},
				PrivateKey: p.PrivateKey,
				ShortID:    []string{p.ShortID},
			},
		},
	}

	data, _ := json.Marshal(inb)
	return data
}

func buildUserInbound(port int, uuid, tag string) json.RawMessage {
	inb := config.VLESSInbound{
		Type:       "vless",
		Tag:        tag,
		Listen:     "0.0.0.0",
		ListenPort: port,
		Users: []config.VLESSUser{
			{
				Name: tag,
				UUID: uuid,
				Flow: "xtls-rprx-vision",
			},
		},
		TLS: &config.InboundTLSOptions{
			Enabled: false,
		},
		Transport: &config.TransportOptions{
			Type: "ws",
			Path: "/ws",
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

	out := config.VLESSOutbound{
		Type:       "vless",
		Tag:        tag,
		Server:     serverAddr,
		ServerPort: next.Port,
		UUID:       next.UUID,
		Flow:       "",
		TLS: &config.OutboundTLSOptions{
			Enabled:    true,
			ServerName: next.ServerName,
			UTLS: &config.UTLSOptions{
				Enabled:     true,
				Fingerprint: "chrome",
			},
			Reality: &config.OutboundRealityOptions{
				Enabled:   true,
				PublicKey: pubKeyHex,
				ShortID:   next.ShortID,
			},
		},
	}

	data, _ := json.Marshal(out)
	return data, nil
}

// buildAWGTransportInbound builds the transit-side fragment of an inter-node
// AWG link: a userspace WireGuard endpoint (System: false) that listens on
// node.Port with the node's TransitAWGServerPriv and accepts the previous
// node's client as its single peer (peer pub = prev.TransitAWGClientPub,
// allowed_ips = prev.TransitAWGAddress — the previous node's inner IP, so
// route rules can match traffic arriving from it by source_ip_cidr). The tag
// is the chain's transport-in tag so generic route rules steer decrypted
// traffic down the chain. preset supplies the Amnezia obfuscation params.
func buildAWGTransportInbound(node *model.ChainNode, prev *model.ChainNode, tag string, preset *ConnectionPreset, material *AWGObfsMaterial) json.RawMessage {
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}
	port := node.Port
	if port == 0 {
		port = defaultTransportPort
	}
	peer := config.WireGuardPeer{PublicKey: "CLIENT_PUBLIC_KEY_HERE"}
	if prev != nil {
		peer.PublicKey = prev.TransitAWGClientPub
		if peer.PublicKey == "" {
			peer.PublicKey = "CLIENT_PUBLIC_KEY_HERE"
		}
		// AllowedIPs MUST be 0.0.0.0/0 (not just the prev node's transport inner
		// IP) so the exit/transit can route RESPONSE packets back through the
		// tunnel. User traffic arrives with source IPs in 10.8.0.0/24 (the
		// user-entry subnet); the response destination is 10.8.0.x. If the peer's
		// AllowedIPs only lists 10.9.0.2/32 (the transport inner IP), WireGuard
		// drops the response — 10.8.0.x matches no peer. With 0.0.0.0/0 the
		// single peer (the previous node) receives ALL response traffic, which is
		// correct for a linear chain (the transport-in has exactly one peer).
		// This mirrors the transport-out side (which also uses 0.0.0.0/0).
		peer.AllowedIPs = []string{"0.0.0.0/0", "::/0"}
		// Explicit peer endpoint (prev's public IP + AWG client port). WireGuard
		// server peers normally learn the endpoint from incoming packets, but
		// sing-box-extended's userspace endpoint does not populate it reliably —
		// so the server never sends a response (the handshake initiation is
		// accepted but the response has nowhere to go). Setting the endpoint
		// explicitly makes the response reach the previous node. prev.Addr is the
		// SSH addr (IP:22); strip the port to get the bare IP.
		peer.Address = extractHost(prev.Addr)
		peer.Port = prev.TransitAWGClientPort
		if peer.Port == 0 {
			peer.Port = 51821 // deterministic fallback (matches ApplyChain's 51820+i+1 for node index 0)
		}
	}
	ep := config.WireGuardEndpoint{
		Type:       "wireguard",
		Tag:        tag,
		System:     false, // userspace wireguard-go (no kernel module needed to listen)
		MTU:        1420,
		Address:    []string{"10.9.0.1/24"}, // transit server inner address
		PrivateKey: node.TransitAWGServerPriv,
		ListenPort: port,
		Peers:      []config.WireGuardPeer{peer},
		// Amnezia is intentionally DISABLED on the inter-node transport. The
		// transport is a service tunnel between trusted servers — it does NOT
		// need DPI obfuscation (that's the user-entry awg0's job, which runs on
		// the kernel module). Running amnezia on a USERSPACE wireguard-go
		// endpoint is the known unstable path: the handshake (simpler crypto)
		// completes, but the data plane fails — Jc junk-packets and the
		// chacha20poly1305 overlap make userspace amnezia drop data packets
		// (verified on a real VPS: 72 KiB sent, 92 B received — data out, no
		// responses back). Plain WireGuard in userspace is rock-solid. See
		// VPN/docs/sing-box-extended.md + the live-VPS trace in PROGRESS.md §8.
		Amnezia: nil,
	}
	data, _ := json.Marshal(ep)
	return data
}

// buildAWGTransportOutbound builds the previous-node-side fragment of an
// inter-node AWG link. sing-box-extended 1.13 removed the wireguard OUTBOUND
// (deprecated in 1.11, gone in 1.13), so the client side is a WireGuard
// ENDPOINT with a single peer that dials the next node's transit endpoint
// (peer address/port + server public key). This mirrors RenderAWGHop's shape.
// serverAddr is the next node's bare host (from extractHost(next.Addr));
// next.Port is the next node's AWG listen port; thisNode.TransitAWGClientPriv
// is the client private key; next.TransitAWGServerPub is the peer (server)
// public key; thisNode.TransitAWGAddress is this client's inner tunnel IP.
// The tag is the chain's inter-node outbound tag so route rules steer traffic
// into this endpoint. preset supplies the Amnezia params (must match the
// server endpoint's amnezia block or the handshake fails).
func buildAWGTransportOutbound(thisNode, next *model.ChainNode, serverAddr, tag string, preset *ConnectionPreset, material *AWGObfsMaterial) (json.RawMessage, error) {
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}
	serverPort := next.Port
	if serverPort == 0 {
		serverPort = defaultTransportPort
	}
	localAddr := thisNode.TransitAWGAddress
	if localAddr == "" {
		localAddr = "10.9.0.2/32"
	}
	ep := config.WireGuardEndpoint{
		Type:       "wireguard",
		Tag:        tag, // route rules reference this tag as the "outbound"
		System:     false,
		MTU:        1420,
		Address:    []string{localAddr},
		PrivateKey: thisNode.TransitAWGClientPriv,
		ListenPort: thisNode.TransitAWGClientPort, // fixed source port — NAT'd VPSes need a stable mapping or handshake responses never return (the peer replies to a port that's gone after a re-handshake retry)
		Peers: []config.WireGuardPeer{
			{
				PublicKey:                   next.TransitAWGServerPub,
				Address:                     serverAddr,
				Port:                        serverPort,
				PersistentKeepaliveInterval: 25,
				AllowedIPs:                  []string{"0.0.0.0/0", "::/0"},
			},
		},
		// Amnezia DISABLED on inter-node transport — see buildAWGTransportInbound
		// for the full rationale. Plain WireGuard in userspace is stable; userspace
		// amnezia drops data packets (chacha20poly1305 overlap). The transport is a
		// trusted server-to-server tunnel — DPI obfuscation is the user-entry's job.
		Amnezia: nil,
	}
	data, _ := json.Marshal(ep)
	return data, nil
}

func buildDirectOutbound(tag string) json.RawMessage {
	out := config.DirectOutbound{
		Type: "direct",
		Tag:  tag,
	}
	data, _ := json.Marshal(out)
	return data
}

// BuildAWGAmnezia returns amnezia options only when CPS level > 0, otherwise nil.
// Official sing-box 1.13+ does not support the "amnezia" field — only sing-box-extended does.
// material, when non-nil, supplies the persisted I1-I5 so the server endpoint and
// every client .conf render the SAME I1-I5 (without it, each call generates fresh
// random I1-I5 and the CPS handshake breaks server↔client). Chain paths pass the
// chain's persisted material; standalone/legacy paths pass nil (on-the-fly gen).
func BuildAWGAmnezia(awg *AWGPreset, preset *ConnectionPreset, material *AWGObfsMaterial) *config.AmneziaOptions {
	level := 0
	if preset != nil && preset.CPSLevel > 0 {
		level = preset.CPSLevel
	} else if awg != nil && awg.CPSLevel > 0 {
		level = awg.CPSLevel
	}
	if level <= 0 {
		return nil
	}
	return BuildAmneziaSection(awg, preset, material)
}

// safeShortID returns at most the first 4 chars of a short ID, avoiding slice bounds panic.
func safeShortID(s string) string {
	if len(s) > 4 {
		return s[:4]
	}
	return s
}

// extractHost strips the port from an address like "1.2.3.4:22" or returns the string as-is.
// extractHost strips the port from an address like "1.2.3.4:22" or returns the
// string as-is when there is no port. Uses net.SplitHostPort so bracketed IPv6
// literals ([2001:db8::1]:22) and bare IPv6 addresses (2001:db8::1) are handled
// correctly instead of being mis-split at the last ':' (sibling of CTO-review
// H8 — extractHost had the same bug as the splitters).
func extractHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr // no port: whole input is the host (covers bare IPv6)
	}
	return host
}

// transitAddresses returns the TransitAWGAddress values already claimed by
// chain nodes, so allocateAWGTransitIP can pick a collision-free inner IP for a
// new node. Used during AWG inter-node transport key generation in ApplyChain.
func transitAddresses(chain *model.Chain) []string {
	var taken []string
	for i := range chain.Nodes {
		if a := chain.Nodes[i].TransitAWGAddress; a != "" {
			taken = append(taken, a)
		}
	}
	return taken
}

// exitAddresses returns the ExitAWGAddress values already claimed by chain
// balancer nodes, so allocateAWGExitIP can pick a collision-free inner IP for a
// new kernel awg-exit-nX interface.
func exitAddresses(chain *model.Chain) []string {
	var taken []string
	for i := range chain.Nodes {
		for _, link := range chain.Nodes[i].ExitAWGLinks {
			if link.Address != "" {
				taken = append(taken, link.Address)
			}
		}
	}
	return taken
}

func chainNodeByID(chain *model.Chain, id string) *model.ChainNode {
	if chain == nil {
		return nil
	}
	for i := range chain.Nodes {
		if chain.Nodes[i].ID == id {
			return &chain.Nodes[i]
		}
	}
	return nil
}

func exitLinkByTarget(node *model.ChainNode, targetID string) *model.AWGExitLink {
	if node == nil {
		return nil
	}
	for i := range node.ExitAWGLinks {
		if node.ExitAWGLinks[i].TargetID == targetID {
			return &node.ExitAWGLinks[i]
		}
	}
	return nil
}

// ensureAWGExitLinks creates stable per-target kernel AWG material for a
// multi-exit balancer node and the matching Role=exit server nodes. Each target
// gets one balancer-side awg-exit-nX client link in 10.10.0.0/24 and each exit
// node gets one server keypair/listen port for its awg0 interface.
func ensureAWGExitLinks(chain *model.Chain, node *model.ChainNode) error {
	if chain == nil || node == nil || len(node.ExitTargets) == 0 {
		return nil
	}
	taken := exitAddresses(chain)
	for idx, targetID := range node.ExitTargets {
		target := chainNodeByID(chain, targetID)
		if target == nil {
			return fmt.Errorf("exit target %q not found", targetID)
		}
		if target.Role != model.NodeRoleExit {
			return fmt.Errorf("exit target %q has role %q, want %q", targetID, target.Role, model.NodeRoleExit)
		}
		if target.ExitAWGServerPriv == "" || target.ExitAWGServerPub == "" {
			priv, pub, err := GenerateWireGuardKeypair()
			if err != nil {
				return fmt.Errorf("exit target %q: generate awg server keypair: %w", targetID, err)
			}
			target.ExitAWGServerPriv = priv
			target.ExitAWGServerPub = pub
		}
		if target.ExitAWGListenPort == 0 {
			// Keep exit server listen ports deterministic and away from the default
			// user-entry port. The balancer can override later if a UI field is added.
			target.ExitAWGListenPort = 52000 + idx
		}

		link := exitLinkByTarget(node, targetID)
		if link == nil {
			node.ExitAWGLinks = append(node.ExitAWGLinks, model.AWGExitLink{TargetID: targetID})
			link = &node.ExitAWGLinks[len(node.ExitAWGLinks)-1]
		}
		if link.InterfaceName == "" {
			link.InterfaceName = fmt.Sprintf("awg-exit-n%d", idx+1)
		}
		if link.ClientPriv == "" || link.ClientPub == "" {
			priv, pub, err := GenerateWireGuardKeypair()
			if err != nil {
				return fmt.Errorf("exit target %q: generate awg client keypair: %w", targetID, err)
			}
			link.ClientPriv = priv
			link.ClientPub = pub
		}
		if link.Address == "" {
			link.Address = allocateAWGExitIP(taken)
			taken = append(taken, link.Address)
		}
		if link.ClientPort == 0 {
			link.ClientPort = 51900 + idx + 1
		}
	}
	return nil
}

// createBackup makes a timestamped backup of the current config under $HOME
// (writable without sudo) and returns the backup path. Uses cp — the backup is
// PRESERVED (never destroyed by rollback) so a second recovery attempt is
// always possible. Returns ("", nil) when there is no existing config (first
// deploy); callers must tolerate that (rollback becomes a no-op restore).
func createBackup(client ports.SSHClient, file string) (string, error) {
	// Name the backup after the source file's basename so multiple files backed
	// up in the same second (a multi-file AWG push: awg0.conf + awg-exit-n1.conf
	// + ...) don't all collide into one "config.json.bak" and clobber each other.
	// For the sing-box path (/etc/sing-box/config.json → "config.json.bak") this
	// is identical to the old hardcoded behavior. For AWG confs each gets its own
	// "<basename>.bak" inside the timestamped dir.
	bakName := filepath.Base(file) + ".bak"
	cmd := `set -e
HOME_DIR="${HOME:-/tmp}"
BAK_DIR="$HOME_DIR/sing-box-orch-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BAK_DIR"
if [ -f "` + file + `" ]; then
	cp -p "` + file + `" "$BAK_DIR/` + bakName + `"
	echo "$BAK_DIR/` + bakName + `"
else
	# No prior config — record an empty backup path so the caller knows rollback
	# is unavailable, but still return the marker dir for consistency.
	echo "$BAK_DIR/` + bakName + `"
fi`
	out, err := client.Run(cmd)
	return strings.TrimSpace(out), err
}

// performRollback restores the backup via cp (NOT mv — the backup is preserved
// for a second attempt), then restarts the service. If the backup file does not
// exist (first deploy with no prior config) this is a no-op restore.
func performRollback(client ports.SSHClient, file, backupPath, serviceName string, useSudo bool) error {
	if backupPath == "" {
		return fmt.Errorf("no backup path provided")
	}
	cmd := fmt.Sprintf(`test -f %s && cp %s %s; systemctl restart %s; sleep 2; systemctl is-active --quiet %s || true`,
		backupPath, backupPath, file, serviceName, serviceName)
	if useSudo {
		cmd = fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	_, err := client.Run(cmd)
	if err != nil {
		slog.Error("deploy: rollback FAILED",
			"file", file, "backup", backupPath, "service", serviceName, "err", err)
		return fmt.Errorf("rollback failed: %w", err)
	}
	slog.Warn("deploy: rollback applied (restored previous config)",
		"file", file, "backup", backupPath, "service", serviceName)
	return nil
}

// cleanupBackups keeps only the last 5 backups.
func cleanupBackups(client ports.SSHClient, file string) {
	client.Run(fmt.Sprintf(`ls -t %s.bak.* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true`, file))
}

// pushConfig writes the config to the remote host with the reliable deploy
// sequence: backup (cp, before write) → self-signed cert (if TLS inbounds) →
// upload via stdin cat (no heredoc) → sing-box check (stdout+stderr captured) →
// rollback on check-fail (cp, backup preserved) → systemctl restart → real
// health-probe (is-active with retry, journalctl on failure) → rollback on
// inactive. useSudo wraps privileged commands for non-root SSH users. Returns
// a human-readable result string and an error on failure.
//
// nodeID drives the per-host serialization: the whole backup→write→restart→
// rollback sequence runs under withHostLock(nodeID), so concurrent applies
// (CLI, web, background auto-apply, takeover) targeting the same node cannot
// interleave and corrupt the rollback chain (CTO-review C2). This is the
// SINGLE serialization chokepoint — callers must NOT wrap pushConfig in another
// withHostLock(nodeID) (sync.Mutex is not reentrant → deadlock). An empty
// nodeID skips locking (only acceptable for throwaway test hosts).
func pushConfig(client ports.SSHClient, nodeID, cfgContent string, useSudo bool) (string, error) {
	if nodeID == "" {
		return pushConfigLocked(client, cfgContent, useSudo)
	}
	type pushResult struct {
		out string
		err error
	}
	r := withHostLock(nodeID, func() pushResult {
		out, err := pushConfigLocked(client, cfgContent, useSudo)
		return pushResult{out: out, err: err}
	})
	return r.out, r.err
}

// pushConfigLocked performs the actual deploy sequence. The caller is
// responsible for holding the per-host lock (via pushConfig/withHostLock).
func pushConfigLocked(client ports.SSHClient, cfgContent string, useSudo bool) (string, error) {

	// sudo wraps a single command; sudoBash wraps a pipeline.
	sudo := func(cmd string) string {
		if useSudo {
			return "sudo " + cmd
		}
		return cmd
	}
	sudoB := func(cmd string) string {
		if !useSudo {
			return cmd
		}
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}

	var js json.RawMessage
	if err := json.Unmarshal([]byte(cfgContent), &js); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	configFile := "/etc/sing-box/config.json"

	// 1. Backup existing config (best-effort, never blocks the deploy).
	backupPath, backupErr := createBackup(client, configFile)
	if backupErr != nil {
		log.Printf("pushConfig: backup warning for %s: %v", configFile, backupErr)
	}

	// 2. Ensure self-signed TLS cert exists when the config references TLS-based
	// inbounds (TUIC/Hysteria2/VLESS/Trojan). Generated via openssl; best-effort.
	ensureCertForTLSInbounds(client, cfgContent)

	// 3. Upload via stdin cat. When useSudo, the target (/etc/sing-box/config.json)
	// is root-owned, so we write to $HOME first and sudo cp into place (UploadText
	// itself can't sudo the cat, and the path isn't writable as lcp).
	if useSudo {
		tmp := "/tmp/angry-config-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".json"
		if err := client.UploadText(context.Background(), cfgContent, tmp, 0o644); err != nil {
			return "", fmt.Errorf("write config (tmp): %w", err)
		}
		if _, _, _, err := client.RunWithOutput(context.Background(),
			sudoB("cp "+tmp+" "+configFile+" && chmod 644 "+configFile+" && rm -f "+tmp), 30*time.Second); err != nil {
			return "", fmt.Errorf("write config: %w", err)
		}
	} else {
		if err := client.UploadText(context.Background(), cfgContent, configFile, 0o644); err != nil {
			return "", fmt.Errorf("write config: %w", err)
		}
	}

	// 4. sing-box check — capture BOTH streams so the operator sees the real
	// validation error instead of an opaque "exit status 1".
	checkCmd := sudo(fmt.Sprintf("/usr/local/bin/sing-box check -c %s", configFile))
	_, stderr, exit, err := client.RunWithOutput(context.Background(), checkCmd, 60*time.Second)
	if err != nil {
		if backupPath != "" {
			rbErr := performRollback(client, configFile, backupPath, "sing-box", useSudo)
			if rbErr != nil {
				// Wrap the rollback error so callers can detect deploy+rollback
				// failure via errors.Is/As; the original check err is kept in the
				// message for context (CTO-review: use %w to preserve the chain).
				return "", fmt.Errorf("check failed (exit %d): %s | AND rollback failed: %w (check err: %v)", exit, stderr, rbErr, err)
			}
			return "", fmt.Errorf("rolled back — check failed (exit %d): %s (check err: %w)", exit, stderr, err)
		}
		return "", fmt.Errorf("check failed (exit %d, no backup): %s (err: %w)", exit, stderr, err)
	}

	// 5. Restart. No 2>&1 (that would swallow the useful stderr into stdout,
	// which Run discards on error). Keep stderr separate for the error path.
	if _, _, _, err := client.RunWithOutput(context.Background(), sudoB("systemctl restart sing-box"), 60*time.Second); err != nil {
		if backupPath != "" {
			rbErr := performRollback(client, configFile, backupPath, "sing-box", useSudo)
			if rbErr != nil {
				return "", fmt.Errorf("restart failed: %v | AND rollback failed: %w", err, rbErr)
			}
			return "", fmt.Errorf("rolled back — restart failed: %w", err)
		}
		return "", fmt.Errorf("restart failed (no backup): %w", err)
	}

	// 6. Real health-probe: is-active with a short retry (handles the brief
	// "activating" window), and capture journalctl on failure for diagnosis.
	if err := probeServiceUp(client, "sing-box", useSudo); err != nil {
		slog.Error("deploy: service not active after restart — rolling back",
			"err", err)
		if backupPath != "" {
			if rbErr := performRollback(client, configFile, backupPath, "sing-box", useSudo); rbErr != nil {
				slog.Error("deploy: health-probe rollback also failed",
					"file", configFile, "backup", backupPath, "err", rbErr)
			}
		}
		return "", fmt.Errorf("service not active after restart: %v", err)
	}

	// 7. Cleanup old backups.
	cleanupBackups(client, configFile)

	return "success", nil
}

// probeServiceUp waits up to ~7s for the unit to become active. On failure it
// returns the last 30 journalctl lines so the operator sees why sing-box didn't
// start (the old implementation reported success as long as `systemctl restart`
// returned 0, which is NOT the same as the service staying up).
func probeServiceUp(client ports.SSHClient, service string, useSudo bool) error {
	sudoB := func(cmd string) string {
		if !useSudo {
			return cmd
		}
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	check := sudoB("sleep 3 && systemctl is-active --quiet " + service + " && echo UP || echo DOWN")
	for attempt := 0; attempt < 3; attempt++ {
		out, _, _, _ := client.RunWithOutput(context.Background(), check, 30*time.Second)
		if strings.TrimSpace(out) == "UP" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	journal, _, _, _ := client.RunWithOutput(context.Background(),
		sudoB("journalctl -u "+service+" -n 30 --no-pager 2>/dev/null"), 30*time.Second)
	tail := strings.TrimSpace(journal)
	if len(tail) > 1200 {
		tail = tail[len(tail)-1200:]
	}
	return fmt.Errorf("service not active; journal:\n%s", tail)
}

// ensureCertForTLSInbounds generates a self-signed cert (best-effort) when the
// config has TLS-based inbounds that reference /etc/sing-box/cert.pem. This
// replaces the old writeTUICCert/base64 path, which only covered TUIC and used
// a hardcoded CN. Here we cover all TLS inbounds and use the host's address.
func ensureCertForTLSInbounds(client ports.SSHClient, cfgContent string) {
	needsCert := strings.Contains(cfgContent, `"type":"tuic"`) ||
		strings.Contains(cfgContent, `"type": "tuic"`) ||
		strings.Contains(cfgContent, `"type":"hysteria2"`) ||
		strings.Contains(cfgContent, `"type": "hysteria2"`) ||
		strings.Contains(cfgContent, `"certificate_path":"/etc/sing-box/cert.pem"`) ||
		strings.Contains(cfgContent, `"certificate_path": "/etc/sing-box/cert.pem"`)
	if !needsCert {
		return
	}
	certCmd := `test -f /etc/sing-box/cert.pem || (which openssl >/dev/null 2>&1 && \
openssl req -x509 -newkey rsa:2048 -keyout /etc/sing-box/key.pem \
-out /etc/sing-box/cert.pem -days 3650 -nodes -subj "/CN=sing-box" 2>/dev/null && \
chmod 644 /etc/sing-box/cert.pem /etc/sing-box/key.pem) \
|| echo 'cert-gen skipped'`
	stdout, stderr, exitCode, runErr := client.RunWithOutput(context.Background(), certCmd, 60*time.Second)
	if runErr != nil {
		slog.Warn("deploy: self-signed cert generation command failed",
			"stdout", strings.TrimSpace(stdout), "stderr", strings.TrimSpace(stderr), "exit_code", exitCode, "err", runErr)
	} else if strings.Contains(stdout, "cert-gen skipped") {
		slog.Info("deploy: self-signed cert generation skipped (openssl missing or cert already present)")
	}
}

// ==================== XHTTP Transport Support ====================
// XHTTP provides better obfuscation for transport links between nodes.

func buildXHTTPTransportInbound(p *hopParams, tag string, preset *ConnectionPreset) json.RawMessage {
	xhttp := preset.XHTTP
	if xhttp == nil || len(xhttp.Methods) == 0 || len(xhttp.Paths) == 0 {
		xhttp = &XHTTPPreset{
			Methods: []string{"POST"},
			Paths:   []string{"/api/v1/" + safeShortID(p.ShortID)},
			Hosts:   []string{p.ServerName},
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

	transport := &config.TransportOptions{
		Type:        "http", // Using "http" for sing-box's HTTP/2 multiplexing mapped from xhttp
		Host:        []string{p.ServerName},
		Path:        path,
		Method:      method,
		Headers:     headers,
		IdleTimeout: "15s",
		PingTimeout: "15s",
	}

	// Apply advanced 2026 XHTTP obfuscation
	ApplyXHTTPObfuscation(transport, xhttp)

	inb := config.VLESSInbound{
		Type:       "vless",
		Tag:        tag,
		Listen:     "0.0.0.0",
		ListenPort: p.Port,
		Users: []config.VLESSUser{
			{
				Name: tag,
				UUID: p.UUID,
				Flow: "",
			},
		},
		TLS: &config.InboundTLSOptions{
			Enabled:    true,
			ServerName: p.ServerName,
			Reality: &config.InboundRealityOptions{
				Enabled: true,
				Handshake: &config.RealityHandshake{
					Server:     p.ServerName,
					ServerPort: 443,
				},
				PrivateKey: p.PrivateKey,
				ShortID:    []string{p.ShortID},
			},
		},
		Transport: transport,
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
			Paths:   []string{"/api/v1/" + safeShortID(next.ShortID)},
			Hosts:   []string{next.ServerName},
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

	fingerprint := "chrome"
	if preset.Reality != nil && len(preset.Reality.Fingerprints) > 0 {
		fingerprint = preset.Reality.Fingerprints[0]
	}

	transport := &config.TransportOptions{
		Type:        "http", // Mapping xhttp to http type in JSON config payload
		Host:        []string{next.ServerName},
		Path:        path,
		Method:      method,
		Headers:     headers,
		IdleTimeout: "15s",
		PingTimeout: "15s",
	}

	// Apply advanced 2026 XHTTP obfuscation
	ApplyXHTTPObfuscation(transport, xhttp)

	out := config.VLESSOutbound{
		Type:       "vless",
		Tag:        tag,
		Server:     serverAddr,
		ServerPort: next.Port,
		UUID:       next.UUID,
		Flow:       "", // Vision incompatible with xhttp
		TLS: &config.OutboundTLSOptions{
			Enabled:    true,
			ServerName: next.ServerName,
			UTLS: &config.UTLSOptions{
				Enabled:     true,
				Fingerprint: fingerprint,
			},
			Reality: &config.OutboundRealityOptions{
				Enabled:   true,
				PublicKey: pubKeyHex,
				ShortID:   next.ShortID,
			},
		},
		Transport: transport,
	}

	data, _ := json.Marshal(out)
	return data, nil
}

// ==================== User Protocols (TUIC, AWG) ====================

// buildTUICUserInbound builds a TUIC chain user-entry inbound. uuid is the
// user identity; password is an INDEPENDENT credential (must not equal uuid —
// CTO-review M7). The TLS cert is embedded inline (self-signed, generated here)
// instead of referenced by a file path, because pushConfig's
// ensureCertForTLSInbounds only writes /etc/sing-box/cert.pem and would leave a
// /etc/sing-box/certs/tuic-cert.pem reference dangling — which made sing-box
// check fail on a fresh node (caught by e2e against a real VPS).
func buildTUICUserInbound(port int, uuid, password, tag string, preset *ConnectionPreset, p *hopParams) json.RawMessage {
	return buildTUICInboundWithUsers(port, []config.TUICUser{{UUID: uuid, Password: password}}, tag, preset, p)
}

// chainTUICUsers builds the TUIC users array for a chain entry. When the chain
// has assigned users with per-user TUIC credentials, emit one entry per user
// (multi-user inbound — the basis for per-client auth_user routing). When no
// per-user creds are available, fall back to the chain-wide shared creds as a
// single user (legacy behavior). The user Name becomes the auth_user identity
// used by route rules.
func chainTUICUsers(c *model.Chain, users []model.User) []config.TUICUser {
	var out []config.TUICUser
	for _, u := range users {
		if !u.Active || u.IsExpired() {
			continue
		}
		uuid := u.TUICUUID
		password := u.TUICPassword
		if uuid == "" {
			uuid = tuicUUID(c)
		}
		if password == "" {
			password = tuicPassword(c)
		}
		out = append(out, config.TUICUser{UUID: uuid, Password: password})
	}
	if len(out) == 0 {
		// No per-user creds -> legacy single-user with chain-wide creds.
		return []config.TUICUser{{UUID: tuicUUID(c), Password: tuicPassword(c)}}
	}
	return out
}

// buildTUICInboundWithUsers renders a TUIC inbound with an explicit users
// array (multi-user when >1, single-user otherwise). TLS/congestion/auth
// options match buildTUICUserInbound.
func buildTUICInboundWithUsers(port int, users []config.TUICUser, tag string, preset *ConnectionPreset, p *hopParams) json.RawMessage {
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

	serverName := DefaultRealitySNI
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		serverName = preset.Reality.ServerNames[0]
	}

	inb := config.TUICInbound{
		Type:              "tuic",
		Tag:               tag,
		Listen:            "0.0.0.0",
		ListenPort:        port,
		Users:             users,
		CongestionControl: congestion,
		AuthTimeout:       authTimeout,
		ZeroRTTHandshake:  true,
		Heartbeat:         "10s",
		TLS:               buildTUICInlineTLS(serverName),
	}

	data, _ := json.Marshal(inb)
	return data
}

// buildTUICInlineTLS returns a TLS config for a TUIC inbound with the self-signed
// certificate embedded inline (Certificate/Key PEM), not referenced by path.
// The path-based variant (buildTUICTLSOptions) relied on a file that
// ensureCertForTLSInbounds never wrote for the tuic-cert.pem path, so sing-box
// check failed on a fresh node. Inline cert mirrors the standalone TUIC path
// (merged_config.go) and removes the dependency on remote cert generation.
// ALPN h3 is required: a TUIC client (QUIC) aborts with "server did not select
// an ALPN protocol" when the server omits it (caught by e2e client connect).
func buildTUICInlineTLS(serverName string) *config.InboundTLSOptions {
	tls := &config.InboundTLSOptions{
		Enabled:    true,
		ServerName: serverName,
		ALPN:       []string{"h3"},
	}
	if cert, key, err := GenerateSelfSignedCert(serverName); err == nil {
		tls.Certificate = cert
		tls.Key = key
	}
	return tls
}

// buildAWGUserInbound builds a single-peer userspace AWG endpoint (config.WireGuardEndpoint
// with System:false). This is the PRE-kernel-rework user-entry shape.
//
// TEST-ONLY / LEGACY: production user-facing AWG servers (chain entry, standalone,
// exit) now use kernel `awg-quick@awg0` + sing-box TUN-overlay — see awg_server.go
// (RenderServerAWGConf) + awg_tun_overlay.go (BuildAWGTUNOverlay). This builder has
// NO production callers (grep-verified: only clientconfig_test.go / helpers_test.go
// reference it). It is retained because those tests assert peer/amnezia-material
// logic that is still meaningful for the userspace path used by inter-node AWG
// transit (buildAWGTransportInbound/Outbound). Do NOT wire this into a production
// render path — per AGENTS.md #11, the userspace-endpoint path is unstable under
// amnezia for user-facing servers. Remove once the transit path also moves to kernel.
func buildAWGUserInbound(port int, uuid string, tag string, preset *ConnectionPreset, serverPrivKeyB64, clientPubKey string) ([]byte, string, error) {
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}

	var privKeyB64, pubKeyB64 string
	var err error

	if serverPrivKeyB64 != "" {
		privKeyB64 = serverPrivKeyB64
		pubKeyB64, err = deriveWireGuardPublicFromPrivate(privKeyB64)
		if err != nil {
			return nil, "", fmt.Errorf("derive awg pub from provided priv: %w", err)
		}
	} else {
		privKeyB64, pubKeyB64, err = generateWireGuardKeypair()
		if err != nil {
			return nil, "", fmt.Errorf("generate awg server keypair: %w", err)
		}
	}

	peerPub := clientPubKey
	if peerPub == "" {
		peerPub = "CLIENT_PUBLIC_KEY_HERE"
	}

	ep := config.WireGuardEndpoint{
		Type:       "wireguard",
		Tag:        tag, // Use the user-in tag for routing natively
		System:     false,
		MTU:        1420,
		Address:    []string{"10.8.0.1/32"},
		PrivateKey: privKeyB64,
		ListenPort: port,
		Peers: []config.WireGuardPeer{
			{
				PublicKey:  peerPub,
				AllowedIPs: []string{"10.8.0.2/32"},
			},
		},
		Amnezia: BuildAWGAmnezia(awg, preset, nil), // standalone single-peer: no persisted material
	}

	epJSON, _ := json.Marshal(ep)

	return epJSON, pubKeyB64, nil
}

// buildAWGUserInboundMulti builds a multi-peer AWG endpoint for a chain entry
// where each user is a distinct WireGuard peer. Every user contributes one peer
// carrying their AWGPublicKey (so the server accepts their handshakes) and
// their AWGAddress as AllowedIPs (the peer's inner source IP — this is what
// per-client source_ip_cidr route rules match on). Users without an
// AWGPublicKey or AWGAddress are skipped (they cannot be a peer). When no user
// qualifies, the endpoint still gets a placeholder peer so the config is valid
// (clients just won't be able to connect until creds are assigned).
//
// Returns the endpoint JSON and the server's public key (derived from
// serverPrivKeyB64, or generated when empty — caller persists the latter).
//
// TEST-ONLY / LEGACY: same status as buildAWGUserInbound above. Production
// chain-entry AWG is kernel awg0 + TUN-overlay (RenderServerAWGConf builds the
// .conf with all user peers directly). This builder has NO production callers
// (only clientconfig_test.go). The per-user peer-material logic it exercises
// (AWGPublicKey/AWGAddress → peer) is still correct and reused conceptually by
// RenderServerAWGConf, which is why the tests are kept. Do NOT wire this into a
// production render path — see AGENTS.md #11 / PROGRESS §1.A.
func buildAWGUserInboundMulti(port int, tag string, preset *ConnectionPreset, serverPrivKeyB64 string, users []model.User, material *AWGObfsMaterial) ([]byte, string, error) {
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}

	var privKeyB64, pubKeyB64 string
	var err error
	if serverPrivKeyB64 != "" {
		privKeyB64 = serverPrivKeyB64
		pubKeyB64, err = deriveWireGuardPublicFromPrivate(privKeyB64)
		if err != nil {
			return nil, "", fmt.Errorf("derive awg pub from provided priv: %w", err)
		}
	} else {
		privKeyB64, pubKeyB64, err = generateWireGuardKeypair()
		if err != nil {
			return nil, "", fmt.Errorf("generate awg server keypair: %w", err)
		}
	}

	peers := make([]config.WireGuardPeer, 0, len(users))
	for _, u := range users {
		if !u.Active {
			continue
		}
		if u.AWGPublicKey == "" || u.AWGAddress == "" {
			continue // no per-user AWG creds -> cannot be a peer
		}
		peers = append(peers, config.WireGuardPeer{
			PublicKey:  u.AWGPublicKey,
			AllowedIPs: []string{u.AWGAddress},
		})
	}
	if len(peers) == 0 {
		// No qualified users yet: keep the endpoint valid with a placeholder so
		// sing-box accepts the config. Replaced once users get AWG creds.
		peers = []config.WireGuardPeer{
			{PublicKey: "CLIENT_PUBLIC_KEY_HERE", AllowedIPs: []string{"10.8.0.2/32"}},
		}
	}

	ep := config.WireGuardEndpoint{
		Type:       "wireguard",
		Tag:        tag, // user-in tag — route rules address this endpoint by tag
		System:     false,
		MTU:        1420,
		Address:    []string{"10.8.0.1/32"}, // server tunnel IP
		PrivateKey: privKeyB64,
		ListenPort: port,
		Peers:      peers,
		Amnezia:    BuildAWGAmnezia(awg, preset, material),
	}

	epJSON, _ := json.Marshal(ep)
	return epJSON, pubKeyB64, nil
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
			Paths:   []string{"/api/v1/" + safeShortID(shortID)},
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

	inb := config.VLESSInbound{
		Type:       "vless",
		Tag:        "transport-in",
		Listen:     "0.0.0.0",
		ListenPort: port,
		Users: []config.VLESSUser{{
			Name: "transport",
			UUID: uuid,
			Flow: "", // Vision incompatible with xhttp
		}},
		TLS: &config.InboundTLSOptions{
			Enabled:    true,
			ServerName: serverName,
			Reality: &config.InboundRealityOptions{
				Enabled: true,
				Handshake: &config.RealityHandshake{
					Server:     serverName,
					ServerPort: 443,
				},
				PrivateKey: privKeyB64,
				ShortID:    []string{shortID},
			},
		},
		Transport: &config.TransportOptions{
			Type:        "http",
			Host:        []string{serverName},
			Path:        path,
			Method:      method,
			Headers:     headers,
			IdleTimeout: "15s",
			PingTimeout: "15s",
		},
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

// GenerateStableTUICUserCreds generates stable UUID + an INDEPENDENT password
// for a TUIC user entry at chain creation time. The password must not equal the
// UUID: a TUIC link exposes both in its userinfo, so a shared secret would mean
// leaking the UUID also leaks the password (and vice versa). The password is a
// 16-byte url-safe base64 secret independent of the identity (CTO-review M7).
func GenerateStableTUICUserCreds() (uuid, password string) {
	uuid = generateStableUUID()
	password = GenerateTUICPassword()
	return uuid, password
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

// usersByChainMap builds a chain-name -> users map for the given chains by
// loading all users from the store and filtering to those whose ChainNames
// include each chain. Only active, non-expired users are included. Used by
// buildMergedNodeConfig to render multi-user entry inbounds (per-client
// routing). Returns nil when the store has no users, so single-user chains
// keep the legacy fallback behavior.
func usersByChainMap(store *Store, chains []*model.Chain) map[string][]model.User {
	if store == nil || len(chains) == 0 {
		return nil
	}
	all, err := store.ListUsers()
	if err != nil || len(all) == 0 {
		return nil
	}
	out := map[string][]model.User{}
	for _, c := range chains {
		for _, u := range all {
			if !u.Active || u.IsExpired() {
				continue
			}
			for _, cn := range u.ChainNames {
				if cn == c.Name {
					out[c.Name] = append(out[c.Name], *u)
					break
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// usersByInboundMap builds a map keyed by standalone-inbound Tag → users
// assigned to that inbound (via ForUsers), mirroring usersByChainMap. Only
// active, non-expired users with a matching ID are included. Used to render
// multi-peer standalone AWG endpoints (one WireGuard peer per user). Returns
// nil when store is nil, no inbounds, or no assignments. Inbounds without a
// stable Tag are skipped (they fall back to the legacy single-peer render).
func usersByInboundMap(store *Store, inbounds []model.NodeInbound) map[string][]model.User {
	if store == nil || len(inbounds) == 0 {
		return nil
	}
	all, err := store.ListUsers()
	if err != nil || len(all) == 0 {
		return nil
	}
	// index users by ID for O(users) lookup
	byID := make(map[string]*model.User, len(all))
	for _, u := range all {
		byID[u.ID] = u
	}
	out := map[string][]model.User{}
	for _, ib := range inbounds {
		if ib.Tag == "" || len(ib.ForUsers) == 0 {
			continue
		}
		for _, uid := range ib.ForUsers {
			u, ok := byID[uid]
			if !ok || !u.Active || u.IsExpired() {
				continue
			}
			out[ib.Tag] = append(out[ib.Tag], *u)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ApplyMergedNode reads all chains from the store that contain this node,
// merges them with standalone inbounds into a single sing-box config,
// and pushes it to the remote node via SSH.
//
// Per-host serialization of the critical backup→write→restart section is done
// inside pushConfig via withHostLock(info.ID) — the single chokepoint. This
// method itself does NOT take the lock, so the config build / SSH connect /
// backend.Deploy stay unconstrained and other nodes are not blocked (CTO-review
// C2 redesign: a previous version wrapped the whole method in withHostLock AND
// pushConfig in a global mutex, which deadlocked-adjacent and serialized all
// nodes).
func (a *Applier) ApplyMergedNode(
	ctx context.Context,
	store *Store,
	info *model.NodeInfo,
) (*ApplyReport, *MergeReport, error) {
	return a.applyMergedNodeLocked(ctx, store, info)
}

// applyMergedNodeLocked contains the real merged-deploy logic. The per-host
// lock is acquired inside pushConfig (the deploy chokepoint); this function
// must NOT wrap the whole body in withHostLock(info.ID).
func (a *Applier) applyMergedNodeLocked(
	ctx context.Context,
	store *Store,
	info *model.NodeInfo,
) (*ApplyReport, *MergeReport, error) {
	if info.Addr == "" {
		return nil, nil, fmt.Errorf("node %q has no address", info.ID)
	}

	chains, err := store.GetChainsForNode(info.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("get chains for node: %w", err)
	}

	for _, c := range chains {
		resolved, err := store.ResolveNodes(c)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve chain %q: %w", c.Name, err)
		}
		c.Nodes = resolved
	}

	for i := range info.Inbounds {
		ib := &info.Inbounds[i]
		if ib.UUID == "" {
			ib.UUID = generateStableUUID()
		}
		if ib.ServerPrivKey == "" {
			switch ib.Protocol {
			case "vless-reality":
				b := make([]byte, 32)
				rand.Read(b)
				ib.ServerPrivKey = base64.RawURLEncoding.EncodeToString(b)
				var privKeyArr, pub [32]byte
				copy(privKeyArr[:], b)
				curve25519.ScalarBaseMult(&pub, &privKeyArr)
				ib.ServerPubKey = base64.RawURLEncoding.EncodeToString(pub[:])
				sb := make([]byte, 8)
				rand.Read(sb)
				ib.ShortID = hex.EncodeToString(sb)
			case "awg":
				if priv, pub, kerr := generateWireGuardKeypair(); kerr == nil {
					ib.ServerPrivKey = priv
					ib.ServerPubKey = pub
				}
			}
		}
		// Hysteria2: ensure a per-node obfs password exists (older inbounds
		// saved before this field was added have none).
		if ib.Protocol == "hysteria2" && ib.ObfsPassword == "" {
			ib.ObfsPassword = GenerateHysteria2ObfsPassword()
		}
	}

	// Fetch this node's MTProxy users (the node-level MTProxy inbound is built
	// from them in buildMergedNodeConfig). Empty for non-MTProxy nodes.
	mtproxyUsers := store.ListMTProxyUsersForNode(info.ID)
	cfg, mergeReport, err := buildMergedNodeConfig(info, chains, usersByChainMap(store, chains), usersByInboundMap(store, info.Inbounds), mtproxyUsers)
	if err != nil {
		return nil, mergeReport, fmt.Errorf("build merged config: %w", err)
	}

	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, mergeReport, fmt.Errorf("marshal merged config: %w", err)
	}

	resolved := resolveHostKey(store, &info.Host)
	if resolved.KeyPath == "" {
		log.Printf("ssh: no key configured for node %s and no default key set", info.ID)
	}
	client, err := a.connector.Connect(resolved.Addr, resolved.User, resolved.KeyPath)
	if err != nil {
		return nil, mergeReport, fmt.Errorf("ssh connect: %w", err)
	}
	defer client.Close()

	backend := a.factory.Create()
	if _, deployErr := backend.DeployWithOptions(ctx, info.Host, model.DeployOptions{UseSudo: info.UseSudo}); deployErr != nil {
		return nil, mergeReport, fmt.Errorf("deploy sing-box: %w", deployErr)
	}

	// Install the AWG kernel module when the node runs a kernel AWG interface
	// (standalone AWG inbound, or a chain AWG entry/transit/exit). ApplyChain
	// already does this; ApplyMergedNode historically skipped it — a standalone
	// AWG deploy would then push an awg0.conf with no module to load it. Gate on
	// the same condition ApplyChain uses plus the exit role (no AWG inbound but
	// ExitAWGServer* present).
	needsAWGModule := false
	for _, ib := range info.Inbounds {
		if ib.Protocol == "awg" {
			needsAWGModule = true
			break
		}
	}
	if !needsAWGModule {
		for _, c := range chains {
			if c.UserProtocol == model.UserProtocolAWG || c.Transport == model.TransportAWG {
				needsAWGModule = true
				break
			}
		}
	}
	if needsAWGModule {
		if awgErr := backend.InstallAWGModuleWithOptions(ctx, info.Host, model.DeployOptions{UseSudo: info.UseSudo}); awgErr != nil {
			return nil, mergeReport, fmt.Errorf("install awg module: %w", awgErr)
		}
		// Enable IPv4 forwarding for every AWG node (same pairing as ApplyChain):
		// transit nodes get a userspace transport endpoint (no awg0.conf) but still
		// forward packets between transport-in and the egress outbound — without
		// ip_forward=1 the kernel drops them and egress silently fails.
		if fwdErr := ensureIPForward(client, info.UseSudo); fwdErr != nil {
			return nil, mergeReport, fmt.Errorf("enable ip_forward: %w", fwdErr)
		}
	}

	// Read old config before pushing to compute inbound diff for observability.
	oldCfgBytes, _ := client.Run("cat /etc/sing-box/config.json 2>/dev/null")
	oldCfg := string(oldCfgBytes)

	// Render the kernel awg-quick .conf files this node needs (standalone awg0,
	// chain entry/transit/exit). Empty for non-AWG nodes — pushConfigWithAWG
	// then falls through to the plain pushConfig path.
	awgFiles := renderAWGConfsForDeploy(store, info, chains)

	_, pushErr := pushConfigWithAWG(client, info.ID, string(cfgJSON), awgFiles, info.UseSudo)
	if pushErr != nil {
		if strings.Contains(pushErr.Error(), "rollback successful") {
			return nil, mergeReport, fmt.Errorf("ROLLBACK APPLIED: %w", pushErr)
		}
		return nil, mergeReport, fmt.Errorf("push config: %w", pushErr)
	}

	// Compute diff of inbound/endpoint tags after successful push.
	if oldCfg != "" {
		mergeReport.AddedInbounds, mergeReport.RemovedInbounds = diffInboundTags(oldCfg, string(cfgJSON))
	}

	recordDeploySuccess(store, info.ID, string(cfgJSON))
	WriteAudit(store, "deploy", "node", info.ID, AuditPayload{"mode": "merged"}, "operator")

	return &ApplyReport{
		ChainName: "<merged>",
		Profile:   "merged",
		Nodes:     []NodeResult{{ID: info.ID, Success: true}},
	}, mergeReport, nil
}

// GenerateSelfSignedCert generates a self-signed RSA certificate and private key
// (exported for use from web/UI layer).
// suitable for use with TUIC, Hysteria2, etc. inbounds.
// Returns PEM-encoded certificate and private key.
func GenerateSelfSignedCert(host string) (certPEM, keyPEM string, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate rsa key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("generate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Angry-BOX Self-Signed"},
			CommonName:   host,
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes}))

	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))

	return certPEM, keyPEM, nil
}
