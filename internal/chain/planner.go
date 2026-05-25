package chain

import (
	"crypto/rand"
	"encoding/json"
	"fmt"

	"github.com/alexeylcp/lucx-core/internal/backend"
	xraycfg "github.com/alexeylcp/lucx-core/internal/backend/xray/config"
	"github.com/alexeylcp/lucx-core/internal/store"
)

// ServerBatch collects all mutations for a single server.
// Applied in ONE config write + ONE restart.
type ServerBatch struct {
	ServerID  string
	Inbounds  []json.RawMessage
	Outbounds []json.RawMessage
	Routing   []backend.RoutingRule
	Backend   backend.ProxyBackend
}

type Plan struct {
	Batches []ServerBatch // one batch per server, ordered
}

// ServerLookup resolves a server ID to its connection details.
type ServerLookup func(serverID string) (*store.Server, error)

func BuildPlan(chain *store.Chain, lookup ServerLookup) (*Plan, error) {
	plan := &Plan{}
	chainID := chain.ID
	batchMap := make(map[string]*ServerBatch)
	var serverOrder []string

	defaultClientID := genClientID()

	getOrCreateBatch := func(serverID string, be backend.ProxyBackend) *ServerBatch {
		if b, ok := batchMap[serverID]; ok {
			return b
		}
		b := &ServerBatch{ServerID: serverID, Backend: be}
		batchMap[serverID] = b
		serverOrder = append(serverOrder, serverID)
		return b
	}

	for i, node := range chain.Nodes {
		be, err := backend.Get(backend.BackendType(node.BackendType))
		if err != nil {
			return nil, fmt.Errorf("node %d: %w", i, err)
		}
		batch := getOrCreateBatch(node.ServerID, be)

		entrySpec, _ := ParseEntrySpec(node.InboundSpec)
		if entrySpec.ClientID == "" {
			entrySpec.ClientID = defaultClientID
		}
		clientID := entrySpec.ClientID

		// Default transport parameters
		transport := entrySpec.Transport
		xhttpMode := entrySpec.XHTTPMode

		// For hop/exit, parse HopSpec for port/transport override
		if node.Role == "hop" || node.Role == "exit" {
			hop, _ := ParseHopSpec(node.HopInboundSpec)
			if hop.ClientID != "" {
				clientID = hop.ClientID
			}
			if hop.Port != 0 {
				entrySpec.Port = hop.Port
			}
			if hop.Transport != "" {
				transport = hop.Transport
			}
			if hop.XHTTPMode != "" {
				xhttpMode = hop.XHTTPMode
			}
		}

		// Determine next node's address/port for outbound.
		// OutboundSpec overrides (if set in this node's outbound_spec).
		outSpec, _ := ParseOutbound(node.OutboundSpec)
		nextHostOverride := outSpec.Address
		nextPortOverride := outSpec.Port

		nextPort := 443
		if nextPortOverride != 0 {
			nextPort = nextPortOverride
		} else if i+1 < len(chain.Nodes) {
			nextPort = getInboundPort(chain.Nodes[i+1])
		}

		nextHost := ""
		if nextHostOverride != "" {
			nextHost = nextHostOverride
		} else if i+1 < len(chain.Nodes) {
			nextSrv, err := lookup(chain.Nodes[i+1].ServerID)
			if err != nil {
				return nil, fmt.Errorf("node %d next server: %w", i, err)
			}
			nextHost = nextSrv.Host
		}

		switch node.Role {
		case "entry":
			batch.Inbounds = append(batch.Inbounds, buildEntry(node, chainID, entrySpec))
			if i+1 < len(chain.Nodes) && nextHost != "" {
				outTag := fmt.Sprintf("lucx-%s-hop%d-to-%d", chainID, i, i+1)
				batch.Outbounds = append(batch.Outbounds,
					xraycfg.VLESSOutbound(outTag, nextHost, nextPort, clientID, transport, xhttpMode))
				batch.Routing = append(batch.Routing, backend.RoutingRule{
					Type:        "field",
					InboundTag:  []string{fmt.Sprintf("lucx-%s-entry", chainID)},
					OutboundTag: outTag,
				})
			}

		case "hop":
			inTag := fmt.Sprintf("lucx-%s-hop%d", chainID, i)
			batch.Inbounds = append(batch.Inbounds, xraycfg.VLESSHop(inTag, clientID, entrySpec.Port, transport, xhttpMode))
			if i+1 < len(chain.Nodes) && nextHost != "" {
				outTag := fmt.Sprintf("lucx-%s-hop%d-to-%d", chainID, i, i+1)
				batch.Outbounds = append(batch.Outbounds,
					xraycfg.VLESSOutbound(outTag, nextHost, nextPort, clientID, transport, xhttpMode))
				batch.Routing = append(batch.Routing, backend.RoutingRule{
					Type:        "field",
					InboundTag:  []string{inTag},
					OutboundTag: outTag,
				})
			}

		case "exit":
			inTag := fmt.Sprintf("lucx-%s-exit", chainID)
			batch.Inbounds = append(batch.Inbounds, xraycfg.VLESSHop(inTag, clientID, entrySpec.Port, transport, xhttpMode))
		}
	}

	for _, serverID := range serverOrder {
		plan.Batches = append(plan.Batches, *batchMap[serverID])
	}
	return plan, nil
}

func buildEntry(node store.ChainNode, chainID string, s EntrySpec) json.RawMessage {
	tag := fmt.Sprintf("lucx-%s-entry", chainID)
	switch {
	case node.Protocol == "trojan":
		return xraycfg.TrojanEntry(tag, s.Password, s.ServerName, s.Port, s.Transport, s.XHTTPHost, s.XHTTPPath, s.XHTTPMode, s.Fingerprint)
	case s.Security == "reality":
		return xraycfg.VLESSEntryReality(tag, s.ClientID, s.RealityKey, s.RealityPub, s.Port, s.Transport, s.XHTTPHost, s.XHTTPPath, s.XHTTPMode, s.Fingerprint)
	default:
		return xraycfg.VLESSEntryTLS(tag, s.ClientID, s.ServerName, s.Port, s.Transport, s.XHTTPHost, s.XHTTPPath, s.XHTTPMode, s.Fingerprint)
	}
}

// getInboundPort extracts the inbound port from a node's spec.
func getInboundPort(node store.ChainNode) int {
	switch node.Role {
	case "entry":
		s, _ := ParseEntrySpec(node.InboundSpec)
		if s.Port != 0 {
			return s.Port
		}
	case "hop", "exit":
		s, _ := ParseHopSpec(node.HopInboundSpec)
		if s.Port != 0 {
			return s.Port
		}
	}
	return 443
}

func genClientID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
