package web

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// ─── Panel relay (ssh -R through the spinal-cord nodes) ─────────────────────
// The orchestrator often runs behind NAT (home VPS, Keenetic router), while
// the nodes are public. For every node with the caddy utility + PanelRelay
// opt-in, the orchestrator keeps a persistent remote port-forward: the node
// listens on 127.0.0.1:PanelRelayPort and bridges to the panel's local
// listener. Caddy routes https://panel.<TLSDomain> there, so the operator
// reaches the ONE real panel (the store's source of truth) from anywhere.
//
// The relay is transport only — auth still happens at the panel's Basic-Auth
// gate. A plain-HTTP panel being relayed publicly is warned about loudly.

// remoteForwarder is implemented by the concrete SSH client (and fakes in
// tests). The ports.SSHClient interface stays minimal; the relay type-asserts.
type remoteForwarder interface {
	RemoteForward(ctx context.Context, remoteAddr, localAddr string) error
}

var (
	relayMu sync.Mutex
	relays  = map[string]context.CancelFunc{} // nodeID -> cancel
)

// StartPanelRelays boots a relay loop for every eligible node (caddy utility
// installed + PanelRelay opt-in). Called once at serve startup.
func (s *Server) StartPanelRelays() {
	infos, _ := s.store().ListNodeInfos()
	for _, info := range infos {
		if info != nil && info.UtilityInstalled(model.UtilityCaddy) && info.PanelRelay {
			s.StartNodeRelay(info.ID)
		}
	}
}

// StartNodeRelay starts (or keeps) the relay loop for one node.
func (s *Server) StartNodeRelay(nodeID string) {
	relayMu.Lock()
	defer relayMu.Unlock()
	if _, ok := relays[nodeID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	relays[nodeID] = cancel
	go s.relayLoop(ctx, nodeID)
}

// StopNodeRelay cancels one node's relay loop.
func (s *Server) StopNodeRelay(nodeID string) {
	relayMu.Lock()
	cancel, ok := relays[nodeID]
	if ok {
		delete(relays, nodeID)
	}
	relayMu.Unlock()
	if ok {
		cancel()
	}
}

// StopPanelRelays tears down every relay (graceful shutdown path).
func (s *Server) StopPanelRelays() {
	relayMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(relays))
	for id, cancel := range relays {
		cancels = append(cancels, cancel)
		delete(relays, id)
	}
	relayMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Server) relayLoop(ctx context.Context, nodeID string) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		err := s.runRelayOnce(ctx, nodeID)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("panel relay down, retrying", "node", nodeID, "err", err, "retry_in", backoff.String())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
		}
	}
}

func (s *Server) runRelayOnce(ctx context.Context, nodeID string) error {
	st := s.store()
	host, err := st.GetHost(nodeID)
	if err != nil {
		return err
	}
	info, _ := st.GetNodeInfo(nodeID)
	if info == nil || !info.PanelRelay || !info.UtilityInstalled(model.UtilityCaddy) {
		// Disabled mid-flight: stop retrying; the toggle re-starts the loop.
		s.StopNodeRelay(nodeID)
		return nil
	}
	resolved := chain.ResolveHostKey(st, host)
	client, err := s.SSHConnector().Connect(resolved.Addr, resolved.User, resolved.KeyPath)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()
	fwd, ok := client.(remoteForwarder)
	if !ok {
		return fmt.Errorf("connector does not support remote forwarding")
	}
	remoteAddr := fmt.Sprintf("127.0.0.1:%d", chain.PanelRelayPort)
	localAddr := relayLocalAddr(s.ActiveListenAddr)
	slog.Info("panel relay up", "node", nodeID, "remote", remoteAddr, "local", localAddr)
	return fwd.RemoteForward(ctx, remoteAddr, localAddr)
}

// relayLocalAddr converts the panel's listen address into a dialable loopback
// target (0.0.0.0 / [::] are rewritten to 127.0.0.1; an empty value defaults
// to the standard panel port).
func relayLocalAddr(listen string) string {
	if listen == "" {
		return "127.0.0.1:9080"
	}
	host, port, ok := strings.Cut(listen, ":")
	if !ok || port == "" {
		return "127.0.0.1:9080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
