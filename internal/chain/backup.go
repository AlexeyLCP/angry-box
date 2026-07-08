package chain

// backup.go — node + full-panel backup/restore helpers.
//
// The whole panel identity (Hosts + NodeInfos + Chains + the transit/exit
// material on ChainNodes + Users + Settings + KnownHosts) already lives in
// store.json, written atomically and optionally encrypted at rest. These
// helpers expose TWO granularities for the operator:
//
//   - ExportStore / ImportStore: the entire storeFile as plaintext JSON. This
//     is the "back up the whole panel" / "migrate to a new orchestrator" path.
//     Plaintext (not the on-disk encrypted form) so a backup is portable —
//     restoring on a different install does not require the same master key.
//   - ExportNode / ImportNode: one node's slice — its Host + NodeInfo + the
//     ChainNode records (with all transit/exit material) for every chain the
//     node belongs to. This is the "move one node between installs" or "back
//     up a blocked node's identity before poking at it" path.
//
// Import dedups by ID: an existing host with the same ID but a different Addr
// is preserved unless force=true (so importing a node backup onto an install
// that already manages that ID does not silently reroute a live node). The
// chain-membership records carried by a node backup are merged into existing
// chains by node ID — a missing matching chain is skipped (not created), so a
// node backup alone never invents a half-chain.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// BackupFormat tags the JSON payload so a single import endpoint / restore
// command can auto-detect whether a file is a full-store backup or a
// per-node backup without sniffing fields.
const (
	BackupFormatStore = "angry-box-store"
	BackupFormatNode  = "angry-box-node"
)

// backupEnvelope wraps every exported backup so the format is self-describing
// and a future schema bump can be detected. Version is the backup format
// version (currently 1), distinct from the store's schema_version.
type backupEnvelope struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	ExportedAt string `json:"exported_at"`
}

// NodeBackup is one node's portable identity: its Host, its NodeInfo, and the
// ChainNode record (with all transit/exit material) for every chain it
// belongs to. Restoring this onto an install reproduces the node's identity
// and its membership in those chains.
type NodeBackup struct {
	backupEnvelope
	Node          model.ChainNode      `json:"node"`          // the Host-shaped record (ID/Addr/User/KeyPath) + transit material
	NodeInfo      *model.NodeInfo      `json:"node_info,omitempty"`
	Chains        []NodeChainMembership `json:"chains,omitempty"`
}

// NodeChainMembership records this node's slot in one chain: the chain name +
// the full ChainNode (carrying Role/ExitTargets/Transit*/ExitAWG* so restoring
// re-creates the node's role + link material in that chain).
type NodeChainMembership struct {
	ChainName string             `json:"chain_name"`
	Node      model.ChainNode    `json:"node"`
}

// ErrBackupFormatUnknown is returned by detectBackupFormat when the JSON does
// not carry a recognized backup envelope.
var ErrBackupFormatUnknown = errors.New("backup: unknown format (not an angry-box store or node backup)")

// detectBackupFormat peeks at a backup JSON payload's envelope to decide
// whether it is a full-store backup or a per-node backup. Used by the unified
// restore path so the operator does not have to tell us which kind it is.
func detectBackupFormat(data []byte) (string, error) {
	var env backupEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("backup: parse envelope: %w", err)
	}
	switch env.Format {
	case BackupFormatStore, BackupFormatNode:
		return env.Format, nil
	default:
		return "", ErrBackupFormatUnknown
	}
}

// DetectBackupFormat is the exported form of detectBackupFormat for callers
// outside the chain package (the web import handler auto-detects a store vs
// node backup from the envelope so a single endpoint handles both).
func DetectBackupFormat(data []byte) (string, error) {
	return detectBackupFormat(data)
}

// ExportStore returns the entire store as plaintext JSON (a full-panel
// backup). The on-disk store may be encrypted at rest; this returns the
// decrypted content so a backup is portable across installs (restoring on a
// different orchestrator does not require the same master key). The returned
// bytes are pretty-marshaled JSON with a backup envelope.
func (s *Store) ExportStore() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("export store: read: %w", err)
	}
	if sf == nil {
		sf = &storeFile{}
	}
	// Wrap the storeFile in an envelope so a restore can detect it. The Store
	// nests under its own key so it does not collide with the envelope fields.
	type storeBackup struct {
		backupEnvelope
		Store *storeFile `json:"store"`
	}
	b := storeBackup{
		backupEnvelope: backupEnvelope{Format: BackupFormatStore, Version: 1, ExportedAt: time.Now().UTC().Format(time.RFC3339)},
		Store:          sf,
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("export store: marshal: %w", err)
	}
	return out, nil
}

