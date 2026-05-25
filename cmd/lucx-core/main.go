package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"

	"github.com/alexeylcp/lucx-core/internal/api"
	"github.com/alexeylcp/lucx-core/internal/backend"
	_ "github.com/alexeylcp/lucx-core/internal/backend/xray" // register Xray backend
	"github.com/alexeylcp/lucx-core/internal/chain"
	"github.com/alexeylcp/lucx-core/internal/config"
	"github.com/alexeylcp/lucx-core/internal/ssh"
	"github.com/alexeylcp/lucx-core/internal/store"
	lucxweb "github.com/alexeylcp/lucx-core/web"
	"github.com/alexeylcp/lucx-core/internal/ws"
)

func main() {
	cfg := config.Parse()

	s, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer s.Close()

	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "lucx-dev-secret-change-me"
		log.Println("WARNING: using default JWT secret")
	}

	sshFactory := ssh.NewFactory(s)
	wsHub := ws.NewHub()
	engine := chain.NewEngineWithHub(s, func(serverID string) (*ssh.Client, error) {
		srv, err := s.GetServer(serverID)
		if err != nil {
			return nil, err
		}
		return sshFactory.Dial(srv)
	}, wsHub)

	// CLI mode: add-server
	if cfg.AddServer {
		runAddServer(s, cfg)
		return
	}

	// CLI mode: setup-test
	if cfg.SetupTest {
		runSetupTest(s, engine, sshFactory)
		return
	}

	// CLI mode: apply-chain
	if cfg.ApplyChain != "" {
		runApplyChain(s, engine, cfg.ApplyChain)
		return
	}

	// HTTP server mode
	handlers := &api.Handlers{
		Store:      s,
		Engine:     engine,
		SSHFactory: func(srv *store.Server) (*ssh.Client, error) { return sshFactory.Dial(srv) },
		JWTSecret:  cfg.JWTSecret,
		WSHub:      wsHub,
	}

	router := api.NewRouter(handlers)
	webHandler := lucxweb.Handler()
	combined := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
			router.ServeHTTP(w, r)
		} else {
			webHandler.ServeHTTP(w, r)
		}
	})
	log.Printf("LucX Core listening on %s", cfg.ListenAddr)
	go func() {
		if err := http.ListenAndServe(cfg.ListenAddr, combined); err != nil {
			log.Fatalf("http: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("LucX Core shutting down")
}

// runApplyChain applies a chain by ID and prints the result.
func runApplyChain(s *store.Store, engine *chain.Engine, chainID string) {
	c, err := s.GetChain(chainID)
	if err != nil {
		log.Fatalf("chain not found: %v", err)
	}
	log.Printf("Applying chain %q (%s) with %d nodes", c.Name, c.ID, len(c.Nodes))
	for i, n := range c.Nodes {
		srv, _ := s.GetServer(n.ServerID)
		host := "?"
		if srv != nil {
			host = srv.Host
		}
		log.Printf("  node %d: role=%s protocol=%s server=%s backend=%s",
			i, n.Role, n.Protocol, host, n.BackendType)
	}

	if err := engine.Apply(nil, c); err != nil {
		log.Fatalf("APPLY FAILED: %v", err)
	}

	log.Printf("Chain %q applied successfully", c.Name)

	// Generate client config
	if len(c.Nodes) > 0 {
		entryNode := c.Nodes[0] // client connects to entry
		srv, err := s.GetServer(entryNode.ServerID)
		if err != nil {
			log.Printf("  (cannot generate client config: server not found)")
			return
		}
		client, err := ssh.NewFactory(s).Dial(srv)
		if err != nil {
			log.Printf("  (cannot connect for client config: %v)", err)
			return
		}
		defer client.Close()

		be, err := getBackend(entryNode.BackendType)
		if err != nil {
			log.Printf("  (backend lookup: %v)", err)
			return
		}
		config, err := be.BuildClientConfig(nil, client, fmt.Sprintf("lucx-%s-entry", c.ID))
		if err != nil {
			log.Printf("  (client config: %v)", err)
			return
		}
		log.Printf("Client config: %s", config)
	}
}

// runSetupTest creates and applies a test 2-hop chain.
func runSetupTest(s *store.Store, engine *chain.Engine, sshFactory *ssh.Factory) {
	// Look for an existing server to use as both entry and exit
	servers, err := s.ListServers()
	if err != nil || len(servers) == 0 {
		log.Fatal("No servers configured. Add a server first via HTTP API or manual DB insert.")
	}

	// Use GetServer to include credential (ListServers omits it for security)
	srv, err := s.GetServer(servers[0].ID)
	if err != nil {
		log.Fatalf("Get server: %v", err)
	}
	log.Printf("Using server: %s (%s)", srv.Name, srv.Host)

	// Check SSH connectivity
	client, err := sshFactory.Dial(srv)
	if err != nil {
		log.Fatalf("SSH to %s failed: %v", srv.Host, err)
	}
	be, err := getBackend("xray")
	if err != nil {
		log.Fatalf("backend: %v", err)
	}
	status, err := be.Status(nil, client)
	client.Close()
	log.Printf("Xray status check: running=%v version=%q err=%v", status.Running, status.Version, err)
	if err != nil || !status.Running {
		log.Printf("WARNING: Xray status check failed, continuing anyway...")
	}

	// Create test chain: Entry (VLESS+Reality:443) + Exit (VLESS:8443)
	chainID := uuid.New().String()
	testChain := &store.Chain{
		ID:   chainID,
		Name: "Test 2-hop",
		Nodes: []store.ChainNode{
			{
				ChainID:     chainID,
				ServerID:    srv.ID,
				BackendType: "xray",
				Protocol:    "vless",
				Position:    0,
				Role:        "entry",
				InboundSpec: chain.MustJSON(chain.EntrySpec{
					Security:   "reality",
					RealityKey: "-Oc8RVINXPw_rkY6kX31QBOj4cRJT5Z5fcZo3LK772E",
					RealityPub: "j8BwtO99UFIeWX3aPSVDm2jbWTDby6OCp6Bly9OADEY",
					ServerName: "discord.com",
					Port:       443,
				}),
			},
			{
				ChainID:     chainID,
				ServerID:    srv.ID,
				BackendType: "xray",
				Protocol:    "vless",
				Position:    1,
				Role:        "exit",
				InboundSpec: chain.MustJSON(chain.HopSpec{
					Port: 8443,
				}),
			},
		},
	}
	testChain.Status = "draft"

	if err := s.CreateChain(testChain); err != nil {
		log.Fatalf("Create chain: %v", err)
	}
	log.Printf("Created test chain %q (%s)", testChain.Name, testChain.ID)

	// Apply it
	log.Println("Applying chain...")
	gotChain, _ := s.GetChain(chainID)
	if err := engine.Apply(nil, gotChain); err != nil {
		log.Fatalf("APPLY FAILED: %v", err)
	}

	log.Println("Chain applied successfully!")

	// Generate client config
	client, err = sshFactory.Dial(srv)
	if err != nil {
		log.Printf("Cannot connect for client config: %v", err)
		return
	}
	defer client.Close()

	config, err := be.BuildClientConfig(nil, client, fmt.Sprintf("lucx-%s-entry", chainID))
	if err != nil {
		log.Printf("Client config error: %v", err)
		return
	}
	log.Printf("\n===== CLIENT CONFIG =====\n%s\n==========================", config)
}

func runAddServer(s *store.Store, cfg *config.Config) {
	if cfg.ServerHost == "" {
		log.Fatal("-server-host is required for -add-server")
	}
	var cred string
	if cfg.ServerKeyFile != "" {
		data, err := os.ReadFile(cfg.ServerKeyFile)
		if err != nil {
			log.Fatalf("read key file: %v", err)
		}
		cred = string(data)
	} else {
		cred = os.Getenv("LUCX_SERVER_PASS")
		if cred == "" {
			log.Fatal("LUCX_SERVER_PASS env var or -server-key-file is required")
		}
	}
	name := cfg.ServerName
	if name == "" {
		name = cfg.ServerHost
	}

	srv := &store.Server{
		ID:         uuid.New().String(),
		Name:       name,
		Host:       cfg.ServerHost,
		Port:       cfg.ServerPort,
		Username:   cfg.ServerUser,
		AuthMethod: cfg.ServerAuth,
		Credential: cred,
		Status:     "unknown",
		Source:     "fresh",
	}
	if err := s.CreateServer(srv); err != nil {
		log.Fatalf("Add server: %v", err)
	}
	log.Printf("Server added: id=%s name=%s host=%s:%d user=%s", srv.ID, srv.Name, srv.Host, srv.Port, srv.Username)
}

func getBackend(bt string) (backend.ProxyBackend, error) {
	return backend.Get(backend.BackendType(bt))
}
