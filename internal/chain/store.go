package chain

import (
	cryptoRand "crypto/rand"
	"encoding/json"
	"fmt"
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
type Store struct {
	mu   sync.RWMutex
	path string
}

// acquireFSLock and releaseFSLock provide cross-process synchronization via a
// directory-based mutex (store.json.lock). They are NOT currently wired into the
// Store methods — only sync.RWMutex is used. To enable multi-process safety,
// replace direct mu.Lock()/mu.RUnlock() calls with withLock()/withRLock().
func (s *Store) acquireFSLock() {
	lockPath := s.path + ".lock"
	for {
		if err := os.Mkdir(lockPath, 0o755); err == nil {
			return
		}
		// If it exists, check if it's stale (e.g. older than 30 seconds)
		if info, err := os.Stat(lockPath); err == nil {
			if time.Since(info.ModTime()) > 30*time.Second {
				os.Remove(lockPath)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Store) releaseFSLock() {
	os.Remove(s.path + ".lock")
}

// withLock executes the given function under a full write lock.
func (s *Store) withLock(fn func()) {
	s.mu.Lock()
	s.acquireFSLock()
	defer s.releaseFSLock()
	defer s.mu.Unlock()
	fn()
}

// withRLock executes the given function under a read lock.
func (s *Store) withRLock(fn func()) {
	s.mu.RLock()
	// Read lock also acquires FSLock in shared mode, but Mkdir is exclusive.
	// For simplicity, we just use exclusive for both since writes are rare.
	s.acquireFSLock()
	defer s.releaseFSLock()
	defer s.mu.RUnlock()
	fn()
}

// NewStore creates a store backed by the given JSON file.
func NewStore(path string) *Store {
	return &Store{path: path}
}

type storeFile struct {
	Hosts        []*model.Host           `json:"hosts"`
	Chains       []*model.Chain          `json:"chains"`
	Users        []*model.User           `json:"users,omitempty"`
	Settings     *model.PanelSettings    `json:"settings,omitempty"`
	NodeInfos    []*model.NodeInfo       `json:"node_infos,omitempty"`
	Metrics      []*model.NodeMetrics    `json:"metrics,omitempty"`
	KnownHosts   []*model.KnownHost      `json:"known_hosts,omitempty"`
	Profiles     []*model.Profile        `json:"profiles,omitempty"`
	Assignments  []*model.ClientAssignment `json:"assignments,omitempty"`
	RouteRules   []*model.RouteRule      `json:"route_rules,omitempty"`
	AuditLogs    []*model.AuditLog       `json:"audit_logs,omitempty"`
	MtproxyUsers []*model.MtproxyUser    `json:"mtproxy_users,omitempty"`
	Links        []*model.ConnectionLink `json:"links,omitempty"`
}

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
		return nil, fmt.Errorf("store: host %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("store: read: %w", err)
	}

	for _, h := range sf.Hosts {
		if h.ID == id {
			return h, nil
		}
	}
	return nil, fmt.Errorf("store: host %q not found", id)
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
		return fmt.Errorf("store: host %q not found", id)
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
		return fmt.Errorf("store: host %q not found", id)
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
		return nil, fmt.Errorf("store: chain %q not found", name)
	}
	if err != nil {
		return nil, fmt.Errorf("store: read: %w", err)
	}

	for _, c := range sf.Chains {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("store: chain %q not found", name)
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
		return fmt.Errorf("store: chain %q not found", name)
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
		return fmt.Errorf("store: chain %q not found", name)
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
	return s.writeStore(sf)
}

// GetUser returns a user by ID.
func (s *Store) GetUser(id string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sf, err := s.readStore()
	if err != nil {
		return nil, err
	}
	for _, u := range sf.Users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, fmt.Errorf("store: user %q not found", id)
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
		return fmt.Errorf("store: user %q not found", id)
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

// ─── Profiles ────────────────────────────────────────────────────────────────

// SaveProfile persists a profile (create or update by ID). Name must be unique.
func (s *Store) SaveProfile(p *model.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	if p.ID == "" {
		p.ID = newID()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = timeNow()
	}
	p.UpdatedAt = timeNow()

	// Enforce unique name (case-sensitive) across other profiles.
	for _, existing := range sf.Profiles {
		if existing.ID != p.ID && existing.Name == p.Name {
			return fmt.Errorf("store: profile name %q already exists", p.Name)
		}
	}

	replaced := false
	for i, ex := range sf.Profiles {
		if ex.ID == p.ID {
			sf.Profiles[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		sf.Profiles = append(sf.Profiles, p)
	}
	return s.writeStore(sf)
}

// GetProfile returns a profile by ID.
func (s *Store) GetProfile(id string) (*model.Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		return nil, err
	}
	for _, p := range sf.Profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("store: profile %q not found", id)
}

// ListProfiles returns all profiles.
func (s *Store) ListProfiles() ([]*model.Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return []*model.Profile{}, nil
		}
		return nil, err
	}
	return sf.Profiles, nil
}

// DeleteProfile removes a profile and its assignments (cascade).
func (s *Store) DeleteProfile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		return err
	}
	found := false
	for i, p := range sf.Profiles {
		if p.ID == id {
			sf.Profiles = append(sf.Profiles[:i], sf.Profiles[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("store: profile %q not found", id)
	}
	// Cascade-delete assignments pointing at this profile.
	kept := sf.Assignments[:0]
	for _, a := range sf.Assignments {
		if a.ProfileID != id {
			kept = append(kept, a)
		}
	}
	sf.Assignments = kept
	return s.writeStore(sf)
}

// ─── ClientAssignments ───────────────────────────────────────────────────────

// SaveAssignment creates a client assignment. Uniqueness of
// (profile_id, client_type, client_id) is enforced.
func (s *Store) SaveAssignment(a *model.ClientAssignment) error {
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
	if a.CreatedAt.IsZero() {
		a.CreatedAt = timeNow()
	}
	for _, ex := range sf.Assignments {
		if ex.ProfileID == a.ProfileID && ex.ClientType == a.ClientType && ex.ClientID == a.ClientID {
			return fmt.Errorf("store: assignment already exists")
		}
	}
	sf.Assignments = append(sf.Assignments, a)
	return s.writeStore(sf)
}

// ListAssignments returns all assignments.
func (s *Store) ListAssignments() ([]*model.ClientAssignment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return []*model.ClientAssignment{}, nil
		}
		return nil, err
	}
	return sf.Assignments, nil
}

// ListAssignmentsForProfile returns assignments for a given profile.
func (s *Store) ListAssignmentsForProfile(profileID string) ([]*model.ClientAssignment, error) {
	all, err := s.ListAssignments()
	if err != nil {
		return nil, err
	}
	var out []*model.ClientAssignment
	for _, a := range all {
		if a.ProfileID == profileID {
			out = append(out, a)
		}
	}
	return out, nil
}

// DeleteAssignment removes an assignment by ID.
func (s *Store) DeleteAssignment(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		return err
	}
	for i, a := range sf.Assignments {
		if a.ID == id {
			sf.Assignments = append(sf.Assignments[:i], sf.Assignments[i+1:]...)
			return s.writeStore(sf)
		}
	}
	return fmt.Errorf("store: assignment %q not found", id)
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

// ─── MtproxyUsers ────────────────────────────────────────────────────────────

// SaveMtproxyUser persists an MTProxy user (create or update by ID). Name must
// be unique per node.
func (s *Store) SaveMtproxyUser(u *model.MtproxyUser) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if os.IsNotExist(err) {
		sf = &storeFile{}
	} else if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}

	if u.ID == "" {
		u.ID = newID()
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = timeNow()
	}
	for _, ex := range sf.MtproxyUsers {
		if ex.ID != u.ID && ex.NodeID == u.NodeID && ex.Name == u.Name {
			return fmt.Errorf("store: mtproxy user %q already exists on node %q", u.Name, u.NodeID)
		}
	}
	replaced := false
	for i, ex := range sf.MtproxyUsers {
		if ex.ID == u.ID {
			sf.MtproxyUsers[i] = u
			replaced = true
			break
		}
	}
	if !replaced {
		sf.MtproxyUsers = append(sf.MtproxyUsers, u)
	}
	return s.writeStore(sf)
}

