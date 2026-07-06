package chain

import (
	cryptoRand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// Store provides JSON-file persistence for hosts and chains.
//
// Concurrency model: a single in-process sync.RWMutex serializes access. The
// Store does NOT provide cross-process safety — the orchestrator is assumed to
// be the sole writer of its store file (single-daemon deployment). Running two
// daemons against the same store file is unsupported and would race on the
// read-modify-write cycle each SaveX performs. A previous FS-lock attempt
// (mkdir-based mutex) was removed because it was never wired into the methods
// and gave a false sense of multi-process safety.
type Store struct {
	mu   sync.RWMutex
	path string
}

// NewStore creates a store backed by the given JSON file.
func NewStore(path string) *Store {
	s := &Store{path: path}
	s.migrateOnce()
	return s
}

// migrateOnce runs the forward migration chain to bring the on-disk store up
// to currentSchemaVersion. Idempotent: a store already at the current version
// is a no-op; a store at a lower version runs each missing migration in order,
// bumping the schema_version on disk after each step. Holds the lock, reads
// the store, migrates, and writes it back. CTO-review §8 (no schema versioning
// finding): previously each schema change was a one-shot ad-hoc migration with
// no version tracking — this makes the chain explicit and extensible.
func (s *Store) migrateOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if err != nil {
		return
	}
	// migrations[i] brings the store from version i to i+1. Add new migrations
	// here in order and bump currentSchemaVersion.
	migrations := []func(*storeFile) error{
		s.migrateMtproxyUsers, // v0 -> v1: legacy MtproxyUser -> User (subproject B)
	}
	changed := false
	for i := sf.SchemaVersion; i < len(migrations) && i < currentSchemaVersion; i++ {
		if err := migrations[i](sf); err != nil {
			log.Printf("store: migration to schema v%d failed: %v", i+1, err)
			return
		}
		sf.SchemaVersion = i + 1
		changed = true
	}
	if changed {
		_ = s.writeStore(sf)
	}
}

// migrateMtproxyUsers converts legacy storeFile.MtproxyUsers into Users with
// MTProxy* fields. Idempotent: no-op when MtproxyUsers is empty. Writes a
// one-shot .bak backup before the first migration.
func (s *Store) migrateMtproxyUsers(sf *storeFile) error {
	if len(sf.MtproxyUsers) == 0 {
		return nil
	}
	// One-shot backup (only if not already backed up this run).
	bakPath := s.path + ".prebmigrate.bak"
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		if data, err := os.ReadFile(s.path); err == nil {
			_ = os.WriteFile(bakPath, data, 0o600)
		}
	}
	existingNames := map[string]bool{}
	existingIDs := map[string]bool{}
	for _, u := range sf.Users {
		existingNames[u.Name] = true
		existingIDs[u.ID] = true
	}
	for _, m := range sf.MtproxyUsers {
		id := m.ID
		if existingIDs[id] {
			id = m.ID + "_mtp"
		}
		name := m.Name
		if existingNames[name] {
			name = m.Name + " (MTProxy @" + m.NodeID + ")"
		}
		domain := m.FakeTLSDomain
		if domain == "" {
			domain = "disk.yandex.ru"
		}
		sf.Users = append(sf.Users, &model.User{
			ID:                id,
			Name:              name,
			Active:            m.Enabled,
			CreatedAt:         m.CreatedAt,
			MTProxySecret:     m.SecretHex,
			MTProxyDomain:     domain,
			MTProxyOrderIndex: m.OrderIndex,
			MTProxyNodes:      []string{m.NodeID},
		})
		existingNames[name] = true
		existingIDs[id] = true
	}
	sf.MtproxyUsers = nil
	return nil
}

// ListMTProxyUsers returns all Users that have MTProxySecret set.
func (s *Store) ListMTProxyUsers() []*model.User {
	all, _ := s.ListUsers()
	out := make([]*model.User, 0, len(all))
	for _, u := range all {
		if u.MTProxySecret != "" {
			out = append(out, u)
		}
	}
	return out
}

// ListMTProxyUsersForNode returns Users whose MTProxyNodes contains nodeID.
func (s *Store) ListMTProxyUsersForNode(nodeID string) []*model.User {
	all, _ := s.ListUsers()
	out := make([]*model.User, 0)
	for _, u := range all {
		for _, n := range u.MTProxyNodes {
			if n == nodeID {
				out = append(out, u)
				break
			}
		}
	}
	return out
}

