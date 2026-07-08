package chain

// relocate.go — node relocation: move a blocked/failed node to a new VPS and
// auto-heal the chains that referenced it.
//
// The use case: one chain node's IP gets blackholed by DPI (or the VPS dies).
// The operator spins up a fresh VPS and runs RelocateNode (UI button or
// `angry-box relocate`). Relocation:
//
//  1. Updates the node's Addr (and optionally User/KeyPath) in three places —
//     Host, NodeInfo.Host, and the ChainNode snapshot in every chain that
//     contains the node — so the live SSH target + the next-hop embedding both
//     move to the new IP.
//  2. Keeps the node's ID + ALL transit/exit material (Reality PrivateKey/
//     ShortID/UUID, AWG Transit*/ExitAWG*, Role, ExitTargets) unchanged, so
//     re-deploying the node onto the new VPS reuses the SAME credentials —
//     other nodes and existing clients do NOT need reconfiguration.
//  3. Re-runs ApplyChain for every chain containing the node. ApplyChain
//     re-deploys the node itself (onto the new VPS, with the same keys) AND
//     every node that embeds this node's Addr in its config — the previous hop
//     (its outbound dials extractHost(N.Addr)), and any balancer whose
//     ExitTargets include N (its awg-exit-nX endpoint embeds N.Addr). One
//     RelocateNode call therefore heals the whole affected chain topology.
//
// ResolveNodes (Stage 1 fix) preserves the transit material across this
// re-apply, which is what makes key reuse work. Without that fix, relocation
// would regenerate AWG keys and break the links it is supposed to heal.

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// RelocateReport is the outcome of a RelocateNode call: which node moved, from
// where to where, and the per-chain re-apply result (so the UI/CLI can show
// exactly which chains were healed and which failed).
type RelocateReport struct {
	NodeID  string
	OldAddr string
	NewAddr string
	Chains  []RelocateChainResult
}

// RelocateChainResult is one chain's re-apply outcome during a relocation.
type RelocateChainResult struct {
	Name    string
	Success bool
	Error   string
	Nodes   []NodeResult // the per-node deploy results from ApplyChain
}

// chainApplier is the subset of *Applier RelocateNode uses. Declared as an
// interface so tests inject a fake that records calls + returns a canned
// report without dialing SSH; production passes a real *Applier (which
// satisfies this interface structurally).
type chainApplier interface {
	ApplyChain(ctx context.Context, store *Store, chain *model.Chain, awgClientPubKey string) (*ApplyReport, error)
}

