package main

import (
	"context"
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
  -port      Listen port for inbound
  -protocol  Protocol (default: VLESS)
  -type      Config type: transport or user (default: transport)

Examples:
  angry-box host add mynode --addr 192.168.1.1:22 --user root --key ~/.ssh/id_ed25519
  angry-box chain create mychain --nodes mynode1,mynode2,mynode3 --strategy urltest
  angry-box apply-chain mychain
  angry-box deploy -addr 192.168.1.1 -key ~/.ssh/id_ed25519
  angry-box config -port 443
`

// CLI flags.
var (
	backendStr string
	storePath  string
	addr       string
	user       string
	keyPath    string
	port       int
	protocol   string
	configType string
	nodesStr   string
	strategy   string

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

	// Apply config defaults for flags that weren't explicitly set on CLI
	// (simple approach: we let per-command flag sets override later)
	_ = orchCfg // will be used more in future iterations

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

		name, flagArgs := popFirstArg(os.Args[3:])
		_ = fs.Parse(flagArgs)

		if name == "" {
			fmt.Fprintf(os.Stderr, "usage: angry-box chain create <name> --nodes id1,id2,id3 [--strategy urltest]\n")
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

		c := &model.Chain{
			Name:     name,
			Nodes:    nodes,
			Strategy: model.Strategy(strategy),
		}

		if err := s.SaveChain(c); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("chain %q created with %d nodes (strategy: %s)\n", name, len(nodes), strategy)

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

	name, flagArgs := popFirstArg(os.Args[2:])
	_ = fs.Parse(flagArgs)

	if name == "" {
		fmt.Fprintf(os.Stderr, "usage: angry-box apply-chain <name>\n")
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

	fmt.Printf("applying chain %q (%d nodes, strategy: %s)...\n", c.Name, len(c.Nodes), c.Strategy)

	f := factory.New()
	applier := chain.NewApplier(f)

	ctx := context.Background()
	if err := applier.ApplyChain(ctx, c); err != nil {
		fmt.Fprintf(os.Stderr, "apply-chain failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("chain %q applied successfully\n", c.Name)
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
		cfg, err := b.GenerateConfig(ct, model.ConfigParams{
			Port:     port,
			Protocol: protocol,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "config generation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(cfg.Content)

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
