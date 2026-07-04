package chain

import (
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestEnsureAWGExitLinks_GeneratesPersistentMaterial(t *testing.T) {
	c := &model.Chain{
		Name: "mx",
		Nodes: []model.ChainNode{
			{ID: "bal", Role: model.NodeRoleEntry, ExitTargets: []string{"exit1", "exit2"}},
			{ID: "exit1", Role: model.NodeRoleExit},
			{ID: "exit2", Role: model.NodeRoleExit},
		},
	}
	if err := ensureAWGExitLinks(c, &c.Nodes[0]); err != nil {
		t.Fatalf("ensureAWGExitLinks: %v", err)
	}
	bal := &c.Nodes[0]
	if len(bal.ExitAWGLinks) != 2 {
		t.Fatalf("want 2 exit links, got %d", len(bal.ExitAWGLinks))
	}
	if bal.ExitAWGLinks[0].TargetID != "exit1" || bal.ExitAWGLinks[1].TargetID != "exit2" {
		t.Fatalf("links not in target order: %#v", bal.ExitAWGLinks)
	}
	if bal.ExitAWGLinks[0].InterfaceName != "awg-exit-n1" || bal.ExitAWGLinks[1].InterfaceName != "awg-exit-n2" {
		t.Fatalf("unexpected interface names: %#v", bal.ExitAWGLinks)
	}
	if bal.ExitAWGLinks[0].ClientPriv == "" || bal.ExitAWGLinks[0].ClientPub == "" {
		t.Fatal("link 1 keypair not generated")
	}
	if bal.ExitAWGLinks[1].ClientPriv == "" || bal.ExitAWGLinks[1].ClientPub == "" {
		t.Fatal("link 2 keypair not generated")
	}
	if bal.ExitAWGLinks[0].Address == bal.ExitAWGLinks[1].Address {
		t.Fatalf("exit link addresses must be unique: %#v", bal.ExitAWGLinks)
	}
	if bal.ExitAWGLinks[0].Address != "10.10.0.2/32" || bal.ExitAWGLinks[1].Address != "10.10.0.3/32" {
		t.Fatalf("unexpected exit addresses: %#v", bal.ExitAWGLinks)
	}
	if c.Nodes[1].ExitAWGServerPriv == "" || c.Nodes[1].ExitAWGServerPub == "" || c.Nodes[1].ExitAWGListenPort == 0 {
		t.Fatal("exit1 server material not generated")
	}
	if c.Nodes[2].ExitAWGServerPriv == "" || c.Nodes[2].ExitAWGServerPub == "" || c.Nodes[2].ExitAWGListenPort == 0 {
		t.Fatal("exit2 server material not generated")
	}

	// Re-run must preserve existing values (Rule 5: persistent keys/addresses).
	firstPriv := bal.ExitAWGLinks[0].ClientPriv
	firstServerPriv := c.Nodes[1].ExitAWGServerPriv
	if err := ensureAWGExitLinks(c, &c.Nodes[0]); err != nil {
		t.Fatalf("second ensureAWGExitLinks: %v", err)
	}
	if bal.ExitAWGLinks[0].ClientPriv != firstPriv {
		t.Fatal("exit client key rotated on second ensure")
	}
	if c.Nodes[1].ExitAWGServerPriv != firstServerPriv {
		t.Fatal("exit server key rotated on second ensure")
	}
}

func TestEnsureAWGExitLinks_RejectsMissingOrNonExitTarget(t *testing.T) {
	c := &model.Chain{
		Name: "mx",
		Nodes: []model.ChainNode{
			{ID: "bal", Role: model.NodeRoleEntry, ExitTargets: []string{"not-there"}},
		},
	}
	if err := ensureAWGExitLinks(c, &c.Nodes[0]); err == nil {
		t.Fatal("expected missing target error")
	}

	c = &model.Chain{
		Name: "mx",
		Nodes: []model.ChainNode{
			{ID: "bal", Role: model.NodeRoleEntry, ExitTargets: []string{"middle"}},
			{ID: "middle", Role: model.NodeRoleTransit},
		},
	}
	if err := ensureAWGExitLinks(c, &c.Nodes[0]); err == nil {
		t.Fatal("expected non-exit target error")
	}
}
