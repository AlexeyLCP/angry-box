package chain

// migrate_v2.go — the store schema v1→v2 migration: first-class inbound
// profiles + chain levels.
//
// Two data movements, both idempotent and key-preserving:
//
//  1. Standalone inbounds (NodeInfo.Inbounds with Source "" or "standalone")
//     are grouped ACROSS nodes by (protocol, port, obfuscation) into one
//     InboundProfile per distinct key; each materialized inbound gets
//     ProfileID set (the single source of truth for placement). Every
//     collapse of >1 inbound into one profile is logged (daemon log + audit).
//
//  2. Every legacy chain gets: (a) an entry profile "chain-entry-<name>"
//     holding the chain's user-entry protocol/port/preset; (b) a materialized
//     NodeInbound on each entry node's NodeInfo carrying the chain's EXISTING
//     entry credentials (AWG server keypair, CPS I1-I5/H1-H4, VLESS UUID =
//     the entry's transit UUID) — existing clients keep connecting, nothing
//     is regenerated; (c) Chain.Levels built from the flat Nodes by role:
//     entries → level 0 (group), each transit hop → its own level (order
//     preserved), exits → last level (group); (d) ChainNode.InboundRef set on
//     entry nodes. Chain.Nodes is left untouched (kept in sync by SaveChain
//     from then on).

import (
	"fmt"
	"log"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// migrateInboundProfiles is the v1→v2 migration step registered in the
// migrateOnce chain. It runs inside the store lock; audit appends only take
// auditMu, so they are safe here (no lock nesting).
func (s *Store) migrateInboundProfiles(sf *storeFile) error {
	s.migrateStandaloneInboundsToProfiles(sf)
	for _, c := range sf.Chains {
		if err := s.migrateChainToLevels(sf, c); err != nil {
			return fmt.Errorf("chain %q: %w", c.Name, err)
		}
	}
	return nil
}

// ─── Step 1: standalone inbounds → profiles ──────────────────────────────────

func (s *Store) migrateStandaloneInboundsToProfiles(sf *storeFile) {
	type ibRef struct {
		info *model.NodeInfo
		idx  int
	}
	groups := map[string][]ibRef{}
	var order []string
	for _, ni := range sf.NodeInfos {
		for i := range ni.Inbounds {
			ib := &ni.Inbounds[i]
			if ib.ProfileID != "" {
				continue // already migrated
			}
			if ib.Source != "" && ib.Source != "standalone" {
				continue // chain-sourced inbounds are handled per-chain below
			}
			key := fmt.Sprintf("%s|%d|%s", ib.Protocol, ib.Port, ib.Obfuscation)
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], ibRef{ni, i})
		}
	}
	if len(order) == 0 {
		return
	}

	used := map[string]bool{}
	for _, p := range sf.InboundProfiles {
		used[p.ID] = true
	}
	for _, key := range order {
		refs := groups[key]
		first := refs[0].info.Inbounds[refs[0].idx]
		pid := uniqueMigrationProfileID(used, migrationProfileIDBase(first.Protocol, first.Port))
		name := strings.ToUpper(first.Protocol)
		if first.Port > 0 {
			name = fmt.Sprintf("%s :%d", name, first.Port)
		}
		sf.InboundProfiles = append(sf.InboundProfiles, &model.InboundProfile{
			ID:          pid,
			Name:        name,
			Protocol:    first.Protocol,
			Port:        first.Port,
			Obfuscation: first.Obfuscation,
			CreatedAt:   timeNow(),
		})
		var nodeIDs []string
		for _, r := range refs {
			r.info.Inbounds[r.idx].ProfileID = pid
			nodeIDs = append(nodeIDs, r.info.ID)
		}
		if len(refs) > 1 {
			log.Printf("store: migration v2: collapsed %d standalone inbounds (%s port %d) into profile %q (nodes: %s)",
				len(refs), first.Protocol, first.Port, pid, strings.Join(nodeIDs, ", "))
		}
		_ = s.SaveAuditLog(&model.AuditLog{
			Actor:      "system",
			Action:     "migrate",
			TargetType: "inbound_profile",
			TargetID:   pid,
			PayloadJSON: fmt.Sprintf(`{"from":"standalone","protocol":%q,"port":%d,"nodes":%q}`,
				first.Protocol, first.Port, strings.Join(nodeIDs, ",")),
		})
	}
}

func migrationProfileIDBase(protocol string, port int) string {
	p := strings.NewReplacer("/", "-", "_", "-", " ", "-").Replace(strings.ToLower(protocol))
	if port > 0 {
		return fmt.Sprintf("prof-%s-%d", p, port)
	}
	return "prof-" + p
}

func uniqueMigrationProfileID(used map[string]bool, base string) string {
	if !used[base] {
		used[base] = true
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !used[cand] {
			used[cand] = true
			return cand
		}
	}
}

// ─── Step 2: chains → entry profile + levels ─────────────────────────────────