// ImportStore replaces the entire panel state from a full-store backup
// (ExportStore output). force=true overwrites a non-empty existing store;
// force=false refuses (returns an error) if the current store has any hosts,
// so an operator cannot accidentally wipe a live panel by importing into it.
// The imported store is written through the normal atomic writeStore path
// (which re-encrypts at rest if a master key is present on this install), then
// re-migrated to the current schema.
func (s *Store) ImportStore(data []byte, force bool) error {
	format, err := detectBackupFormat(data)
	if err != nil {
		return err
	}
	if format != BackupFormatStore {
		return fmt.Errorf("import store: backup is %q, not a store backup", format)
	}
	var b struct {
		backupEnvelope
		Store *storeFile `json:"store"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("import store: parse: %w", err)
	}
	if b.Store == nil {
		return errors.New("import store: no store payload in backup")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, rerr := s.readStore()
	if rerr != nil && !os.IsNotExist(rerr) {
		return fmt.Errorf("import store: read current: %w", rerr)
	}
	if !force && cur != nil && len(cur.Hosts) > 0 {
		return errors.New("import store: current store is non-empty (use force to overwrite)")
	}
	if err := s.writeStore(b.Store); err != nil {
		return fmt.Errorf("import store: write: %w", err)
	}
	// Re-run migrations on the imported store so a backup from an older
	// schema_version is brought up to current on restore. migrateOnce takes
	// the lock itself, so release + re-acquire around it.
	s.mu.Unlock()
	s.migrateOnce()
	s.mu.Lock()
	return nil
}

// ExportNode returns one node's portable identity. ErrHostNotFound if the ID
// is not in the store. The backup carries the node's Host + NodeInfo +, for
// every chain containing the node, the full ChainNode record (with all
// transit/exit material). Restoring it reproduces the node's identity and its
// role + link material in each chain.
func (s *Store) ExportNode(id string) (*NodeBackup, error) {
	host, err := s.GetHost(id)
	if err != nil {
		return nil, err
	}
	info, _ := s.GetNodeInfo(id)

	// Snapshot the node's Host into the ChainNode shape (ID/Addr/User/KeyPath)
	// so the backup is self-contained even if the node is in zero chains.
	node := model.ChainNode{ID: host.ID, Addr: host.Addr, User: host.User, KeyPath: host.KeyPath}

	chains, _ := s.ListChains()
	var memberships []NodeChainMembership
	for _, c := range chains {
		for _, n := range c.Nodes {
			if n.ID == id {
				memberships = append(memberships, NodeChainMembership{ChainName: c.Name, Node: n})
			}
		}
	}
	return &NodeBackup{
		backupEnvelope: backupEnvelope{Format: BackupFormatNode, Version: 1, ExportedAt: time.Now().UTC().Format(time.RFC3339)},
		Node:           node,
		NodeInfo:       info,
		Chains:         memberships,
	}, nil
}

// ImportNode restores a node from a NodeBackup (ExportNode output). The Host +
// NodeInfo are restored first. Then for each chain membership: if a chain with
// that name exists, the node's ChainNode record is merged into it by ID
// (replacing an existing node with the same ID, or appending if absent); a
// chain that does not exist on this install is skipped (a node backup alone
// never invents a half-chain — import the full store, or create the chain
// first). force=true overwrites an existing host with the same ID but a
// different Addr (reroute); force=false preserves the existing host.
func (s *Store) ImportNode(b *NodeBackup, force bool) error {
	if b == nil {
		return errors.New("import node: nil backup")
	}
	if b.Format != BackupFormatNode {
		return fmt.Errorf("import node: backup format %q, not a node backup", b.Format)
	}

	host := &model.Host{ID: b.Node.ID, Addr: b.Node.Addr, User: b.Node.User, KeyPath: b.Node.KeyPath}
	if existing, err := s.GetHost(b.Node.ID); err == nil {
		// Same ID already managed. Without force, refuse to reroute a live node
		// (an accidental import would silently move a working node). With force,
		// overwrite — this is the "restore a backed-up node onto a fresh install
		// that happens to have a stub with the same ID" path.
		if !force && existing.Addr != host.Addr {
			return fmt.Errorf("import node: host %q already exists at %q (use force to overwrite with %q)", host.ID, existing.Addr, host.Addr)
		}
	}
	if err := s.SaveHost(host); err != nil {
		return fmt.Errorf("import node: save host: %w", err)
	}
	if b.NodeInfo != nil {
		// Preserve the backup's NodeInfo, but pin the Host to the just-restored
		// values so the embedded Host is consistent (a stale Host on NodeInfo
		// would shadow the restored one).
		ni := *b.NodeInfo
		ni.Host = *host
		if err := s.SaveNodeInfo(&ni); err != nil {
			return fmt.Errorf("import node: save node_info: %w", err)
		}
	}

	// Merge chain memberships. Chains are matched by name; a missing chain is
	// skipped (collected so the caller can surface which chains were not
	// found). The node's ChainNode replaces any existing node with the same ID
	// in the matched chain, or is appended.
	chains, _ := s.ListChains()
	chainByName := make(map[string]*model.Chain, len(chains))
	for _, c := range chains {
		chainByName[c.Name] = c
	}
	var skipped []string
	for _, m := range b.Chains {
		matched := chainByName[m.ChainName]
		if matched == nil {
			skipped = append(skipped, m.ChainName)
			continue
		}
		replaced := false
		for i, n := range matched.Nodes {
			if n.ID == m.Node.ID {
				matched.Nodes[i] = m.Node
				replaced = true
				break
			}
		}
		if !replaced {
			matched.Nodes = append(matched.Nodes, m.Node)
		}
		if err := s.SaveChain(matched); err != nil {
			return fmt.Errorf("import node: save chain %q: %w", m.ChainName, err)
		}
	}
	if len(skipped) > 0 {
		// Not fatal — the node + its Host/NodeInfo were restored; the missing
		// chains are reported so the operator can create them or import the full
		// store. Return a wrapped error so the caller can surface the list.
		return fmt.Errorf("import node: restored %q, skipped missing chains: %v", host.ID, skipped)
	}
	return nil
}