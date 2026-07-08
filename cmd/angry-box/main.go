package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/config"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
	"github.com/alexeylcp/angry-box/internal/web"
)

var (
	version = "v0.4.1"
	commit  = "none"
	date    = "unknown"

	defaultStorePath = "store.json"
)

const usage = `angry-box — lightweight proxy orchestrator for sing-box-extended.

Usage:
  angry-box <command> [options]

Node commands:
  deploy      Install proxy backend on a remote host
  status      Show proxy status on a remote host
  config      Generate proxy config locally, print to stdout
  apply       Push config to a remote host and restart proxy
  remove      Remove proxy from a remote host
  reload      Gracefully reload proxy on a remote host

Service commands:
  serve       Start HTTP API server (for systemd / init.d daemon)

Host registry:
  host add    Register a host for use in chains
  host list   List registered hosts
  host delete Remove a host from the registry

Chain management:
  chain create   Create a new proxy chain
  chain list     List all chains
  chain show     Show chain details
  chain delete   Delete a chain
  apply-chain    Generate and push configs to all nodes in a chain

Backup & relocation:
  backup store   Export the whole panel as a JSON backup (stdout or -o file)
  backup node    Export one node's portable identity (node id, -o file)
  restore        Import a store or node backup (auto-detect, --force to overwrite)
  relocate       Move a blocked node to a new VPS + re-deploy dependent chains

Other:
  version        Show version information

Common flags:
  -file      Path to store file (default: store.json)
  -config    Path to angry-box config file (default: /etc/angry-box/angry-box.toml)
  -addr      Remote host address (IP:port)
  -user      SSH user (default: root)
  -key       Path to SSH private key
  -port          Listen port for inbound
  -protocol      Protocol (default: VLESS)
  -type          Config type: transport or user (default: transport)
  -profile       Obfuscation profile override (russia_2026 | iran_2026 | china_2026 | maximum_stealth_2026)
  -client-pubkey Client public key for AWG (wireguard) user configs — required for real clients

Examples:
  angry-box host add mynode --addr 192.168.1.1:22 --user root --key ~/.ssh/id_ed25519
  angry-box chain create mychain --nodes mynode1,mynode2,mynode3 --strategy urltest
  angry-box apply-chain mychain
  angry-box deploy -addr 192.168.1.1 -key ~/.ssh/id_ed25519
  angry-box config -port 443
`

// CLI flags.
var (
	storePath    string
	addr         string
	user         string
	keyPath      string
	port         int
	protocol     string
	configType   string
	nodesStr     string
	strategy     string
	transport    string
	userProtocol string
	profile      string
	clientPubKey string

	configPath string
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("No command provided. Defaulting to 'serve' mode...")
		os.Args = append(os.Args, "serve")
	}

	// Load orchestrator config (if present). Flags can still override.
	configPath = os.Getenv("ANGRY_BOX_CONFIG")
	if configPath == "" {
		configPath = config.DefaultConfigPath()
	}

	// Quick pre-parse for global --config flag (before subcommand flag sets)
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
			break
		}
	}

	orchCfg, err := config.Load(configPath)
	if err != nil {
		// A config-load error (malformed TOML, unknown field, unreadable file)
		// must NOT be silently ignored: it previously fell back to DefaultConfig
		// without a word, so an operator's typo could silently drop
		// auth_password_hash / listen_addr / store_file (CTO-review §8 finding).
		// For `serve` we treat it as fatal (the panel would run on wrong
		// defaults); for read-only CLI commands we warn but continue so a
		// broken config does not block `version`/`status` diagnostics.
		cmd := ""
		if len(os.Args) > 1 {
			cmd = os.Args[1]
		}
		if cmd == "serve" {
			fmt.Fprintf(os.Stderr, "config load error (%s): %v\nFix the config file or remove it to regenerate defaults.\n", configPath, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "WARNING: config load error (%s): %v — continuing with defaults\n", configPath, err)
		orchCfg = config.DefaultConfig()
	}

	if orchCfg != nil && orchCfg.StoreFile != "" {
		defaultStorePath = orchCfg.StoreFile
	}

	// Apply global profile + load any external presets for *all* commands (not just serve)
	// This fixes the previous --config flag limitation for profile/presets.
	if orchCfg.DefaultObfuscationProfile != "" {
		if _, ok := chain.GetPreset(orchCfg.DefaultObfuscationProfile); ok {
			chain.SetDefaultProfile(orchCfg.DefaultObfuscationProfile)
		}
	}
	if orchCfg.PresetsFile != "" {
		loadExternalPresets(orchCfg.PresetsFile)
	}

	cmd := os.Args[1]
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Print(usage)
		os.Exit(0)
	}

	// For commands that use SSH and a store path flag, the actual `storePath` might be parsed later.
	// But `storePath` string flag default is defaultStorePath. It's safe to initialize the HostKeyManager
	// here. If a command overrides the `-file` flag, it should ideally re-initialize, but for most
	// cases (like serve), we can initialize it inside the command after parsing. To be safe, we'll initialize 
	s := chain.NewStore(defaultStorePath)
	sshclient.SetHostKeyManager(s)
	sshclient.SetKeyResolver(s)

	switch cmd {
	case "host":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: angry-box host <add|list|delete> [options]\n")
			os.Exit(1)
		}
		hostCmd(os.Args[2])

	case "chain":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: angry-box chain <create|list|show|delete> [options]\n")
			os.Exit(1)
		}
		chainCmd(os.Args[2])

	case "apply-chain":
		applyChainCmd()

	case "backup":
		backupCmd()
	case "restore":
		restoreCmd()
	case "relocate":
		relocateCmd()

	case "serve":
		serveCmd()

	case "version":
		fmt.Printf("angry-box %s\n", version)
		fmt.Printf("commit: %s\n", commit)
		fmt.Printf("built:  %s\n", date)

	case "deploy", "status", "config", "apply", "remove", "reload":
		nodeCmd(cmd)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", cmd, usage)
		os.Exit(1)
	}
}