// ListMtproxyUsers returns all MTProxy users.
func (s *Store) ListMtproxyUsers() ([]*model.MtproxyUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return []*model.MtproxyUser{}, nil
		}
		return nil, err
	}
	return sf.MtproxyUsers, nil
}

// ListMtproxyUsersForNode returns MTProxy users for a node.
func (s *Store) ListMtproxyUsersForNode(nodeID string) ([]*model.MtproxyUser, error) {
	all, err := s.ListMtproxyUsers()
	if err != nil {
		return nil, err
	}
	var out []*model.MtproxyUser
	for _, u := range all {
		if u.NodeID == nodeID {
			out = append(out, u)
		}
	}
	return out, nil
}

// DeleteMtproxyUser removes an MTProxy user by ID.
func (s *Store) DeleteMtproxyUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		return err
	}
	for i, u := range sf.MtproxyUsers {
		if u.ID == id {
			sf.MtproxyUsers = append(sf.MtproxyUsers[:i], sf.MtproxyUsers[i+1:]...)
			return s.writeStore(sf)
		}
	}
	return fmt.Errorf("store: mtproxy user %q not found", id)
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

	// Atomic write (temp + rename) so a crash mid-write cannot truncate the
	// store file and lose the whole panel state (CTO-review M3).
	if err := atomicWriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("store: write: %w", err)
	}
	return nil
}
