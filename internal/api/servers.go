package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/health"
	"github.com/alexeylcp/lucx-core/internal/reality"
	"github.com/alexeylcp/lucx-core/internal/scanner"
	"github.com/alexeylcp/lucx-core/internal/store"
)

func (h *Handlers) handleListServers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		servers, _ := h.Store.ListServers()
		json.NewEncoder(w).Encode(servers)
	}
}

func (h *Handlers) handleCreateServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var raw struct {
			Name       string `json:"name"`
			Host       string `json:"host"`
			Port       int    `json:"port"`
			Username   string `json:"username"`
			AuthMethod string `json:"auth_method"`
			Credential string `json:"credential"`
		}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, `{"error":"invalid json"}`, 400)
			return
		}
		if raw.Port == 0 {
			raw.Port = 22
		}
		if raw.AuthMethod == "" {
			raw.AuthMethod = "password"
		}
		// Check for duplicate host:port
		if existingID, existingName, exists := h.Store.ServerExists(raw.Host, raw.Port); exists {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			fmt.Fprintf(w, `{"error":"server already registered","existing_id":"%s","existing_name":"%s","hint":"edit the existing server instead"}`, existingID, existingName)
			return
		}

		srv := store.Server{
			ID:         uuid.New().String(),
			Name:       raw.Name,
			Host:       raw.Host,
			Port:       raw.Port,
			Username:   raw.Username,
			AuthMethod: raw.AuthMethod,
			Credential: raw.Credential,
			Status:     "unknown",
			Source:     "fresh",
		}
		if err := h.Store.CreateServer(&srv); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(srv)
	}
}

func (h *Handlers) handleGetServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		srv, err := h.Store.GetServer(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		json.NewEncoder(w).Encode(srv)
	}
}

func (h *Handlers) handleDeleteServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		h.Store.DeleteServer(id)
		w.WriteHeader(204)
	}
}

func (h *Handlers) handleScanServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		srv, err := h.Store.GetServer(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		client, err := h.SSHFactory(srv)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer client.Close()
		check, err := scanner.PreInstallCheck(client)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(check)
	}
}

func (h *Handlers) handleInstallServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		srv, err := h.Store.GetServer(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		client, err := h.SSHFactory(srv)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"ssh: %s"}`, err.Error()), 500)
			return
		}
		defer client.Close()

		check, _ := scanner.PreInstallCheck(client)
		if !check.Safe && !check.CanInstall {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, check.Warning), 409)
			return
		}

		be, _ := backend.Get(backend.BackendXray)
		path, err := be.Install(r.Context(), client)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"install: %s"}`, err.Error()), 500)
			return
		}
		if err := be.Start(r.Context(), client); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"start: %s"}`, err.Error()), 500)
			return
		}
		status, _ := be.Status(r.Context(), client)

		h.Store.UpsertServerBackend(&store.ServerBackend{
			ServerID: id, BackendType: "xray", Version: status.Version,
			Status: "running", ConfigPath: path, ConfigManaged: true,
		})
		h.Store.UpdateServerStatus(id, "online")

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"installed","version":"%s","path":"%s"}`, status.Version, path)
	}
}

func (h *Handlers) handleImportServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		srv, err := h.Store.GetServer(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		client, err := h.SSHFactory(srv)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"ssh: %s"}`, err.Error()), 500)
			return
		}
		defer client.Close()

		cfg, err := scanner.ImportExisting(client)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"import: %s"}`, err.Error()), 500)
			return
		}

		h.Store.UpdateServerStatus(id, "imported")
		h.Store.UpsertServerBackend(&store.ServerBackend{
			ServerID: id, BackendType: "xray", Status: "imported",
			ConfigManaged: false,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
	}
}

func (h *Handlers) handleHealthCheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		srv, err := h.Store.GetServer(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		client, err := h.SSHFactory(srv)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"online":false,"error":"ssh: %s"}`, err.Error())
			return
		}
		defer client.Close()
		be, _ := backend.Get(backend.BackendXray)
		status := health.Check(r.Context(), client, be)
		json.NewEncoder(w).Encode(status)
	}
}

func (h *Handlers) handleGenerateKeys() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		srv, err := h.Store.GetServer(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		client, err := h.SSHFactory(srv)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"ssh: %s"}`, err.Error()), 500)
			return
		}
		defer client.Close()
		kp, err := reality.GenerateKeys(client)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		json.NewEncoder(w).Encode(kp)
	}
}

func (h *Handlers) handleGenerateKeysAny() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.URL.Query().Get("server_id")
		if serverID == "" {
			http.Error(w, `{"error":"server_id query parameter required"}`, 400)
			return
		}
		srv, err := h.Store.GetServer(serverID)
		if err != nil {
			http.Error(w, `{"error":"server not found"}`, 404)
			return
		}
		client, err := h.SSHFactory(srv)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"ssh: %s"}`, err.Error()), 500)
			return
		}
		defer client.Close()
		kp, err := reality.GenerateKeys(client)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		json.NewEncoder(w).Encode(kp)
	}
}