type storeFile struct {
	// SchemaVersion tracks the store.json schema version for forward
	// migrations. 0 = legacy store written before schema versioning was
	// introduced; the migrateOnce chain runs each missing migration in order
	// and bumps the version on disk. New stores are written at the current
	// schemaVersion constant. CTO-review §8 (no schema versioning finding).
	SchemaVersion int                        `json:"schema_version,omitempty"`
	Hosts         []*model.Host              `json:"hosts"`
	Chains        []*model.Chain             `json:"chains"`
	Users         []*model.User              `json:"users,omitempty"`
	Settings      *model.PanelSettings       `json:"settings,omitempty"`
	NodeInfos     []*model.NodeInfo          `json:"node_infos,omitempty"`
	Metrics       []*model.NodeMetrics       `json:"metrics,omitempty"`
	KnownHosts    []*model.KnownHost         `json:"known_hosts,omitempty"`
	RouteRules    []*model.RouteRule         `json:"route_rules,omitempty"`
	AuditLogs     []*model.AuditLog          `json:"audit_logs,omitempty"`
	MtproxyUsers  []*model.MtproxyUser       `json:"mtproxy_users,omitempty"`
	Links         []*model.ConnectionLink    `json:"links,omitempty"`
}

// currentSchemaVersion is the schema version the orchestrator writes. New
// stores are created at this version; existing stores at a lower version are
// migrated up to it by migrateOnce. Bump this constant when adding a new
// migration to the chain below.
const currentSchemaVersion = 1

// ─── Hosts ────────────────────────────────────────────────────────────────────

// SaveHost persists a host (creates or updates).
func (s *Store) SaveHost(h *model.Host) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	replaced := false
	for i, host := range sf.Hosts {
		if host.ID == h.ID {
			sf.Hosts[i] = h
			replaced = true
			break
		}
	}
	if !replaced {
		sf.Hosts = append(sf.Hosts, h)
	}

	return s.writeStore(sf)
}

// GetHost returns a host by ID.
func (s *Store) GetHost(id string) (*model.Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("store: host %q not found: %w", id, ErrHostNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: read: %w", err)
	}

	for _, h := range sf.Hosts {
		if h.ID == id {
			return h, nil
		}
	}
	return nil, fmt.Errorf("store: host %q not found: %w", id, ErrHostNotFound)
}

// ListHosts returns all stored hosts.
func (s *Store) ListHosts() ([]*model.Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: read: %w", err)
	}
	return sf.Hosts, nil
}

// DeleteHost removes a host by ID.
func (s *Store) DeleteHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		return fmt.Errorf("store: host %q not found: %w", id, ErrHostNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	// Safety check: refuse delete if any chain still references this host
	for _, c := range sf.Chains {
		for _, n := range c.Nodes {
			if n.ID == id {
				return fmt.Errorf("store: cannot delete host %q: still referenced by chain %q", id, c.Name)
			}
		}
	}

	found := false
	filtered := sf.Hosts[:0]
	for _, h := range sf.Hosts {
		if h.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, h)
	}
	if !found {
		return fmt.Errorf("store: host %q not found: %w", id, ErrHostNotFound)
	}

	sf.Hosts = filtered
	return s.writeStore(sf)
}

// ─── Chains ───────────────────────────────────────────────────────────────────

// SaveChain persists a chain (creates or updates).
func (s *Store) SaveChain(chain *model.Chain) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	replaced := false
	for i, c := range sf.Chains {
		if c.Name == chain.Name {
			sf.Chains[i] = chain
			replaced = true
			break
		}
	}
	if !replaced {
		sf.Chains = append(sf.Chains, chain)
	}

	return s.writeStore(sf)
}

// GetChain returns a chain by name.
func (s *Store) GetChain(name string) (*model.Chain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("store: chain %q not found: %w", name, ErrChainNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: read: %w", err)
	}

	for _, c := range sf.Chains {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("store: chain %q not found: %w", name, ErrChainNotFound)
}

// ListChains returns all stored chains.
func (s *Store) ListChains() ([]*model.Chain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: read: %w", err)
	}
	return sf.Chains, nil
}

