package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/chain"
	xraycfg "github.com/alexeylcp/lucx-core/internal/backend/xray/config"
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
			// Default hop inbound spec for all roles
			if c.Nodes[i].HopInboundSpec == "" || c.Nodes[i].HopInboundSpec == "{}" {
				c.Nodes[i].HopInboundSpec = chain.MustJSON(chain.DefaultHopInbound(0))
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

func (h *Handlers) handleUpdateChainNode() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		posStr := chi.URLParam(r, "pos")
		pos, err := strconv.Atoi(posStr)
		if err != nil {
			http.Error(w, `{"error":"invalid position"}`, 400)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var req struct {
			InboundSpec    string `json:"inbound_spec"`
			HopInboundSpec string `json:"hop_inbound_spec"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, 400)
			return
		}

		if err := h.Store.UpdateChainNode(id, pos, req.InboundSpec, req.HopInboundSpec); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Reset chain to draft on node change so re-apply triggers full pipeline
		h.Store.UpdateChainStatus(id, "draft")

		node, _ := h.Store.GetChainNode(id, pos)
		json.NewEncoder(w).Encode(node)
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
		if c.Status == "active" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"active","message":"already applied"}`))
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
		c, err := h.Store.GetChain(id)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}

		// Collect unique servers
		seen := make(map[string]bool)
		for _, n := range c.Nodes {
			seen[n.ServerID] = true
		}

		var errs []string
		for serverID := range seen {
			srv, lookupErr := h.Store.GetServer(serverID)
			if lookupErr != nil {
				log.Printf("[ROLLBACK] server %s not found, skipping", serverID)
				errs = append(errs, fmt.Sprintf("%s: not found", serverID))
				continue
			}
			client, sshErr := h.SSHFactory(srv)
			if sshErr != nil {
				log.Printf("[ROLLBACK] server %s SSH failed: %v", serverID, sshErr)
				errs = append(errs, fmt.Sprintf("%s: ssh failed", serverID))
				continue
			}
			backups, listErr := xraycfg.ListBackups(r.Context(), client)
			if listErr != nil || len(backups) == 0 {
				log.Printf("[ROLLBACK] server %s: no backups found", serverID)
				errs = append(errs, fmt.Sprintf("%s: no backups", serverID))
				client.Close()
				continue
			}
			// Latest backup (ls -t returns newest first) is the one created
			// just before this chain was applied.
			if restoreErr := xraycfg.NewManager(client).Rollback(r.Context(), backups[0]); restoreErr != nil {
				log.Printf("[ROLLBACK] server %s rollback failed: %v", serverID, restoreErr)
				errs = append(errs, fmt.Sprintf("%s: rollback failed", serverID))
			} else {
				log.Printf("[ROLLBACK] server %s rolled back from %s", serverID, backups[0])
			}
			client.Close()
		}

		if len(errs) > 0 {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"error","errors":%s}`, jsonStr(errs))
			return
		}

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
		entryNode := c.Nodes[0]
		srv, err := h.Store.GetServer(entryNode.ServerID)
		if err != nil {
			http.Error(w, `{"error":"entry server not found"}`, 404)
			return
		}
		client, err := h.SSHFactory(srv)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer client.Close()
		be, err := backend.Get(backend.BackendType(entryNode.BackendType))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		config, err := be.BuildClientConfig(r.Context(), client, fmt.Sprintf("lucx-%s-entry", id))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"config":"%s"}`, config)
	}
}

func jsonStr(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}
