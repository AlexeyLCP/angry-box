package chain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestBuildMergedNodeConfig_Success(t *testing.T) {
	// 1. Setup node info with a standalone inbound on port 1080
	nodeInfo := &model.NodeInfo{
		Host: model.Host{
			ID:   "node-1",
			Addr: "1.2.3.4:22",
			User: "root",
		},
		Inbounds: []model.NodeInbound{
			{
				Protocol:      "vless-reality",
				Port:          1080,
				UUID:          "11111111-1111-1111-1111-111111111111",
				ServerPrivKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA",
				ServerPubKey:  "pubkey-standalone",
				ShortID:       "0123456789abcdef",
			},
		},
	}

	// 2. Setup 2 chains where this node participates:
	// Chain A: node-1 is the Entry node (index 0), TUIC user-in on port 8443
	chainA := &model.Chain{
		Name:         "chain-A",
		Strategy:     model.StrategyURLTest,
		Transport:    model.TransportReality,
		UserProtocol: model.UserProtocolTUIC,
		TUICEntryUserUUID:     "22222222-2222-2222-2222-222222222222",
		TUICEntryUserPassword: "tuic-password",
		Nodes: []model.ChainNode{
			{ID: "node-1", Addr: "1.2.3.4:22", Port: 8443, TransitUUID: "uuid-a1", TransitPrivKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA"},
			{ID: "node-2", Addr: "5.6.7.8:22", Port: 443, TransitUUID: "uuid-a2", TransitPrivKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA"},
		},
	}

	// Chain B: node-1 is a Transit/Transit node (index 1), VLESS-Reality transit-in on port 443
	chainB := &model.Chain{
		Name:         "chain-B",
		Strategy:     model.StrategyURLTest,
		Transport:    model.TransportReality,
		UserProtocol: model.UserProtocolVLESSReality,
		Nodes: []model.ChainNode{
			{ID: "node-3", Addr: "9.10.11.12:22", Port: 8443, TransitUUID: "uuid-b1", TransitPrivKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA"},
			{ID: "node-1", Addr: "1.2.3.4:22", Port: 443, TransitUUID: "uuid-b2", TransitPrivKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA"},
		},
	}

	nodeChains := []*model.Chain{chainA, chainB}

	// 3. Build merged config
	cfg, report, err := buildMergedNodeConfig(nodeInfo, nodeChains, nil, nil)
	if err != nil {
		t.Fatalf("Failed to build merged config: %v", err)
	}

	if report.StandaloneCount != 1 {
		t.Errorf("Expected 1 standalone inbound, got %d", report.StandaloneCount)
	}
	if len(report.ChainsIncluded) != 2 {
		t.Errorf("Expected 2 chains included, got %d", len(report.ChainsIncluded))
	}

	// Check if inbounds contain all three expected ports: 1080 (standalone), 8443 (entry chain A), 443 (transit chain B)
	foundPorts := make(map[int]bool)
	for _, inbRaw := range cfg.Inbounds {
		var inb struct {
			Port int `json:"listen_port"`
		}
		json.Unmarshal(inbRaw, &inb)
		if inb.Port > 0 {
			foundPorts[inb.Port] = true
		}
	}

	// For AWG it might be an endpoint and endpoints are mapped to Ports too
	if len(cfg.Endpoints) > 0 {
		// TUIC or VLESS will use listen_port. Let's verify our specific ports:
		// 1080 is standalone reality (listen_port)
		// 8443 is chainA tuic (listen_port)
		// 443 is chainB reality (listen_port)
		if !foundPorts[1080] {
			t.Errorf("Port 1080 (standalone) not found in generated inbounds")
		}
		if !foundPorts[8443] {
			t.Errorf("Port 8443 (chain A entry) not found in generated inbounds")
		}
		if !foundPorts[443] {
			t.Errorf("Port 443 (chain B transit) not found in generated inbounds")
		}
	}
}

func TestBuildMergedNodeConfig_Conflict(t *testing.T) {
	// Setup node info with a standalone inbound on port 443
	nodeInfo := &model.NodeInfo{
		Host: model.Host{
			ID:   "node-1",
			Addr: "1.2.3.4:22",
			User: "root",
		},
		Inbounds: []model.NodeInbound{
			{
				Protocol: "vless-reality",
				Port:     443, // CONFLICT PORT
				UUID:     "11111111-1111-1111-1111-111111111111",
			},
		},
	}

	// Setup a chain where this node is a transit node on port 443
	chainA := &model.Chain{
		Name:         "chain-A",
		Strategy:     model.StrategyURLTest,
		Transport:    model.TransportReality,
		UserProtocol: model.UserProtocolTUIC,
		Nodes: []model.ChainNode{
			{ID: "node-2", Addr: "5.6.7.8:22", Port: 8443},
			{ID: "node-1", Addr: "1.2.3.4:22", Port: 443}, // CONFLICT PORT
		},
	}

	nodeChains := []*model.Chain{chainA}

	// Build merged config -> Expect conflict error
	_, _, err := buildMergedNodeConfig(nodeInfo, nodeChains, nil, nil)
	if err == nil {
		t.Fatalf("Expected port conflict error, but got nil")
	}

	if !strings.Contains(err.Error(), "port 443 conflict") {
		t.Errorf("Expected error to mention 'port 443 conflict', got: %v", err)
	}
}

func TestDiffInboundTags(t *testing.T) {
	oldJSON := `{
		"inbounds": [
			{"tag": "sa-0-vless-reality"},
			{"tag": "ch-chain-A-user-in"}
		],
		"endpoints": [
			{"tag": "sa-1-awg"}
		]
	}`

	newJSON := `{
		"inbounds": [
			{"tag": "sa-0-vless-reality"},
			{"tag": "ch-chain-B-transport-in"}
		],
		"endpoints": [
			{"tag": "ch-chain-B-tun-in"}
		]
	}`

	added, removed := diffInboundTags(oldJSON, newJSON)

	// Expected added: "ch-chain-B-transport-in", "ch-chain-B-tun-in"
	// Expected removed: "ch-chain-A-user-in", "sa-1-awg"

	addedMap := make(map[string]bool)
	for _, a := range added {
		addedMap[a] = true
	}
	removedMap := make(map[string]bool)
	for _, r := range removed {
		removedMap[r] = true
	}

	if !addedMap["ch-chain-B-transport-in"] || !addedMap["ch-chain-B-tun-in"] || len(added) != 2 {
		t.Errorf("Unexpected added tags: %v", added)
	}
	if !removedMap["ch-chain-A-user-in"] || !removedMap["sa-1-awg"] || len(removed) != 2 {
		t.Errorf("Unexpected removed tags: %v", removed)
	}
}