// GetChainsForNode returns all chains that contain the given node ID.
func (s *Store) GetChainsForNode(nodeID string) ([]*model.Chain, error) {
	chains, err := s.ListChains()
	if err != nil {
		return nil, err
	}
	var result []*model.Chain
	for _, c := range chains {
		for _, n := range c.Nodes {
			if n.ID == nodeID {
				result = append(result, c)
				break
			}
		}
	}
	return result, nil
}

// DeleteChain removes a chain by name.
func (s *Store) DeleteChain(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		return fmt.Errorf("store: chain %q not found: %w", name, ErrChainNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	found := false
	filtered := sf.Chains[:0]
	for _, c := range sf.Chains {
		if c.Name == name {
			found = true
			continue
		}
		filtered = append(filtered, c)
	}
	if !found {
		return fmt.Errorf("store: chain %q not found: %w", name, ErrChainNotFound)
	}

	sf.Chains = filtered
	return s.writeStore(sf)
}

// ResolveNodes resolves host references in a chain to full ChainNode entries.
func (s *Store) ResolveNodes(chain *model.Chain) ([]model.ChainNode, error) {
	resolved := make([]model.ChainNode, 0, len(chain.Nodes))
	for _, n := range chain.Nodes {
		host, err := s.GetHost(n.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve node %q: %w", n.ID, err)
		}
		
		info, _ := s.GetNodeInfo(n.ID)

		rn := model.ChainNode{
			ID:      host.ID,
			Addr:    host.Addr,
			User:    host.User,
			KeyPath: host.KeyPath,
			Port:           n.Port,
			TransitPrivKey: n.TransitPrivKey,
			TransitShortID: n.TransitShortID,
			TransitUUID:    n.TransitUUID,
		}
		if info != nil {
			rn.Inbounds = info.Inbounds
		}
		resolved = append(resolved, rn)
	}
	return resolved, nil
}

// ─── Users ─────────────────────────────────────────────────────────────────────

// SaveUser persists a user (creates or updates).
func (s *Store) SaveUser(u *model.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	if u.CreatedAt.IsZero() {
		u.CreatedAt = timeNow()
	}

	replaced := false
	for i, existing := range sf.Users {
		if existing.ID == u.ID {
			sf.Users[i] = u
			replaced = true
			break
		}
	}
	if !replaced {
		sf.Users = append(sf.Users, u)
	}
	// MTProxy secret uniqueness: reject if another user shares the same secret AND
	// an overlapping MTProxyNode. Self (same ID on update) is allowed.
	if u.MTProxySecret != "" && len(u.MTProxyNodes) > 0 {
		for _, ex := range sf.Users {
			if ex.ID == u.ID {
				continue
			}
			if ex.MTProxySecret != u.MTProxySecret {
				continue
			}
			for _, n := range u.MTProxyNodes {
				for _, en := range ex.MTProxyNodes {
					if en == n {
						return fmt.Errorf("store: mtproxy secret already used on node %s", n)
					}
				}
			}
		}
	}
	return s.writeStore(sf)
}

// GetUser returns a user by ID.
func (s *Store) GetUser(id string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("store: user %q not found: %w", id, ErrUserNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: read: %w", err)
	}
	for _, u := range sf.Users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, fmt.Errorf("store: user %q not found: %w", id, ErrUserNotFound)
}

// ListUsers returns all users.
func (s *Store) ListUsers() ([]*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return sf.Users, nil
}

// DeleteUser removes a user by ID.
func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if err != nil {
		return err
	}
	filtered := sf.Users[:0]
	found := false
	for _, u := range sf.Users {
		if u.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, u)
	}
	if !found {
		return fmt.Errorf("store: user %q not found: %w", id, ErrUserNotFound)
	}
	sf.Users = filtered
	return s.writeStore(sf)
}

// ─── Settings ──────────────────────────────────────────────────────────────────

// GetSettings returns panel settings (or defaults if not set).
func (s *Store) GetSettings() (*model.PanelSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return &model.PanelSettings{MetricsInterval: 15}, nil
		}
		return nil, err
	}
	if sf.Settings == nil {
		return &model.PanelSettings{MetricsInterval: 15}, nil
	}
	if sf.Settings.MetricsInterval <= 0 {
		sf.Settings.MetricsInterval = 15
	}
	return sf.Settings, nil
}

