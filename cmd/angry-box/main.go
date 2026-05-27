package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

const usage = `angry-box — lightweight proxy orchestrator for sing-box and xray.

Usage:
  angry-box <command> [options]

Commands:
  deploy     Install proxy backend on a remote host
  status     Show proxy status on a remote host
  config     Generate proxy config locally, print to stdout
  apply      Push config to a remote host and restart proxy
  remove     Remove proxy from a remote host
  reload     Gracefully reload proxy on a remote host

Common flags:
  -backend   Proxy backend: sing-box (default) or xray
  -addr      Remote host address (IP:port)
  -user      SSH user (default: root)
  -key       Path to SSH private key
  -port      Listen port for inbound (config/apply commands)
  -protocol  Protocol for inbound: VLESS, VMess, Trojan (default: VLESS)
  -type      Config type: transport or user (config/apply commands, default: transport)

Examples:
  angry-box deploy -addr 192.168.1.1 -key ~/.ssh/id_ed25519
  angry-box status -addr 192.168.1.1 -key ~/.ssh/id_ed25519
  angry-box config -port 443
  angry-box apply -addr 192.168.1.1 -key ~/.ssh/id_ed25519 -port 443
`

// CLI flags — package-level for access from helper functions.
var (
	backendStr string
	addr       string
	user       string
	keyPath    string
	port       int
	protocol   string
	configType string
)

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}

	cmd := os.Args[1]

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
		requireFlags("addr", "key")
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		result, err := b.Deploy(ctx, host)
		if err != nil {
			fmt.Fprintf(os.Stderr, "deploy failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("deploy: %s v%s — %s\n", b.Name(), result.Version, result.Message)

	case "status":
		requireFlags("addr", "key")
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
		requireFlags("addr", "key")
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
		requireFlags("addr", "key")
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		if err := b.Remove(ctx, host); err != nil {
			fmt.Fprintf(os.Stderr, "remove failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s removed from %s\n", b.Name(), host.Addr)

	case "reload":
		requireFlags("addr", "key")
		host := model.Host{Addr: addr, User: user, KeyPath: keyPath}
		if err := b.Reload(ctx, host); err != nil {
			fmt.Fprintf(os.Stderr, "reload failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s reloaded on %s\n", b.Name(), host.Addr)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", cmd, usage)
		os.Exit(1)
	}
}

func requireFlags(names ...string) {
	for _, name := range names {
		switch name {
		case "addr":
			if addr == "" {
				fmt.Fprintf(os.Stderr, "error: -addr is required\n")
				os.Exit(1)
			}
		case "key":
			if keyPath == "" {
				fmt.Fprintf(os.Stderr, "error: -key is required\n")
				os.Exit(1)
			}
		}
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