// ─── Host commands ────────────────────────────────────────────────────────────

func hostCmd(action string) {
	switch action {
	case "add":
		fs := flag.NewFlagSet("host add", flag.ExitOnError)
		fs.StringVar(&storePath, "file", defaultStorePath, "store file path")
		fs.StringVar(&addr, "addr", "", "SSH address (IP:port)")
		fs.StringVar(&user, "user", "root", "SSH user")
		fs.StringVar(&keyPath, "key", "", "path to SSH private key")

		id, flagArgs := popFirstArg(os.Args[3:])
		_ = fs.Parse(flagArgs)

		if id == "" {
			fmt.Fprintf(os.Stderr, "usage: angry-box host add <id> --addr <addr> --key <key>\n")
			os.Exit(1)
		}

		requireVal(addr, "addr")
		requireVal(keyPath, "key")

		s := chain.NewStore(storePath)
		if err := s.SaveHost(&model.Host{
			ID:      id,
			Addr:    addr,
			User:    user,
			KeyPath: keyPath,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("host %q registered\n", id)

	case "list":
		fs := flag.NewFlagSet("host list", flag.ExitOnError)
		fs.StringVar(&storePath, "file", defaultStorePath, "store file path")
		_ = fs.Parse(os.Args[3:])

		s := chain.NewStore(storePath)
		hosts, err := s.ListHosts()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(hosts) == 0 {
			fmt.Println("no hosts registered")
			return
		}
		for _, h := range hosts {
			fmt.Printf("%s  %s@%s  key=%s\n", h.ID, h.User, h.Addr, h.KeyPath)
		}

	case "delete":
		fs := flag.NewFlagSet("host delete", flag.ExitOnError)
		fs.StringVar(&storePath, "file", defaultStorePath, "store file path")

		id, flagArgs := popFirstArg(os.Args[3:])
		_ = fs.Parse(flagArgs)

		if id == "" {
			fmt.Fprintf(os.Stderr, "usage: angry-box host delete <id>\n")
			os.Exit(1)
		}

		s := chain.NewStore(storePath)
		if err := s.DeleteHost(id); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("host %q deleted\n", id)

	default:
		fmt.Fprintf(os.Stderr, "unknown host command: %s\n", action)
		os.Exit(1)
	}
}

// ─── Chain commands ───────────────────────────────────────────────────────────

func chainCmd(action string) {
	switch action {
	case "create":
		fs := flag.NewFlagSet("chain create", flag.ExitOnError)
		fs.StringVar(&storePath, "file", defaultStorePath, "store file path")
		fs.StringVar(&nodesStr, "nodes", "", "comma-separated host IDs (required)")
		fs.StringVar(&strategy, "strategy", "urltest", "routing strategy (urltest, failover, selector, bond)")
		fs.StringVar(&transport, "transport", "xhttp", "transport between nodes (xhttp or reality)")
		fs.StringVar(&userProtocol, "user-protocol", "tuic", "user entry protocol (tuic, awg, vless-reality)")
		fs.StringVar(&profile, "profile", "", "obfuscation profile override (e.g. china_2026, russia_2026)")

		name, flagArgs := popFirstArg(os.Args[3:])
		_ = fs.Parse(flagArgs)

		if name == "" {
			fmt.Fprintf(os.Stderr, "usage: angry-box chain create <name> --nodes id1,id2,id3 [--strategy urltest] [--transport xhttp] [--user-protocol tuic]\n")
			os.Exit(1)
		}

		requireVal(nodesStr, "nodes")

		nodeIDs := strings.Split(nodesStr, ",")
		if len(nodeIDs) < 1 {
			fmt.Fprintf(os.Stderr, "error: at least one node is required\n")
			os.Exit(1)
		}

		s := chain.NewStore(storePath)

		// Validate all hosts exist.
		nodes := make([]model.ChainNode, 0, len(nodeIDs))
		for _, id := range nodeIDs {
			id = strings.TrimSpace(id)
			if _, err := s.GetHost(id); err != nil {
				fmt.Fprintf(os.Stderr, "error: host %q not found — register it first with 'host add'\n", id)
				os.Exit(1)
			}
			nodes = append(nodes, model.ChainNode{ID: id})
		}

		if profile != "" {
			if _, ok := chain.GetPreset(profile); !ok {
				fmt.Fprintf(os.Stderr, "error: unknown obfuscation profile %q (available: %v)\n", profile, chain.ListPresets())
				os.Exit(1)
			}
		}

		c := &model.Chain{
			Name:               name,
			Nodes:              nodes,
			Strategy:           model.Strategy(strategy),
			Transport:          model.TransportType(transport),
			UserProtocol:       model.UserProtocol(userProtocol),
			ObfuscationProfile: profile,
		}

		// Generate stable user-entry credentials once at creation time for AWG/TUIC.
		// This is the key change for "AWG works like clockwork" — clients configure once.
		// Transport hop keys still rotate on every apply for security.
		if userProtocol == "awg" {
			priv, pub, err := chain.GenerateWireGuardKeypair()
			if err == nil {
				c.AWGEntryServerPriv = priv
				c.AWGEntryServerPub = pub
			}
		}
		if userProtocol == "tuic" {
			// Stable UUID + password for the single TUIC user on the entry node
			uuid, _, err := chain.GenerateStableTUICUserCreds()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: generate TUIC creds: %v\n", err)
				os.Exit(1)
			}
			c.TUICEntryUserUUID = uuid
			c.TUICEntryUserPassword = uuid
		}

		if err := s.SaveChain(c); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("chain %q created with %d nodes (strategy: %s, transport: %s, user: %s, profile: %s)\n",
			name, len(nodes), strategy, transport, userProtocol, profile)

	case "list":
		fs := flag.NewFlagSet("chain list", flag.ExitOnError)
		fs.StringVar(&storePath, "file", defaultStorePath, "store file path")
		_ = fs.Parse(os.Args[3:])

		s := chain.NewStore(storePath)
		chains, err := s.ListChains()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(chains) == 0 {
			fmt.Println("no chains defined")
			return
		}
		for _, c := range chains {
			nodeIDs := make([]string, len(c.Nodes))
			for i, n := range c.Nodes {
				nodeIDs[i] = n.ID
			}
			fmt.Printf("%s  nodes: [%s]  strategy: %s\n", c.Name, strings.Join(nodeIDs, " → "), c.Strategy)
		}

	case "show":
		fs := flag.NewFlagSet("chain show", flag.ExitOnError)
		fs.StringVar(&storePath, "file", defaultStorePath, "store file path")

		name, flagArgs := popFirstArg(os.Args[3:])
		_ = fs.Parse(flagArgs)

		if name == "" {
			fmt.Fprintf(os.Stderr, "usage: angry-box chain show <name>\n")
			os.Exit(1)
		}

		s := chain.NewStore(storePath)
		c, err := s.GetChain(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("chain:    %s\n", c.Name)
		fmt.Printf("strategy: %s\n", c.Strategy)
		fmt.Printf("nodes:\n")
		for i, n := range c.Nodes {
			fmt.Printf("  %d. %s\n", i+1, n.ID)
			// Try to resolve host details for display.
			if host, err := s.GetHost(n.ID); err == nil {
				fmt.Printf("     addr: %s  user: %s  key: %s\n", host.Addr, host.User, host.KeyPath)
			}
		}

	case "delete":
		fs := flag.NewFlagSet("chain delete", flag.ExitOnError)
		fs.StringVar(&storePath, "file", defaultStorePath, "store file path")

		name, flagArgs := popFirstArg(os.Args[3:])
		_ = fs.Parse(flagArgs)

		if name == "" {
			fmt.Fprintf(os.Stderr, "usage: angry-box chain delete <name>\n")
			os.Exit(1)
		}

		s := chain.NewStore(storePath)
		if err := s.DeleteChain(name); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("chain %q deleted\n", name)

	default:
		fmt.Fprintf(os.Stderr, "unknown chain command: %s\n", action)
		os.Exit(1)
	}
}

// ─── apply-chain ──────────────────────────────────────────────────────────────

func applyChainCmd() {
	fs := flag.NewFlagSet("apply-chain", flag.ExitOnError)
	fs.StringVar(&storePath, "file", defaultStorePath, "store file path")
	fs.StringVar(&clientPubKey, "client-pubkey", "", "client public key to use for AWG user entry (if omitted and chain uses awg, a convenient sample is auto-generated)")

	name, flagArgs := popFirstArg(os.Args[2:])
	_ = fs.Parse(flagArgs)

	if name == "" {
		fmt.Fprintf(os.Stderr, "usage: angry-box apply-chain <name> [--client-pubkey <pub>]\n")
		os.Exit(1)
	}

	s := chain.NewStore(storePath)
	c, err := s.GetChain(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Resolve host references to full connection details.
	resolved, err := s.ResolveNodes(c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	c.Nodes = resolved

	fmt.Printf("applying chain %q (%d nodes, strategy: %s, transport: %s, user: %s)\n",
		c.Name, len(c.Nodes), c.Strategy, c.Transport, c.UserProtocol)

	effProfile := c.ObfuscationProfile
	if effProfile == "" {
		effProfile = chain.GetDefaultPresetName()
	}
	fmt.Printf("effective obfuscation profile: %s\n", effProfile)

	f := factory.New(nil)
	applier := chain.NewApplier(f, nil)
	sshclient.SetHostKeyManager(s)
	sshclient.SetKeyResolver(s)

	ctx := context.Background()
	report, err := applier.ApplyChain(ctx, s, c, clientPubKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apply-chain failed: %v\n", err)
		// Still print partial report if we have one
		if report != nil {
			printApplyReport(report)
		}
		os.Exit(1)
	}

	printApplyReport(report)
}

func printApplyReport(r *chain.ApplyReport) {
	hasErrors := false
	for _, n := range r.Nodes {
		if !n.Success {
			hasErrors = true
			break
		}
	}

	if hasErrors {
		fmt.Printf("\n❌ chain %q applied with ERRORS\n", r.ChainName)
	} else {
		fmt.Printf("\n✓ chain %q applied successfully\n", r.ChainName)
	}
	fmt.Printf("  profile: %s  transport: %s  user: %s\n", r.Profile, r.Transport, r.UserProto)

	for _, n := range r.Nodes {
		status := "OK"
		if !n.Success {
			status = "FAIL"
		}
		fmt.Printf("  - %s: %s", n.ID, status)
		if n.Error != "" {
			fmt.Printf(" (%s)", n.Error)
		}
		fmt.Println()
	}

	if r.AWG != nil && r.UserProto == model.UserProtocolAWG {
		fmt.Println("\n=== AWG Client Config (ready to use / adapt) ===")
		fmt.Printf("Server public key (put in client [Peer] PublicKey): %s\n", r.AWG.ServerPub)
		fmt.Printf("Client public key that was allowed on server:     %s\n", r.AWG.ClientPubUsed)
		if r.AWG.ClientPriv != "" {
			fmt.Printf("Sample client private key (for testing):         %s\n", r.AWG.ClientPriv)
		}
		if r.AWG.Note != "" {
			fmt.Printf("Note: %s\n", r.AWG.Note)
		}
		fmt.Printf(`
[Interface]
PrivateKey = %s
Address = 10.8.0.2/32
MTU = 1420

[Peer]
PublicKey = %s
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = <ENTRY_NODE_PUBLIC_IP>:%d
PersistentKeepalive = 25
`, firstNonEmpty(r.AWG.ClientPriv, "<your-client-private-key>"), r.AWG.ServerPub, defaultUserPortForPrint())
		fmt.Println("amnezia parameters come from the active profile on the server (must match exactly).")
		fmt.Println("==================================================")
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func defaultUserPortForPrint() int { return 8443 }

// loadExternalPresets reads a JSON array of ConnectionPreset from the given path
// and merges them into the global registry (user presets override built-ins on name clash).
func loadExternalPresets(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read presets_file %s: %v\n", path, err)
		return
	}
	var extras []chain.ConnectionPreset
	if err := json.Unmarshal(data, &extras); err != nil {
		fmt.Fprintf(os.Stderr, "warning: presets_file %s is not valid JSON array of presets: %v\n", path, err)
		return
	}
	if err := chain.LoadPresets(extras); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load presets from %s: %v\n", path, err)
		return
	}
	fmt.Printf("loaded %d additional obfuscation preset(s) from %s\n", len(extras), path)
}

// ─── Single-node commands (existing) ──────────────────────────────────────────

func nodeCmd(cmd string) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.StringVar(&addr, "addr", "", "remote host address")
	fs.StringVar(&user, "user", "root", "SSH user")
	fs.StringVar(&keyPath, "key", "", "path to SSH private key")
	fs.IntVar(&port, "port", 0, "listen port")
	fs.StringVar(&protocol, "protocol", "VLESS", "protocol")
	fs.StringVar(&configType, "type", "transport", "config type (transport or user)")
	fs.StringVar(&profile, "profile", "", "obfuscation profile (russia_2026, iran_2026, china_2026, maximum_stealth_2026)")
	fs.StringVar(&clientPubKey, "client-pubkey", "", "client public key for AWG user configs")
	fs.StringVar(&transport, "transport", "xhttp", "transport for -type=transport (xhttp or reality)")
	useSudo := fs.Bool("sudo", false, "wrap privileged remote commands in sudo (for non-root SSH users with passwordless sudo)")
	installAWG := fs.Bool("install-awg", false, "also install the AmneziaWG kernel module during deploy")
	_ = fs.Parse(os.Args[2:])

	// For single node commands, we don't have a storePath flag directly defined in nodeCmd,
	s := chain.NewStore(defaultStorePath)
	sshclient.SetHostKeyManager(s)
	sshclient.SetKeyResolver(s)

	f := factory.New(nil)
	b := f.Create()

	ctx := context.Background()

	switch cmd {
	case "deploy":
		requireHostFlags()
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		opts := model.DeployOptions{UseSudo: *useSudo, InstallAWGModule: *installAWG}
		result, err := b.DeployWithOptions(ctx, host, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "deploy failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("deploy: %s v%s — %s\n", b.Name(), result.Version, result.Message)

	case "status":
		requireHostFlags()
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		status, err := b.GetStatus(ctx, host)
		if err != nil {
			fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("backend:  %s\n", b.Name())
		fmt.Printf("running:  %v\n", status.Running)
		fmt.Printf("version:  %s\n", status.Version)
		fmt.Printf("pid:      %d\n", status.PID)
		fmt.Printf("uptime:   %s\n", status.Uptime)
		if status.Error != "" {
			fmt.Printf("error:    %s\n", status.Error)
		}

	case "config":
		// Deprecation notice: standalone AWG via the CLI uses the legacy
		// userspace WireGuardEndpoint (RenderAWGHop), which diverges from the
		// kernel-AWG rework the web UI / ApplyMergedNode deploy (kernel
		// awg-quick@awg0 + sing-box TUN-overlay — AGENTS.md Known Issue #11).
		// Conversion to kernel mode is a follow-up (needs a Host-shaped TUN-
		// overlay renderer). The path still works but is not the product target.
		if protocol == "awg" {
			fmt.Fprintln(os.Stderr, "warning: `config --protocol awg` uses the legacy userspace AWG endpoint (deprecated).")
			fmt.Fprintln(os.Stderr, "         Use the web UI / `apply-chain` which deploy the kernel awg-quick + TUN-overlay architecture (AGENTS.md #11).")
		}
		ct := parseConfigType(configType)
		// Apply profile override for this generation if provided
		if profile != "" {
			if _, ok := chain.GetPreset(profile); !ok {
				fmt.Fprintf(os.Stderr, "error: unknown profile %q\n", profile)
				os.Exit(1)
			}
			chain.SetDefaultProfile(profile)
		}

		// For AWG user configs without explicit client key: auto-generate a sample client keypair
		// (same UX as apply-chain). This eliminates the dangerous "CLIENT_PUBLIC_KEY_HERE" placeholder.
		effectiveClientPub := clientPubKey
		var sampleClientPriv string
		if ct == model.ConfigUser && isAWGUserConfig() && effectiveClientPub == "" {
			if priv, pub, kerr := chain.GenerateWireGuardKeypair(); kerr == nil {
				effectiveClientPub = pub
				sampleClientPriv = priv
			}
		}

		params := model.ConfigParams{
			Port:     port,
			Protocol: protocol,
			Extra:    map[string]any{},
		}
		if effectiveClientPub != "" {
			params.Extra["clientPubKey"] = effectiveClientPub
		}
		if transport != "" {
			params.Extra["transport"] = transport
		}

		cfg, err := b.GenerateConfig(ct, params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config generation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(cfg.Content)

		// Enhanced AWG client output: if we auto-generated a sample, print full usable client config
		// with server pub (we can't easily get it from GenerateConfig return without model change,
		// so we tell user to derive or re-run with explicit key for production).
		if ct == model.ConfigUser && isAWGUserConfig() {
			printAWGClientExample(effectiveClientPub, sampleClientPriv)
		}

	case "apply":
		requireHostFlags()
		// Deprecation notice: see the `config` case above.
		if protocol == "awg" {
			fmt.Fprintln(os.Stderr, "warning: `apply --protocol awg` uses the legacy userspace AWG endpoint (deprecated).")
			fmt.Fprintln(os.Stderr, "         Use the web UI / `apply-chain` which deploy the kernel awg-quick + TUN-overlay architecture (AGENTS.md #11).")
		}
		ct := parseConfigType(configType)
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		if err := b.ApplyConfig(ctx, host, ct, model.ConfigParams{
			Port:     port,
			Protocol: protocol,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "apply failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("config applied to %s (%s)\n", host.Addr, b.Name())

	case "remove":
		requireHostFlags()
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		if err := b.Remove(ctx, host); err != nil {
			fmt.Fprintf(os.Stderr, "remove failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s removed from %s\n", b.Name(), host.Addr)

	case "reload":
		requireHostFlags()
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		if err := b.Reload(ctx, host); err != nil {
			fmt.Fprintf(os.Stderr, "reload failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s reloaded on %s\n", b.Name(), host.Addr)
	}
}

// ─── Serve ────────────────────────────────────────────────────────────────────

func serveCmd() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)

	// Load orchestrator-level config first for defaults
	cfg, _ := config.Load(configPath)
	defaultListen := cfg.ListenAddr
	if defaultListen == "" {
		defaultListen = ":9080"
	}
	defaultStore := cfg.StoreFile
	if defaultStore == "" {
		defaultStore = config.DefaultConfig().StoreFile
	}

	// Apply global default obfuscation profile (this becomes the default for all config generation)
	if cfg.DefaultObfuscationProfile != "" {
		if _, ok := chain.GetPreset(cfg.DefaultObfuscationProfile); !ok {
			fmt.Fprintf(os.Stderr, "error: unknown obfuscation profile %q in config\n", cfg.DefaultObfuscationProfile)
			os.Exit(1)
		}
		chain.SetDefaultProfile(cfg.DefaultObfuscationProfile)
	}

	// Load extra presets if configured (after the default profile so they can reference/override)
	if cfg.PresetsFile != "" {
		loadExternalPresets(cfg.PresetsFile)
	}

	listen := fs.String("listen", defaultListen, "HTTP listen address")
	fs.StringVar(&storePath, "file", defaultStore, "store file path")
	devMode := fs.Bool("dev", false, "development mode: load UI from web/ instead of embedded")
	// TLS: optional. When both cert and key are provided, the panel serves
	// HTTPS instead of plain HTTP — strongly recommended whenever the panel is
	// reachable beyond the loopback interface (the control plane carries SSH
	// private keys and can issue fleet-wide RCE via config pushes).
	tlsCert := fs.String("tls-cert", "", "path to TLS certificate (enables HTTPS when set together with --tls-key)")
	tlsKey := fs.String("tls-key", "", "path to TLS private key (enables HTTPS when set together with --tls-cert)")
	_ = fs.Parse(os.Args[2:])

	// Dev mode can also be enabled via environment variable
	if !*devMode {
		devEnv := strings.ToLower(os.Getenv("ANGRY_BOX_DEV"))
		*devMode = devEnv == "1" || devEnv == "true" || devEnv == "on"
	}

	store := chain.NewStore(storePath)
	sshclient.SetHostKeyManager(store)
	sshclient.SetKeyResolver(store)

	mux := http.NewServeMux()

	// Register HTMX Web UI (DaisyUI + templ + HTMX, community patterns from Pagoda/TemplUI).
	// Composition root: create the factory once here and inject it into the UI
	// server (and the auto-apply background), so handlers don't call factory.New()
	// ad-hoc (CTO-review M11). The SSH connection POOL wraps the production
	// connector so a node re-deployed by auto-apply reuses its already-open
	// connection instead of re-dialing every ~5 min (CTO-review §8 pool
	// follow-up). The pool re-verifies liveness (keepalive) + stored known-host
	// fingerprint + key-resolution on each borrow; CLI commands (apply/node) use
	// the raw DefaultConnector directly (short-lived process — no pool benefit).
	sshPool := sshclient.NewPool(sshclient.DefaultConnector, store, store)
	orchFactory := factory.New(sshPool)
	ui := web.NewServer(storePath, *devMode, cfg, *listen, orchFactory)
	ui.SetSSHConnector(sshPool)
	ui.Register(mux)

	// Wire background auto-apply (hybrid deploy mode) with the same factory the
	// CLI uses, so per-user mutations can trigger SSH deploys in the background.
	// The pool is shared so background deploys reuse the same connections as web
	// deploys to the same node.
	chain.InitAutoApply(orchFactory, sshPool, storePath)

	// Start background metrics collection based on panel settings
	settings, _ := chain.NewStore(storePath).GetSettings()
	ui.StartBackgroundMetrics(settings.MetricsInterval)
	// Apply the operator's global default REALITY/TUIC SNI (if set) so
	// ResolveServerName + the standalone singbox renderers use it as the
	// fallback instead of the built-in const (CTO-review §2 Reality SNI drift).
	chain.SetDefaultSNI(settings.DefaultRealitySNI)

	// Existing API routes
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		s := chain.NewStore(storePath)
		hosts, _ := s.ListHosts()
		chains, _ := s.ListChains()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"hosts":  hosts,
			"chains": chains,
		})
	})

	scheme := "http"
	useTLS := *tlsCert != "" && *tlsKey != ""
	if useTLS {
		scheme = "https"
	} else if isLoopbackListen(*listen) {
		// Plain HTTP is acceptable on loopback (no network exposure).
	} else {
		// The panel is bound to a non-loopback address without TLS: warn loudly.
		// Basic-Auth credentials and all responses (including SSH private keys
		// and VPN secrets rendered in the UI) travel in cleartext and can be
		// passively sniffed by anyone on the path (Wi-Fi/LAN/VPC/ISP).
		fmt.Println("WARNING: serving plain HTTP on a non-loopback address.")
		fmt.Println("WARNING: Basic-Auth credentials and panel secrets are sent in cleartext.")
		fmt.Println("WARNING: use --tls-cert/--tls-key or a TLS-terminating reverse proxy,")
		fmt.Println("WARNING: or bind to loopback with --listen 127.0.0.1:9080.")
	}

	fmt.Printf("angry-box %s daemon listening on %s\n", version, *listen)
	listenHost := *listen
	if strings.HasPrefix(listenHost, ":") {
		listenHost = "localhost" + listenHost
	} else if strings.HasPrefix(listenHost, "0.0.0.0:") {
		listenHost = "localhost" + strings.TrimPrefix(listenHost, "0.0.0.0")
	}
	fmt.Printf("Web UI available at %s://%s/ui\n", scheme, listenHost)

	// Wrap the mux in CSRF protection for all state-changing requests. The
	// panel uses HTTP Basic Auth, whose credentials are not protected by
	// cookie SameSite attributes, so cross-origin POSTs from a malicious page
	// can otherwise be submitted in an admin's session (e.g. the historical
	// /ui/settings "auth_enabled" toggle that opened the whole panel).
	handler := web.CSRSMiddleware(mux)
	srv := &http.Server{
		Addr: *listen,
		Handler: handler,
		// Timeouts protect against Slowloris-style slow-read attacks and
		// unbounded keep-alive connections. ReadHeaderTimeout is the tight
		// Slowloris guard; ReadTimeout/WriteTimeout are generous because
		// ApplyChain / ApplyMergedNode blocks the request for the whole SSH
		// deploy (probe floor ~7-9s/node, multi-node chains run to minutes).
		// CTO-review §3/§7 finding: previously no timeouts at all.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown: on SIGINT/SIGTERM drain the HTTP server, stop the
	// background metrics collector, and wait for in-flight background SSH
	// deploys to finish instead of killing them mid-deploy (CTO-review H7).
	go func() {
		var serveErr error
		if useTLS {
			serveErr = srv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			serveErr = srv.ListenAndServe()
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "serve: %v\n", serveErr)
			os.Exit(1)
		}
	}()

	// Graceful shutdown: stop HTTP + metrics, wait for in-flight background
	// deploys to finish (so they're not cut off mid-SSH), then close the SSH
	// connection pool (tears down the idle cached connections). The pool MUST
	// close after WaitAutoApply — otherwise a still-running background deploy
	// would have its pooled connection closed under it.
	gracefulShutdown(srv, ui.Stop, func() {
		chain.WaitAutoApply()
		sshPool.Close()
	}, installSignalHandler())
}

// isLoopbackListen reports whether the listen address binds only to the
// loopback interface (e.g. "127.0.0.1:9080" or "[::1]:9080"). An empty host
// (":9080") or "0.0.0.0:..." binds to all interfaces and is NOT loopback.
func isLoopbackListen(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return false
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func requireHostFlags() {
	requireVal(addr, "addr")
	requireVal(keyPath, "key")
}

func requireVal(val, name string) {
	if val == "" {
		fmt.Fprintf(os.Stderr, "error: -%s is required\n", name)
		os.Exit(1)
	}
}

func parseConfigType(s string) model.ConfigType {
	switch s {
	case "user":
		return model.ConfigUser
	default:
		return model.ConfigTransport
	}
}

// isAWGUserConfig returns true if the current effective preset has AWG settings
// (used for standalone `config -type user` to decide whether to print client example).
func isAWGUserConfig() bool {
	p := chain.GetDefaultPreset()
	return p.AWG != nil
}

// printAWGClientExample prints guidance + a template for AmneziaWG client.
// The critical piece the user needs from the *server* is its public key (printed by apply-chain or by inspecting the generated server config).
func printAWGClientExample(providedClientPub, sampleClientPriv string) {
	fmt.Println("\n# === AWG / AmneziaWG Client Config ===")

	if sampleClientPriv != "" {
		fmt.Println("# Auto-generated sample client keypair for quick testing (same behavior as apply-chain).")
		fmt.Println("# The SERVER_PUBLIC_KEY must be derived from the 'private_key' field in the JSON config printed above.")
		fmt.Printf(`
[Interface]
PrivateKey = %s
Address = 10.8.0.2/32
MTU = 1420

[Peer]
PublicKey = <SERVER_PUBLIC_FROM_THE_JSON_YOU_JUST_GOT>
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = YOUR_ENTRY_NODE_PUBLIC_IP:8443
PersistentKeepalive = 25
`, sampleClientPriv)
		fmt.Println("# Paste the correct Server PublicKey and this config should work immediately with the profile's amnezia params.")
	} else if providedClientPub != "" {
		fmt.Printf("# Used provided --client-pubkey=%s (server config above allows this peer).\n", providedClientPub)
		fmt.Println("# You still need the matching SERVER_PUBLIC_KEY from the generated server private_key.")
	} else {
		fmt.Println("# Generated without client key (may contain placeholder — prefer supplying --client-pubkey or using apply-chain for AWG).")
	}

	fmt.Println("# amnezia parameters (jc/jmin/jmax/h1-h4) come from the active profile — must match server exactly.")

	p := chain.GetDefaultPreset()
	if p.CPSLevel >= 2 || (p.AWG != nil && p.AWG.CPSLevel >= 2) {
		fmt.Println("#")
		fmt.Println("# IMPORTANT (pro_2026 / xhttp_max_stealth_2026):")
		fmt.Println("# This preset uses advanced CPS (I1-I5). The I1-I5 values are embedded in the server config above.")
		fmt.Println("# For the client to work, the AmneziaWG client must receive matching I1-I5 (usually via the 'amnezia' section or extra params).")
		fmt.Println("# When using apply-chain, the report already contains the exact material. For standalone config, re-apply via chain for full CPS output.")
	}

	fmt.Println("# ============================================================")
}

// popFirstArg extracts the first non-flag argument and returns it along with
// the remaining args. Returns ("", args) if no positional arg is found.
func popFirstArg(args []string) (first string, rest []string) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a, append(args[:i], args[i+1:]...)
		}
	}
	return "", args
}

// ─── backup / restore / relocate ─────────────────────────────────────────────

// backupCmd handles `angry-box backup store` and `angry-box backup node <id>`.
// Both write a JSON backup to -o <file> (or stdout when -o is "-"/empty). The
// store backup is the whole panel (hosts, chains, transit keys, users,
// settings); the node backup is one node's portable identity (Host + NodeInfo
// + chain memberships with transit material).
func backupCmd() {
	sub, rest := popFirstArg(os.Args[2:])
	fs := flag.NewFlagSet("backup "+sub, flag.ExitOnError)
	fs.StringVar(&storePath, "file", defaultStorePath, "store file path")
	out := fs.String("o", "", "output file (default: stdout)")
	_ = fs.Parse(rest)

	st := chain.NewStore(storePath)
	var data []byte
	var err error
	switch sub {
	case "store":
		data, err = st.ExportStore()
		if err != nil {
			fmt.Fprintf(os.Stderr, "backup store failed: %v\n", err)
			os.Exit(1)
		}
	case "node":
		id, _ := popFirstArg(fs.Args())
		if id == "" {
			fmt.Fprintf(os.Stderr, "usage: angry-box backup node <id> [-o file]\n")
			os.Exit(1)
		}
		b, err := st.ExportNode(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "backup node %q failed: %v\n", id, err)
			os.Exit(1)
		}
		data, err = json.MarshalIndent(b, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "backup node marshal failed: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: angry-box backup store|node <id> [-o file]\n")
		os.Exit(1)
	}
	if *out == "" || *out == "-" {
		os.Stdout.Write(data)
		return
	}
	if err := os.WriteFile(*out, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "backup written to %s (%d bytes)\n", *out, len(data))
}

// restoreCmd handles `angry-box restore <file> [--force]`. It auto-detects
// whether the file is a store or node backup (via the envelope) and calls the
// matching Store importer. force overwrites a non-empty store / reroutes a
// live node.
func restoreCmd() {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	fs.StringVar(&storePath, "file", defaultStorePath, "store file path")
	force := fs.Bool("force", false, "overwrite a non-empty store / reroute a live node")
	_ = fs.Parse(os.Args[2:])
	args := fs.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: angry-box restore <backup-file> [--force]\n")
		os.Exit(1)
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", args[0], err)
		os.Exit(1)
	}
	st := chain.NewStore(storePath)
	format, err := chain.DetectBackupFormat(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: not an angry-box backup: %v\n", err)
		os.Exit(1)
	}
	switch format {
	case chain.BackupFormatStore:
		if err := st.ImportStore(data, *force); err != nil {
			fmt.Fprintf(os.Stderr, "restore store failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("store restored from %s\n", args[0])
	case chain.BackupFormatNode:
		var b chain.NodeBackup
		if err := json.Unmarshal(data, &b); err != nil {
			fmt.Fprintf(os.Stderr, "restore node: parse: %v\n", err)
			os.Exit(1)
		}
		if err := st.ImportNode(&b, *force); err != nil {
			// Skipped-missing-chains is a warning, not a fatal error: the node
			// itself was restored. Print the message but exit 0 so a script
			// importing many node backups is not aborted by one missing chain.
			fmt.Fprintf(os.Stderr, "restore node: %v\n", err)
		}
		fmt.Printf("node %q restored from %s\n", b.Node.ID, args[0])
	default:
		fmt.Fprintf(os.Stderr, "restore: unknown backup format %q\n", format)
		os.Exit(1)
	}
}

// relocateCmd is the CLI entry point for node relocation (Stage 3D wires the
// full RelocateNode flow; this stub parses flags so `angry-box relocate --help`
// works before the backend lands). It is filled in at Stage 3D.
func relocateCmd() {
	fs := flag.NewFlagSet("relocate", flag.ExitOnError)
	fs.StringVar(&storePath, "file", defaultStorePath, "store file path")
	addr := fs.String("addr", "", "new SSH address (IP:port) for the node")
	user := fs.String("user", "", "new SSH user (optional, keeps current if empty)")
	keyID := fs.String("key", "", "new SSH key registry id (optional, keeps current if empty)")
	_ = fs.Parse(os.Args[2:])
	id, _ := popFirstArg(fs.Args())
	if id == "" || *addr == "" {
		fmt.Fprintf(os.Stderr, "usage: angry-box relocate <node-id> --addr <new-ip:port> [--user <user>] [--key <key-id>]\n")
		os.Exit(1)
	}
	_ = user
	_ = keyID
	fmt.Fprintf(os.Stderr, "relocate: backend not yet wired (Stage 3D)\n")
	os.Exit(1)
}
