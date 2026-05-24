package config

import (
	"flag"
	"os"
)

// Config holds all configuration for LucX Core.
type Config struct {
	ListenAddr    string
	DBPath        string
	JWTSecret     string
	ApplyChain    string // chain ID to apply on startup (CLI mode)
	SetupTest     bool   // create and apply a test 2-hop chain (CLI mode)
	AddServer     bool   // add a server interactively (CLI mode)
	ServerHost    string // server host for -add-server
	ServerUser    string // server username for -add-server (default: root)
	ServerName    string // server display name for -add-server
	ServerPort    int    // server SSH port for -add-server (default: 22)
}

// Parse reads configuration from command-line flags and returns a populated Config.
// Uses a local FlagSet to avoid conflicts with go test flags.
func Parse() *Config {
	cfg := &Config{}
	fs := flag.NewFlagSet("lucx-core", flag.ExitOnError)
	fs.StringVar(&cfg.ListenAddr, "listen", ":8744", "API listen address")
	fs.StringVar(&cfg.DBPath, "db", "./lucx.db", "SQLite database path")
	fs.StringVar(&cfg.JWTSecret, "jwt-secret", "", "JWT signing secret (generate if empty)")
	fs.StringVar(&cfg.ApplyChain, "apply-chain", "", "Apply chain by ID and print result (CLI mode)")
	fs.BoolVar(&cfg.SetupTest, "setup-test", false, "Create and apply a test 2-hop chain (CLI mode)")
	fs.BoolVar(&cfg.AddServer, "add-server", false, "Add a server to the database (CLI mode)")
	fs.StringVar(&cfg.ServerHost, "server-host", "", "Server hostname or IP for -add-server")
	fs.StringVar(&cfg.ServerUser, "server-user", "root", "SSH username for -add-server")
	fs.StringVar(&cfg.ServerName, "server-name", "", "Display name for -add-server")
	fs.IntVar(&cfg.ServerPort, "server-port", 22, "SSH port for -add-server")
	fs.Parse(os.Args[1:])
	return cfg
}