// SaveSettings persists panel settings.
func (s *Store) SaveSettings(settings *model.PanelSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}
	sf.Settings = settings
	return s.writeStore(sf)
}

// ─── NodeInfos ─────────────────────────────────────────────────────────────────

func (s *Store) SaveNodeInfo(ni *model.NodeInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}
	for i, n := range sf.NodeInfos {
		if n.ID == ni.ID {
			sf.NodeInfos[i] = ni
			return s.writeStore(sf)
		}
	}
	sf.NodeInfos = append(sf.NodeInfos, ni)
	return s.writeStore(sf)
}

func (s *Store) GetNodeInfo(id string) (*model.NodeInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if err != nil {
		return nil, err
	}
	for _, n := range sf.NodeInfos {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, fmt.Errorf("store: node_info %q not found", id)
}

func (s *Store) ListNodeInfos() ([]*model.NodeInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return sf.NodeInfos, nil
}

// ─── Metrics ───────────────────────────────────────────────────────────────────

func (s *Store) SaveMetrics(m *model.NodeMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}
	m.LastChecked = timeNow()
	for i, existing := range sf.Metrics {
		if existing.HostID == m.HostID {
			sf.Metrics[i] = m
			return s.writeStore(sf)
		}
	}
	sf.Metrics = append(sf.Metrics, m)
	return s.writeStore(sf)
}

func (s *Store) GetMetrics(hostID string) (*model.NodeMetrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if err != nil {
		return nil, err
	}
	for _, m := range sf.Metrics {
		if m.HostID == hostID {
			return m, nil
		}
	}
	return nil, fmt.Errorf("store: metrics for %q not found", hostID)
}

func (s *Store) ListMetrics() ([]*model.NodeMetrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return sf.Metrics, nil
}

func timeNow() time.Time { return time.Now() }

// ─── KnownHosts / HostKeyManager ───────────────────────────────────────────────

func (s *Store) GetKnownHost(addr string) (*model.KnownHost, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not found")
		}
		return nil, err
	}

	// Try exact match first, then try without port (SSH callback passes hostname
	// without port, but store entries are saved with port from user config).
	candidates := []string{addr}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		candidates = append(candidates, host)
	} else {
		candidates = append(candidates, net.JoinHostPort(addr, "22"))
	}

	for _, c := range candidates {
		for _, kh := range sf.KnownHosts {
			if kh.Addr == c {
				return kh, nil
			}
		}
	}
	return nil, fmt.Errorf("not found")
}

func (s *Store) SaveKnownHost(kh *model.KnownHost) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return err
	}

	// Normalize: always include port 22 if missing, for consistent matching.
	if _, _, err := net.SplitHostPort(kh.Addr); err != nil {
		kh.Addr = net.JoinHostPort(kh.Addr, "22")
	}

	for i, existing := range sf.KnownHosts {
		if existing.Addr == kh.Addr {
			sf.KnownHosts[i] = kh
			return s.writeStore(sf)
		}
	}
	sf.KnownHosts = append(sf.KnownHosts, kh)
	return s.writeStore(sf)
}

// CheckHostKey implements sshclient.HostKeyManager.
func (s *Store) CheckHostKey(addr string, remoteKey ssh.PublicKey) error {
	fingerprint := ssh.FingerprintSHA256(remoteKey)

	kh, err := s.GetKnownHost(addr)
	if err != nil {
		// TOFU: save automatically as trusted.
		newKH := &model.KnownHost{
			Addr:        addr,
			Fingerprint: fingerprint,
			FirstSeen:   timeNow(),
			Trusted:     true,
		}
		_ = s.SaveKnownHost(newKH)
		return nil // trust on first use
	}

	if kh.Fingerprint != fingerprint {
		return &sshclient.HostKeyError{
			RemoteFingerprint: fingerprint,
			Changed:           true,
		}
	}

	if !kh.Trusted {
		return &sshclient.HostKeyError{
			RemoteFingerprint: fingerprint,
			Changed:           false,
		}
	}

	return nil
}

