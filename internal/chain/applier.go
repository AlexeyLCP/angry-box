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
	"math/big"
	"net"
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
		client, err := sshclient.Connect(node.Addr, node.User, node.KeyPath)
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

		cfg, _, buildErr := buildMergedNodeConfig(nodeInfo, nodeChains)
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
		client, connErr := sshclient.Connect(node.Addr, node.User, node.KeyPath)
		if connErr != nil {
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "ssh connect: " + connErr.Error()})
			continue
		}

		backend := a.factory.Create()

		// Install AWG kernel module only when chain uses AWG as user protocol.
		if chain.UserProtocol == model.UserProtocolAWG {
			if awgErr := backend.InstallAWGModule(ctx, node.Host()); awgErr != nil {
				client.Close()
				results = append(results, NodeResult{ID: node.ID, Success: false, Error: "install awg module: " + awgErr.Error()})
				continue
			}
		}

		if _, deployErr := backend.Deploy(ctx, node.Host()); deployErr != nil {
			client.Close()
			results = append(results, NodeResult{ID: node.ID, Success: false, Error: "deploy sing-box: " + deployErr.Error()})
			continue
		}

		_, pushErr := pushConfig(client, node.ID, string(cfgJSON), nodeInfo.UseSudo)
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
				Flow: "xtls-rprx-vision",
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
		Multiplex: &config.MultiplexOptions{
			Enabled: true,
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
		Flow:       "xtls-rprx-vision",
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
		Multiplex: &config.MultiplexOptions{
			Enabled: true,
		},
	}

	data, _ := json.Marshal(out)
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
func BuildAWGAmnezia(awg *AWGPreset, preset *ConnectionPreset) *config.AmneziaOptions {
	level := 0
	if preset != nil && preset.CPSLevel > 0 {
		level = preset.CPSLevel
	} else if awg != nil && awg.CPSLevel > 0 {
		level = awg.CPSLevel
	}
	if level <= 0 {
		return nil
	}
	return BuildAmneziaSection(awg, preset)
}

// buildTUICTLSOptions returns TLS config for a TUIC inbound.
// sing-box 1.13+ does NOT support Reality on TUIC — uses self-signed cert instead.
// Certificates are referenced by path; the actual files are written by pushConfig.
func buildTUICTLSOptions(serverName string) *config.InboundTLSOptions {
	return &config.InboundTLSOptions{
		Enabled:    true,
		ServerName: serverName,
		CertificatePath: "/etc/sing-box/certs/tuic-cert.pem",
		KeyPath:         "/etc/sing-box/certs/tuic-key.pem",
	}
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

// createBackup makes a timestamped backup of the current config under $HOME
// (writable without sudo) and returns the backup path. Uses cp — the backup is
// PRESERVED (never destroyed by rollback) so a second recovery attempt is
// always possible. Returns ("", nil) when there is no existing config (first
// deploy); callers must tolerate that (rollback becomes a no-op restore).
func createBackup(client *sshclient.Client, file string) (string, error) {
	cmd := `set -e
HOME_DIR="${HOME:-/tmp}"
BAK_DIR="$HOME_DIR/sing-box-orch-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BAK_DIR"
if [ -f "` + file + `" ]; then
	cp -p "` + file + `" "$BAK_DIR/config.json.bak"
	echo "$BAK_DIR/config.json.bak"
else
	# No prior config — record an empty backup path so the caller knows rollback
	# is unavailable, but still return the marker dir for consistency.
	echo "$BAK_DIR/config.json.bak"
fi`
	out, err := client.Run(cmd)
	return strings.TrimSpace(out), err
}

// performRollback restores the backup via cp (NOT mv — the backup is preserved
// for a second attempt), then restarts the service. If the backup file does not
// exist (first deploy with no prior config) this is a no-op restore.
func performRollback(client *sshclient.Client, file, backupPath, serviceName string, useSudo bool) error {
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
		return fmt.Errorf("rollback failed: %w", err)
	}
	return nil
}

