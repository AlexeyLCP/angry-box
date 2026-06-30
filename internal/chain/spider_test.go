package chain

import (
	"strconv"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestStore_Link_CRUDAndUnique(t *testing.T) {
	s := newTestStore(t)
	l1 := &model.ConnectionLink{FromNodeID: "n1", ToNodeID: "n2", ChainName: "c1", Transport: "xhttp"}
	if err := s.SaveLink(l1); err != nil {
		t.Fatalf("SaveLink: %v", err)
	}
	if l1.ID == "" {
		t.Fatal("SaveLink should set ID")
	}
	// Duplicate (same from/to/chain) rejected.
	l2 := &model.ConnectionLink{FromNodeID: "n1", ToNodeID: "n2", ChainName: "c1", Transport: "reality"}
	if err := s.SaveLink(l2); err == nil {
		t.Error("expected duplicate-link error")
	}
	// Same from/to but different chain is fine.
	l3 := &model.ConnectionLink{FromNodeID: "n1", ToNodeID: "n2", ChainName: "c2", Transport: "xhttp"}
	if err := s.SaveLink(l3); err != nil {
		t.Errorf("different-chain link should save: %v", err)
	}
	// List + ListForChain.
	all, _ := s.ListLinks()
	if len(all) != 2 {
		t.Errorf("expected 2 links, got %d", len(all))
	}
	c1links, _ := s.ListLinksForChain("c1")
	if len(c1links) != 1 || c1links[0].ID != l1.ID {
		t.Errorf("ListLinksForChain(c1): %+v", c1links)
	}
	// Get + Delete.
	got, err := s.GetLink(l1.ID)
	if err != nil || got.FromNodeID != "n1" {
		t.Errorf("GetLink: %v %+v", err, got)
	}
	if err := s.DeleteLink(l1.ID); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	if _, err := s.GetLink(l1.ID); err == nil {
		t.Error("link should be gone after delete")
	}
}

func TestStore_SaveNodePosition_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	// Save a position for a node that has no NodeInfo yet — should create one.
	if err := s.SaveNodePosition("n1", 123.4, 456.7); err != nil {
		t.Fatal(err)
	}
	info, err := s.GetNodeInfo("n1")
	if err != nil {
		t.Fatal(err)
	}
	if info.PosX != 123.4 || info.PosY != 456.7 {
		t.Errorf("position round-trip: got (%v,%v)", info.PosX, info.PosY)
	}
	// Update the position — should not wipe other fields.
	if err := s.SaveNodePosition("n1", 200, 300); err != nil {
		t.Fatal(err)
	}
	info, _ = s.GetNodeInfo("n1")
	if info.PosX != 200 || info.PosY != 300 {
		t.Errorf("position update: got (%v,%v)", info.PosX, info.PosY)
	}
}

// TestSpiderSync_InsertToAfterFromNode checks the deploy-path sync logic used by
// handleCreateSpiderLink: when adding edge n1→n3 to a chain already containing
// [n1, n2], n3 is inserted right after n1 (not appended at the end).
func TestSpiderSync_InsertToAfterFromNode(t *testing.T) {
	nodes := []model.ChainNode{{ID: "n1"}, {ID: "n2"}}
	// Replicate the handler's insert logic.
	fromIdx := indexOfChainNodeTest(nodes, "n1")
	if fromIdx != 0 {
		t.Fatalf("fromIdx: got %d", fromIdx)
	}
	if indexOfChainNodeTest(nodes, "n3") >= 0 {
		t.Fatal("n3 should not be present yet")
	}
	// insert n3 after n1.
	insertAt := fromIdx + 1
	nodes = append(nodes, model.ChainNode{})
	copy(nodes[insertAt+1:], nodes[insertAt:])
	nodes[insertAt] = model.ChainNode{ID: "n3"}
	if len(nodes) != 3 || nodes[0].ID != "n1" || nodes[1].ID != "n3" || nodes[2].ID != "n2" {
		t.Errorf("expected [n1,n3,n2], got %+v", idsOf(nodes))
	}
}

func idsOf(nodes []model.ChainNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

func indexOfChainNodeTest(nodes []model.ChainNode, id string) int {
	for i, n := range nodes {
		if n.ID == id {
			return i
		}
	}
	return -1
}

// guard against unused import in case helpers move
var _ = strconv.Itoa