// ResolveKey implements sshclient.KeyResolver.
func (s *Store) ResolveKey(keyID string) (string, bool) {
	if keyID == "" {
		return "", false
	}

	// Manual entry is not handled here, it's usually passed directly via "password:" or written to temp in UI

	// Stored keys from settings
	settings, err := s.GetSettings()
	if err == nil {
		for _, k := range settings.SSHKeys {
			if k.ID == keyID && k.KeyData != "" {
				return k.KeyData, true
			}
		}
	}

	// System keys
	if strings.HasPrefix(keyID, "system-") {
		home, err := os.UserHomeDir()
		if err == nil {
			fileName := strings.TrimPrefix(keyID, "system-")
			path := home + "/.ssh/" + fileName
			data, err := os.ReadFile(path)
			if err == nil {
				return string(data), true
			}
		}
	}

	return "", false
}

// ─── RouteRules ──────────────────────────────────────────────────────────────

// SaveRouteRule persists a route rule (create or update by ID).
func (s *Store) SaveRouteRule(r *model.RouteRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	if r.ID == "" {
		r.ID = newID()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = timeNow()
	}
	replaced := false
	for i, ex := range sf.RouteRules {
		if ex.ID == r.ID {
			sf.RouteRules[i] = r
			replaced = true
			break
		}
	}
	if !replaced {
		sf.RouteRules = append(sf.RouteRules, r)
	}
	return s.writeStore(sf)
}

// ListRouteRulesForNode returns enabled+disabled route rules for a node, sorted by priority.
func (s *Store) ListRouteRulesForNode(nodeID string) ([]*model.RouteRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return []*model.RouteRule{}, nil
		}
		return nil, err
	}
	var out []*model.RouteRule
	for _, r := range sf.RouteRules {
		if r.NodeID == nodeID {
			out = append(out, r)
		}
	}
	// stable priority sort (lower first)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Priority < out[j-1].Priority; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// DeleteRouteRule removes a route rule by ID.
func (s *Store) DeleteRouteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		return err
	}
	for i, r := range sf.RouteRules {
		if r.ID == id {
			sf.RouteRules = append(sf.RouteRules[:i], sf.RouteRules[i+1:]...)
			return s.writeStore(sf)
		}
	}
	return fmt.Errorf("store: route rule %q not found", id)
}

// ─── AuditLogs (append-mostly) ───────────────────────────────────────────────

// SaveAuditLog appends an audit entry. ID/TS are filled if empty.
func (s *Store) SaveAuditLog(a *model.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	if a.ID == "" {
		a.ID = newID()
	}
	if a.TS.IsZero() {
		a.TS = timeNow()
	}
	if a.Actor == "" {
		a.Actor = "operator"
	}
	sf.AuditLogs = append(sf.AuditLogs, a)
	// Cap the log to the most recent 5000 entries to avoid unbounded growth.
	if len(sf.AuditLogs) > 5000 {
		sf.AuditLogs = sf.AuditLogs[len(sf.AuditLogs)-5000:]
	}
	return s.writeStore(sf)
}

// ListAuditLogs returns audit entries newest-first, capped to limit.
func (s *Store) ListAuditLogs(limit int) ([]*model.AuditLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return []*model.AuditLog{}, nil
		}
		return nil, err
	}
	if limit <= 0 || limit > len(sf.AuditLogs) {
		limit = len(sf.AuditLogs)
	}
	// newest-first (reverse over the slice)
	out := make([]*model.AuditLog, 0, limit)
	for i := len(sf.AuditLogs) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, sf.AuditLogs[i])
	}
	return out, nil
}

// newID returns a unique identifier for a new store entity (time-based + random
// suffix), matching the style of existing node/user IDs.
func newID() string {
	b := make([]byte, 4)
	_, _ = cryptoRand.Read(b)
	return fmt.Sprintf("%d-%x", timeNow().UnixNano(), b)
}

// ─── ConnectionLinks (spider web edges) ─────────────────────────────────────

// SaveLink persists a connection link (create or update by ID). Uniqueness of
// (from, to, chain) is enforced — a duplicate edge is rejected. ID is generated
// if empty.
func (s *Store) SaveLink(l *model.ConnectionLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	if l.ID == "" {
		l.ID = newID()
	}
	for _, ex := range sf.Links {
		if ex.FromNodeID == l.FromNodeID && ex.ToNodeID == l.ToNodeID && ex.ChainName == l.ChainName && ex.ID != l.ID {
			return fmt.Errorf("store: link %s→%s on chain %q already exists", l.FromNodeID, l.ToNodeID, l.ChainName)
		}
	}
	replaced := false
	for i, ex := range sf.Links {
		if ex.ID == l.ID {
			sf.Links[i] = l
			replaced = true
			break
		}
	}
	if !replaced {
		sf.Links = append(sf.Links, l)
	}
	return s.writeStore(sf)
}