func (s *Store) migrateChainToLevels(sf *storeFile, c *model.Chain) error {
	if c.IsLevelized() || len(c.Nodes) == 0 {
		return nil
	}

	// Partition the flat list by the deploy-truth rule (resolveChainRoles):
	// entry = Role==entry OR (Role=="" && index 0); exit = Role==exit;
	// everything else = transit (each its own level, order preserved).
	var entryIdx, transitIdx, exitIdx []int
	for i, n := range c.Nodes {
		switch {
		case n.Role == model.NodeRoleExit:
			exitIdx = append(exitIdx, i)
		case n.Role == model.NodeRoleEntry || (n.Role == "" && i == 0):
			entryIdx = append(entryIdx, i)
		default:
			transitIdx = append(transitIdx, i)
		}
	}

	proto := string(c.UserProtocol)
	if proto == "" {
		proto = string(model.UserProtocolAWG)
	}
	profileID := "chain-entry-" + c.Name

	// (a) the entry profile itself (idempotent).
	exists := false
	for _, p := range sf.InboundProfiles {
		if p.ID == profileID {
			exists = true
			break
		}
	}
	if !exists {
		sf.InboundProfiles = append(sf.InboundProfiles, &model.InboundProfile{
			ID:                  profileID,
			Name:                fmt.Sprintf("%s entry", c.Name),
			Protocol:            proto,
			Port:                chainUserPort(c),
			Obfuscation:         c.ObfuscationProfile,
			AWGCPSCaptureDomain: c.AWGCPSCaptureDomain,
			CreatedAt:           timeNow(),
		})
	}

	// (b) materialize the entry inbound on each entry node, carrying the
	// chain's EXISTING credentials so current clients keep working.
	for _, i := range entryIdx {
		n := &c.Nodes[i]
		n.InboundRef = profileID
		ni := findNodeInfoByID(sf, n.ID)
		if ni == nil {
			// Legacy chain whose entry node has no NodeInfo yet — create a
			// minimal one from the ChainNode's own host fields so the
			// materialized inbound has somewhere to live.
			ni = &model.NodeInfo{Host: model.Host{ID: n.ID, Addr: n.Addr, User: n.User, KeyPath: n.KeyPath}}
			sf.NodeInfos = append(sf.NodeInfos, ni)
		}
		materializeChainEntryInbound(ni, c, n, profileID, proto)
	}

	// (c) build the levels.
	var levels []model.ChainLevel
	mkLevel := func(idxList []int) model.ChainLevel {
		lv := model.ChainLevel{ID: fmt.Sprintf("l%d", len(levels))}
		for _, i := range idxList {
			lv.Nodes = append(lv.Nodes, c.Nodes[i])
		}
		return lv
	}
	if len(entryIdx) > 0 {
		levels = append(levels, mkLevel(entryIdx))
	}
	for _, i := range transitIdx {
		levels = append(levels, mkLevel([]int{i}))
	}
	if len(exitIdx) > 0 {
		levels = append(levels, mkLevel(exitIdx))
	}
	c.Levels = levels

	log.Printf("store: migration v2: chain %q -> %d levels (entry profile %q, protocol %s)",
		c.Name, len(levels), profileID, proto)
	_ = s.SaveAuditLog(&model.AuditLog{
		Actor:      "system",
		Action:     "migrate",
		TargetType: "chain",
		TargetID:   c.Name,
		PayloadJSON: fmt.Sprintf(`{"levels":%d,"entry_profile":%q,"protocol":%q}`,
			len(levels), profileID, proto),
	})
	return nil
}

// materializeChainEntryInbound upserts the chain-entry NodeInbound (keyed by
// Tag == profileID) on the node's NodeInfo. Existing credentials are copied
// from the chain verbatim — nothing is regenerated (Rule 5: existing clients
// keep connecting after the migration).
func materializeChainEntryInbound(ni *model.NodeInfo, c *model.Chain, entry *model.ChainNode, profileID, proto string) {
	for i := range ni.Inbounds {
		if ni.Inbounds[i].Tag == profileID || ni.Inbounds[i].ProfileID == profileID {
			return // already materialized (idempotent)
		}
	}
	ib := model.NodeInbound{
		Protocol:    proto,
		Port:        chainEntryPort(c, entry.ID),
		Obfuscation: c.ObfuscationProfile,
		Source:      "chain:" + c.Name,
		Tag:         profileID,
		ProfileID:   profileID,
	}
	switch model.UserProtocol(proto) {
	case model.UserProtocolAWG:
		ib.ServerPrivKey = c.AWGEntryServerPriv
		ib.ServerPubKey = c.AWGEntryServerPub
		ib.AWGServerAddress = "10.8.0.1/24"
		ib.AWGCPSLevel = c.AWGCPSLevel
		ib.AWGCPSMimicry = c.AWGCPSMimicry
		ib.AWGCPSI1 = c.AWGCPSI1
		ib.AWGCPSI2 = c.AWGCPSI2
		ib.AWGCPSI3 = c.AWGCPSI3
		ib.AWGCPSI4 = c.AWGCPSI4
		ib.AWGCPSI5 = c.AWGCPSI5
		ib.AWGH1 = c.AWGH1
		ib.AWGH2 = c.AWGH2
		ib.AWGH3 = c.AWGH3
		ib.AWGH4 = c.AWGH4
	case model.UserProtocolTUIC:
		ib.UUID = c.TUICEntryUserUUID
	default: // vless-reality and others: the chain entry used the entry's transit UUID
		ib.UUID = entry.TransitUUID
	}
	ni.Inbounds = append(ni.Inbounds, ib)
}

func findNodeInfoByID(sf *storeFile, id string) *model.NodeInfo {
	for _, ni := range sf.NodeInfos {
		if ni.ID == id {
			return ni
		}
	}
	return nil
}