// RelocateNode moves a node to a new VPS and re-deploys every chain containing
// it so the new IP propagates to dependent nodes (previous hop + balancers on
// this node if it is an exit). The node's ID + transit/exit material are
// preserved so re-deploy reuses the same credentials.
//
// newAddr is required (IP:port). newUser/newKeyPath are optional — empty keeps
// the current value. applier is the chain applier (chain.NewApplier(factory,
// connector)); it must be non-nil. awgClientPubKey is forwarded to ApplyChain
// (pass "" to let ApplyChain auto-generate a sample for AWG user entries).
//
// The re-apply of each affected chain is sequential (chains can share nodes —
// the existing per-host lock serializes deploys anyway), and a failure on one
// chain does NOT abort the others: the report carries per-chain success/error
// so the operator sees exactly what healed and what to retry.
func RelocateNode(ctx context.Context, store *Store, applier chainApplier, nodeID, newAddr, newUser, newKeyPath, awgClientPubKey string) (*RelocateReport, error) {
	if store == nil {
		return nil, fmt.Errorf("relocate: nil store")
	}
	if applier == nil {
		return nil, fmt.Errorf("relocate: nil applier")
	}
	newAddr = strings.TrimSpace(newAddr)
	if nodeID == "" || newAddr == "" {
		return nil, fmt.Errorf("relocate: nodeID and newAddr are required")
	}

	host, err := store.GetHost(nodeID)
	if err != nil {
		return nil, fmt.Errorf("relocate: %w", err)
	}
	oldAddr := host.Addr

	// 1. Update Addr (+ optional User/KeyPath) in the three places it lives.
	host.Addr = newAddr
	if newUser != "" {
		host.User = newUser
	}
	if newKeyPath != "" {
		host.KeyPath = newKeyPath
	}
	if err := store.SaveHost(host); err != nil {
		return nil, fmt.Errorf("relocate: save host: %w", err)
	}
	// NodeInfo embeds Host — reload it, pin the new Host fields, save. Keep all
	// other NodeInfo fields (Country, Inbounds, Takeover, etc.) intact.
	if info, _ := store.GetNodeInfo(nodeID); info != nil {
		info.Host = *host
		if err := store.SaveNodeInfo(info); err != nil {
			return nil, fmt.Errorf("relocate: save node_info: %w", err)
		}
	}
	// Update the ChainNode.Addr snapshot in every chain containing the node.
	// ResolveNodes overwrites Addr from Host on apply, but keeping the snapshot
	// consistent avoids a stale-addr window if something reads the chain
	// directly. The transit/exit material on each ChainNode is untouched.
	affected, err := store.GetChainsForNode(nodeID)
	if err != nil {
		return nil, fmt.Errorf("relocate: list chains: %w", err)
	}
	for _, c := range affected {
		for i := range c.Nodes {
			if c.Nodes[i].ID == nodeID {
				c.Nodes[i].Addr = newAddr
				if newUser != "" {
					c.Nodes[i].User = newUser
				}
				if newKeyPath != "" {
					c.Nodes[i].KeyPath = newKeyPath
				}
			}
		}
		if err := store.SaveChain(c); err != nil {
			return nil, fmt.Errorf("relocate: save chain %q: %w", c.Name, err)
		}
	}

	report := &RelocateReport{NodeID: nodeID, OldAddr: oldAddr, NewAddr: newAddr}

	// 2. Re-apply each affected chain. ApplyChain re-deploys the relocated node
	//    (onto the new VPS, reusing its persisted transit keys) AND every node
	//    whose config embeds the node's Addr (previous hop outbound, balancer
	//    awg-exit-nX endpoint). A failure on one chain is recorded, not fatal —
	//    the operator gets a full per-chain picture.
	for _, c := range affected {
		resolved, err := store.ResolveNodes(c)
		res := RelocateChainResult{Name: c.Name}
		if err != nil {
			res.Error = fmt.Sprintf("resolve: %v", err)
			report.Chains = append(report.Chains, res)
			continue
		}
		c.Nodes = resolved
		applyReport, err := applier.ApplyChain(ctx, store, c, awgClientPubKey)
		if err != nil {
			res.Error = err.Error()
			if applyReport != nil {
				res.Nodes = applyReport.Nodes
			}
			report.Chains = append(report.Chains, res)
			continue
		}
		res.Success = true
		res.Nodes = applyReport.Nodes
		// Persist any newly generated transit material (RelocateNode reuses
		// existing keys, but a node that had none yet would generate them here).
		_ = store.SaveChain(c)
		report.Chains = append(report.Chains, res)
	}

	// 3. Audit the relocation (best-effort; WriteAudit swallows errors).
	WriteAudit(store, "relocate", "node", nodeID,
		map[string]string{"old_addr": oldAddr, "new_addr": newAddr,
			"chains": chainNames(affected), "success": fmt.Sprintf("%d/%d", countSuccess(report.Chains), len(report.Chains))},
		"operator")

	return report, nil
}

// chainNames joins chain names for an audit payload.
func chainNames(chains []*model.Chain) string {
	names := make([]string, 0, len(chains))
	for _, c := range chains {
		names = append(names, c.Name)
	}
	return strings.Join(names, ",")
}

// countSuccess returns how many chain re-applies succeeded in the report.
func countSuccess(results []RelocateChainResult) int {
	n := 0
	for _, r := range results {
		if r.Success {
			n++
		}
	}
	return n
}