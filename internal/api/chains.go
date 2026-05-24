package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/chain"
	"github.com/alexeylcp/lucx-core/internal/ssh"
	"github.com/alexeylcp/lucx-core/internal/store"
)

func (h *Handlers) handleListChains() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chains, _ := h.Store.ListChains()
		json.NewEncoder(w).Encode(chains)
	}
}

func (h *Handlers) handleCreateChain() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var c store.Chain
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			http.Error(w, `{"error":"invalid json"}`, 400)
			return
		}
		c.ID = uuid.New().String()
		c.Status = "draft"
		for i := range c.Nodes {
			c.Nodes[i].ChainID = c.ID
			c.Nodes[i].Position = i
			if i == 0 {
				c.Nodes[i].Role = "entry"
				if c.Nodes[i].InboundSpec == "" || c.Nodes[i].InboundSpec == "{}" {
					c.Nodes[i].InboundSpec = chain.MustJSON(chain.DefaultEntrySpec())
				}
			} else if i == len(c.Nodes)-1 {
				c.Nodes[i].Role = "exit"
			} else {
				c.Nodes[i].Role = "hop"
			}
		}
		if err := h.Store.CreateChain(&c); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(c)
	}
}

func (h *Handlers) handleGetChain() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		c, err := h.Store.GetChain(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		json.NewEncoder(w).Encode(c)
	}
}

func (h *Handlers) handleDeleteChain() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		h.Store.DeleteChain(id)
		w.WriteHeader(204)
	}
}

func (h *Handlers) handleValidateChain() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		c, err := h.Store.GetChain(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		plan, err := chain.BuildPlan(c, h.Store.GetServer)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"plan: %s"}`, err.Error()), 500)
			return
		}
		// chain.Validate expects SSHFactory(serverID string), adapt from
		// handlers.SSHFactory(srv *store.Server) via store lookup.
		sf := func(serverID string) (*ssh.Client, error) {
			srv, lookupErr := h.Store.GetServer(serverID)
			if lookupErr != nil {
				return nil, lookupErr
			}
			return h.SSHFactory(srv)
		}
		if err := chain.Validate(r.Context(), plan, sf); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(fmt.Sprintf(`{"valid":false,"error":"%s"}`, err.Error())))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"valid":true}`))
	}
}

func (h *Handlers) handleApplyChain() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		c, err := h.Store.GetChain(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		if err := h.Engine.Apply(r.Context(), c); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"active"}`))
	}
}

func (h *Handlers) handleRollbackChain() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := h.Store.UpdateChainStatus(id, "draft"); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"draft"}`))
	}
}

func (h *Handlers) handleGetChainConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		c, err := h.Store.GetChain(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		if len(c.Nodes) == 0 {
			http.Error(w, `{"error":"chain has no nodes"}`, 400)
			return
		}
		exitNode := c.Nodes[len(c.Nodes)-1]
		srv, err := h.Store.GetServer(exitNode.ServerID)
		if err != nil {
			http.Error(w, `{"error":"exit server not found"}`, 404)
			return
		}
		client, err := h.SSHFactory(srv)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer client.Close()
		be, err := backend.Get(backend.BackendType(exitNode.BackendType))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		config, err := be.BuildClientConfig(r.Context(), client, fmt.Sprintf("lucx-%s-exit", id))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"config":"%s"}`, config)
	}
}
