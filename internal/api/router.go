package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/alexeylcp/lucx-core/internal/chain"
	"github.com/alexeylcp/lucx-core/internal/store"
	"github.com/alexeylcp/lucx-core/internal/ssh"
	"github.com/alexeylcp/lucx-core/internal/ws"
)

// Handlers holds shared dependencies for all HTTP handlers.
type Handlers struct {
	Store      *store.Store
	Engine     *chain.Engine
	SSHFactory func(srv *store.Server) (*ssh.Client, error)
	JWTSecret  string
	WSHub      *ws.Hub
}

// NewRouter creates a chi router with all routes registered.
func NewRouter(h *Handlers) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	r.Post("/api/v1/auth/login", h.handleLogin())

	r.Group(func(r chi.Router) {
		r.Use(h.authMiddleware())

		// Servers
		r.Get("/api/v1/servers", h.handleListServers())
		r.Post("/api/v1/servers", h.handleCreateServer())
		r.Get("/api/v1/servers/{id}", h.handleGetServer())
		r.Delete("/api/v1/servers/{id}", h.handleDeleteServer())
		r.Post("/api/v1/servers/{id}/scan", h.handleScanServer())
		r.Post("/api/v1/servers/{id}/install", h.handleInstallServer())
		r.Post("/api/v1/servers/{id}/import", h.handleImportServer())
		r.Get("/api/v1/servers/{id}/health", h.handleHealthCheck())
		r.Post("/api/v1/servers/{id}/x25519", h.handleGenerateKeys())

		// Chains
		r.Get("/api/v1/chains", h.handleListChains())
		r.Post("/api/v1/chains", h.handleCreateChain())
		r.Get("/api/v1/chains/{id}", h.handleGetChain())
		r.Delete("/api/v1/chains/{id}", h.handleDeleteChain())
		r.Put("/api/v1/chains/{id}/nodes/{pos}", h.handleUpdateChainNode())
		r.Post("/api/v1/chains/{id}/validate", h.handleValidateChain())
		r.Post("/api/v1/chains/{id}/apply", h.handleApplyChain())
		r.Post("/api/v1/chains/{id}/rollback", h.handleRollbackChain())
		r.Get("/api/v1/chains/{id}/config", h.handleGetChainConfig())

		// Tools
		r.Post("/api/v1/tools/x25519", h.handleGenerateKeysAny())

		// System
		r.Get("/api/v1/status", h.handleStatus())
		r.Get("/api/v1/logs", h.handleLogs())

		// Map
		r.Get("/api/v1/map", h.handleGetMap())

		// WebSocket
		r.Get("/api/v1/ws", h.handleWS())
	})

	return r
}