// ListLinks returns all connection links.
func (s *Store) ListLinks() ([]*model.ConnectionLink, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return []*model.ConnectionLink{}, nil
		}
		return nil, err
	}
	return sf.Links, nil
}

// ListLinksForChain returns links belonging to a chain.
func (s *Store) ListLinksForChain(chainName string) ([]*model.ConnectionLink, error) {
	all, err := s.ListLinks()
	if err != nil {
		return nil, err
	}
	var out []*model.ConnectionLink
	for _, l := range all {
		if l.ChainName == chainName {
			out = append(out, l)
		}
	}
	return out, nil
}

// GetLink returns a link by ID.
func (s *Store) GetLink(id string) (*model.ConnectionLink, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		return nil, err
	}
	for _, l := range sf.Links {
		if l.ID == id {
			return l, nil
		}
	}
	return nil, fmt.Errorf("store: link %q not found", id)
}

// DeleteLink removes a link by ID.
func (s *Store) DeleteLink(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		return err
	}
	for i, l := range sf.Links {
		if l.ID == id {
			sf.Links = append(sf.Links[:i], sf.Links[i+1:]...)
			return s.writeStore(sf)
		}
	}
	return fmt.Errorf("store: link %q not found", id)
}

// SaveNodePosition persists a node's spider-web layout coordinates. Creates a
// NodeInfo record if none exists yet so the position survives even for nodes
// that haven't been applied.
func (s *Store) SaveNodePosition(nodeID string, x, y float64) error {
	info, err := s.GetNodeInfo(nodeID)
	if err != nil {
		info = &model.NodeInfo{}
	}
	info.ID = nodeID
	info.PosX = x
	info.PosY = y
	return s.SaveNodeInfo(info)
}

// ─── internals ─────────────────────────────────────────────────────────────────

func (s *Store) readStore() (*storeFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}

	// At-rest encryption (CTO-review §6 CRITICAL): if a master-key file is
	// present next to the store AND the on-disk data starts with the encMagic
	// header, decrypt before JSON-unmarshalling. If the key is present but
	// the data is NOT encrypted (legacy store first written in plaintext,
	// operator later added the key), treat it as plaintext so the store can
	// be read; the next writeStore will encrypt it (seamless upgrade). If the
	// data IS encrypted but no key is present, fall through to json.Unmarshal
	// which will fail with a clear "not a valid JSON" error (the operator
	// deleted the key without decrypting first).
	if isEncrypted(data) {
		key, kerr := loadMasterKey(masterKeyPath(s.path))
		if kerr != nil {
			return nil, fmt.Errorf("store: encrypted but master key unavailable: %w", kerr)
		}
		if key == nil {
			return nil, fmt.Errorf("store: data is encrypted but master key file %s is missing", masterKeyPath(s.path))
		}
		plain, derr := decryptStore(data, key)
		if derr != nil {
			return nil, fmt.Errorf("store: decrypt: %w", derr)
		}
		data = plain
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("store: parse: %w", err)
	}
	return &sf, nil
}

func (s *Store) writeStore(sf *storeFile) error {
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal: %w", err)
	}

	// At-rest encryption: if a master-key file is present, encrypt the whole
	// payload before the atomic write. Absence of the key = legacy plaintext
	// mode (no behaviour change). The key file is operator-opt-in (see
	// store_crypto.go header for the rationale).
	if key, kerr := loadMasterKey(masterKeyPath(s.path)); kerr == nil && key != nil {
		enc, eerr := encryptStore(data, key)
		if eerr != nil {
			return fmt.Errorf("store: encrypt: %w", eerr)
		}
		data = enc
	} else if kerr != nil && !errors.Is(kerr, os.ErrNotExist) {
		// A present-but-invalid key file is a hard error (do not silently
		// write plaintext and leak the secrets).
		return fmt.Errorf("store: master key invalid: %w", kerr)
	}

	// Atomic write (temp + rename) so a crash mid-write cannot truncate the
	// store file and lose the whole panel state (CTO-review M3).
	if err := atomicWriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("store: write: %w", err)
	}
	return nil
}