// cleanupBackups keeps only the last 5 backups.
func cleanupBackups(client *sshclient.Client, file string) {
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
func pushConfig(client *sshclient.Client, nodeID, cfgContent string, useSudo bool) (string, error) {
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
func pushConfigLocked(client *sshclient.Client, cfgContent string, useSudo bool) (string, error) {

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
				return "", fmt.Errorf("check failed (exit %d): %s | AND rollback failed: %v", exit, stderr, rbErr)
			}
			return "", fmt.Errorf("rolled back — check failed (exit %d): %s", exit, stderr)
		}
		return "", fmt.Errorf("check failed (exit %d, no backup): %s", exit, stderr)
	}

	// 5. Restart. No 2>&1 (that would swallow the useful stderr into stdout,
	// which Run discards on error). Keep stderr separate for the error path.
	if _, _, _, err := client.RunWithOutput(context.Background(), sudoB("systemctl restart sing-box"), 60*time.Second); err != nil {
		if backupPath != "" {
			rbErr := performRollback(client, configFile, backupPath, "sing-box", useSudo)
			if rbErr != nil {
				return "", fmt.Errorf("restart failed: %v | AND rollback failed: %v", err, rbErr)
			}
			return "", fmt.Errorf("rolled back — restart failed: %v", err)
		}
		return "", fmt.Errorf("restart failed (no backup): %v", err)
	}

	// 6. Real health-probe: is-active with a short retry (handles the brief
	// "activating" window), and capture journalctl on failure for diagnosis.
	if err := probeServiceUp(client, "sing-box", useSudo); err != nil {
		if backupPath != "" {
			_ = performRollback(client, configFile, backupPath, "sing-box", useSudo)
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
func probeServiceUp(client *sshclient.Client, service string, useSudo bool) error {
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
func ensureCertForTLSInbounds(client *sshclient.Client, cfgContent string) {
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
	_, _, _, _ = client.RunWithOutput(context.Background(), certCmd, 60*time.Second)
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

func buildTUICUserInbound(port int, uuid, tag string, preset *ConnectionPreset, p *hopParams) json.RawMessage {
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

	serverName := "www.microsoft.com"
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		serverName = preset.Reality.ServerNames[0]
	}

	inb := config.TUICInbound{
		Type:              "tuic",
		Tag:               tag,
		Listen:            "0.0.0.0",
		ListenPort:        port,
		Users: []config.TUICUser{
			{
				UUID:     uuid,
				Password: uuid,
			},
		},
		CongestionControl: congestion,
		AuthTimeout:       authTimeout,
		ZeroRTTHandshake:  true,
		Heartbeat:         "10s",
		TLS: buildTUICTLSOptions(serverName),
	}

	data, _ := json.Marshal(inb)
	return data
}

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
		Amnezia: BuildAWGAmnezia(awg, preset),
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

	cfg, mergeReport, err := buildMergedNodeConfig(info, chains)
	if err != nil {
		return nil, mergeReport, fmt.Errorf("build merged config: %w", err)
	}

	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, mergeReport, fmt.Errorf("marshal merged config: %w", err)
	}

	client, err := sshclient.Connect(info.Addr, info.User, info.KeyPath)
	if err != nil {
		return nil, mergeReport, fmt.Errorf("ssh connect: %w", err)
	}
	defer client.Close()

	backend := a.factory.Create()
	if _, deployErr := backend.Deploy(ctx, info.Host); deployErr != nil {
		return nil, mergeReport, fmt.Errorf("deploy sing-box: %w", deployErr)
	}

	// Read old config before pushing to compute inbound diff for observability.
	oldCfgBytes, _ := client.Run("cat /etc/sing-box/config.json 2>/dev/null")
	oldCfg := string(oldCfgBytes)

	_, pushErr := pushConfig(client, info.ID, string(cfgJSON), info.UseSudo)
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
