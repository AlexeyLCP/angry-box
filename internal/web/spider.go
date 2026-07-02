package web

// spider.go — Spider Web visual chain editor handlers (extracted from ui.go as
// part of the M11 split).

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handleSpiderWeb(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	chains, _ := st.ListChains()
	infos, _ := st.ListNodeInfos()
	links, _ := st.ListLinks()
	s.renderContent(w, r, i18n.T(r.Context(), "Spider Web"), templates.SpiderWeb(hosts, chains, infos, links))
}

func (s *Server) handleCreateSpiderLink(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	fromNode := strings.TrimSpace(r.FormValue("from_node"))
	toNode := strings.TrimSpace(r.FormValue("to_node"))
	transport := strings.TrimSpace(r.FormValue("transport"))
	chainName := strings.TrimSpace(r.FormValue("chain_name"))

	if fromNode == "" || toNode == "" || chainName == "" {
		http.Error(w, i18n.T(r.Context(), "from_node, to_node, and chain_name are required"), http.StatusBadRequest)
		return
	}
	if fromNode == toNode {
		http.Error(w, i18n.T(r.Context(), "from_node and to_node must differ"), http.StatusBadRequest)
		return
	}
	if transport == "" {
		transport = "xhttp"
	}

	st := s.store()

	// Resolve hosts (validates both ends exist).
	fromHost, err := st.GetHost(fromNode)
	if err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "host %q not found"), fromNode), http.StatusBadRequest)
		return
	}
	toHost, err := st.GetHost(toNode)
	if err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "host %q not found"), toNode), http.StatusBadRequest)
		return
	}

	// 1. Persist the graph edge (source of truth for topology).
	link := &model.ConnectionLink{
		FromNodeID: fromNode,
		ToNodeID:   toNode,
		Transport:  model.TransportType(transport),
		ChainName:  chainName,
	}
	if err := st.SaveLink(link); err != nil {
		// Duplicate edge — not fatal, just continue to sync the chain list.
		_ = err
	}

	// 2. Sync Chain.Nodes (materialized deploy path): insert toNode right after
	// fromNode if not already present in the chain's ordered list.
	existing, err := st.GetChain(chainName)
	var nodes []model.ChainNode
	if err == nil {
		nodes = existing.Nodes
	} else {
		nodes = []model.ChainNode{}
	}

	fromCN := model.ChainNode{ID: fromHost.ID, Addr: fromHost.Addr, User: fromHost.User, KeyPath: fromHost.KeyPath}
	toCN := model.ChainNode{ID: toHost.ID, Addr: toHost.Addr, User: toHost.User, KeyPath: toHost.KeyPath}

	// Ensure fromNode is in the list.
	fromIdx := indexOfChainNode(nodes, fromNode)
	if fromIdx < 0 {
		nodes = append(nodes, fromCN)
		fromIdx = len(nodes) - 1
	}
	// Ensure toNode is present; if missing, insert right after fromNode so the
	// deploy order reflects the edge direction.
	if indexOfChainNode(nodes, toNode) < 0 {
		insertAt := fromIdx + 1
		if insertAt > len(nodes) {
			insertAt = len(nodes)
		}
		nodes = append(nodes, model.ChainNode{})
		copy(nodes[insertAt+1:], nodes[insertAt:])
		nodes[insertAt] = toCN
	}

	ch := &model.Chain{
		Name:      chainName,
		Nodes:     nodes,
		Strategy:  model.StrategyURLTest,
		Transport: model.TransportType(transport),
	}
	st.SaveChain(ch)
	chain.WriteAudit(st, "create", "link", link.ID, chain.AuditPayload{"from": fromNode, "to": toNode, "chain": chainName, "transport": transport}, "operator")

	// Re-render with full data from store (including links now).
	s.renderSpider(w, r, st)
}

func (s *Server) handleDeleteSpiderLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	link, err := st.GetLink(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "link not found"), http.StatusNotFound)
		return
	}
	chainName := link.ChainName
	fromNode := link.FromNodeID
	toNode := link.ToNodeID

	if err := st.DeleteLink(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	chain.WriteAudit(st, "delete", "link", id, chain.AuditPayload{"chain": chainName, "from": fromNode, "to": toNode}, "operator")

	// Sync Chain.Nodes: remove nodes that no longer have ANY edge in this chain.
	remainingLinks, _ := st.ListLinksForChain(chainName)
	used := map[string]bool{}
	for _, l := range remainingLinks {
		used[l.FromNodeID] = true
		used[l.ToNodeID] = true
	}
	c, err := st.GetChain(chainName)
	if err == nil {
		filtered := c.Nodes[:0]
		for _, n := range c.Nodes {
			if used[n.ID] {
				filtered = append(filtered, n)
			}
		}
		if len(filtered) == 0 {
			st.DeleteChain(chainName)
		} else {
			c.Nodes = filtered
			st.SaveChain(c)
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// handleSaveNodePosition persists a node's spider-web drag coordinates. Called
// on mouseup after dragging a node. Body params: x, y (float).
func (s *Server) handleSaveNodePosition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	x, _ := strconv.ParseFloat(r.FormValue("x"), 64)
	y, _ := strconv.ParseFloat(r.FormValue("y"), 64)
	if err := s.store().SaveNodePosition(id, x, y); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// renderSpider loads all spider data (hosts/chains/infos/links) and renders the
// SpiderWeb template. Shared by handleSpiderWeb and the link create/delete flows.
func (s *Server) renderSpider(w http.ResponseWriter, r *http.Request, st *chain.Store) {
	allHosts, _ := st.ListHosts()
	allChains, _ := st.ListChains()
	allInfos, _ := st.ListNodeInfos()
	allLinks, _ := st.ListLinks()
	s.render(w, r, templates.SpiderWeb(allHosts, allChains, allInfos, allLinks))
}

// indexOfChainNode returns the index of nodeID in nodes, or -1.
func indexOfChainNode(nodes []model.ChainNode, nodeID string) int {
	for i, n := range nodes {
		if n.ID == nodeID {
			return i
		}
	}
	return -1
}