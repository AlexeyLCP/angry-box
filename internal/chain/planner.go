package chain

import (
	"encoding/json"
	"fmt"

	"github.com/alexeylcp/lucx-core/internal/backend"
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

func BuildPlan(chain *store.Chain) (*Plan, error) {
	plan := &Plan{}
	chainID := chain.ID
	batchMap := make(map[string]*ServerBatch)
	var serverOrder []string

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

		switch node.Role {
		case "entry":
			b, _ := json.Marshal(map[string]interface{}{
				"tag": fmt.Sprintf("lucx-%s-entry", chainID),
				"protocol": node.Protocol, "port": 443, "listen": "0.0.0.0",
			})
			batch.Inbounds = append(batch.Inbounds, b)
			if i+1 < len(chain.Nodes) {
				b, _ := json.Marshal(map[string]interface{}{
					"tag": fmt.Sprintf("lucx-%s-hop%d-to-%d", chainID, i, i+1),
					"protocol": node.Protocol,
				})
				batch.Outbounds = append(batch.Outbounds, b)
				batch.Routing = append(batch.Routing, backend.RoutingRule{
					Type: "field",
					InboundTag:  []string{fmt.Sprintf("lucx-%s-entry", chainID)},
					OutboundTag: fmt.Sprintf("lucx-%s-hop%d-to-%d", chainID, i, i+1),
				})
			}
		case "hop":
			b, _ := json.Marshal(map[string]interface{}{
				"tag": fmt.Sprintf("lucx-%s-hop%d", chainID, i),
				"protocol": node.Protocol, "port": 443, "listen": "0.0.0.0",
			})
			batch.Inbounds = append(batch.Inbounds, b)
			if i+1 < len(chain.Nodes) {
				b, _ := json.Marshal(map[string]interface{}{
					"tag": fmt.Sprintf("lucx-%s-hop%d-to-%d", chainID, i, i+1),
					"protocol": node.Protocol,
				})
				batch.Outbounds = append(batch.Outbounds, b)
				batch.Routing = append(batch.Routing, backend.RoutingRule{
					Type: "field",
					InboundTag:  []string{fmt.Sprintf("lucx-%s-hop%d", chainID, i)},
					OutboundTag: fmt.Sprintf("lucx-%s-hop%d-to-%d", chainID, i, i+1),
				})
			}
		case "exit":
			b, _ := json.Marshal(map[string]interface{}{
				"tag": fmt.Sprintf("lucx-%s-exit", chainID),
				"protocol": node.Protocol, "port": 443, "listen": "0.0.0.0",
			})
			batch.Inbounds = append(batch.Inbounds, b)
		}
	}

	for _, serverID := range serverOrder {
		plan.Batches = append(plan.Batches, *batchMap[serverID])
	}
	return plan, nil
}
