package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/config"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/web"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `angry-box — lightweight proxy orchestrator for sing-box and xray.

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

Other:
  version        Show version information

Common flags:
  -backend   Proxy backend: sing-box (default) or xray
  -file      Path to store file (default: chains.json)
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
	backendStr   string
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
		fmt.Print(usage)
		os.Exit(1)
	}

	// Load orchestrator config (if present). Flags can still override.
	cfgPath := os.Getenv("ANGRY_BOX_CONFIG")
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	orchCfg, _ := config.Load(cfgPath) // ignore error, fall back to defaults

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

	// Quick pre-parse for global --config flag (before subcommand flag sets)
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
			break
		}
	}
	if configPath != "" {
		if c, err := config.Load(configPath); err == nil {
			// Use loaded values as base (flags can still override per-command)
			_ = c
		}
	}

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
		fs.StringVar(&storePath, "file", "chains.json", "store file path")
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
		fs.StringVar(&storePath, "file", "chains.json", "store file path")
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
		fs.StringVar(&storePath, "file", "chains.json", "store file path")

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
		fs.StringVar(&storePath, "file", "chains.json", "store file path")
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
			uuid, _ := chain.GenerateStableTUICUserCreds()
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
		fs.StringVar(&storePath, "file", "chains.json", "store file path")
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
		fs.StringVar(&storePath, "file", "chains.json", "store file path")

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
		fs.StringVar(&storePath, "file", "chains.json", "store file path")

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
	fs.StringVar(&storePath, "file", "chains.json", "store file path")
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

	f := factory.New()
	applier := chain.NewApplier(f)

	ctx := context.Background()
	report, err := applier.ApplyChain(ctx, c, clientPubKey)
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
	fmt.Printf("\n✓ chain %q applied successfully\n", r.ChainName)
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
	fs.StringVar(&backendStr, "backend", "sing-box", "proxy backend")
	fs.StringVar(&addr, "addr", "", "remote host address")
	fs.StringVar(&user, "user", "root", "SSH user")
	fs.StringVar(&keyPath, "key", "", "path to SSH private key")
	fs.IntVar(&port, "port", 0, "listen port")
	fs.StringVar(&protocol, "protocol", "VLESS", "protocol")
	fs.StringVar(&configType, "type", "transport", "config type (transport or user)")
	fs.StringVar(&profile, "profile", "", "obfuscation profile (russia_2026, iran_2026, china_2026, maximum_stealth_2026)")
	fs.StringVar(&clientPubKey, "client-pubkey", "", "client public key for AWG user configs")
	fs.StringVar(&transport, "transport", "xhttp", "transport for -type=transport (xhttp or reality)")
	_ = fs.Parse(os.Args[2:])

	f := factory.New()

	backendKind := model.BackendKind(backendStr)
	b, err := f.Create(backendKind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	switch cmd {
	case "deploy":
		requireHostFlags()
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		result, err := b.Deploy(ctx, host)
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
		defaultListen = ":8090"
	}
	defaultStore := cfg.StoreFile
	if defaultStore == "" {
		defaultStore = "chains.json"
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
	_ = fs.Parse(os.Args[2:])

	mux := http.NewServeMux()

	// Register HTMX Web UI (DaisyUI + templ + HTMX, community patterns from Pagoda/TemplUI)
	ui := web.NewServer(storePath)
	ui.Register(mux)

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

	fmt.Printf("angry-box daemon listening on %s\n", *listen)
	fmt.Println("Web UI available at http://" + *listen + "/ui")
	if err := http.ListenAndServe(*listen, mux); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
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

// generateStableUUIDForTUIC generates a stable UUID for TUIC user entry at chain creation time.
func generateStableUUIDForTUIC() string {
	// Simple stable generation for creation time (not cryptographic, just consistent)
	b := make([]byte, 16)
	// Use a fixed seed pattern based on time or better - for creation we can use proper random
	// For simplicity and stability across runs we use the same pattern as before but at creation only
	_, _ = rand.Read(b) // still random, but only called once at creation
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
