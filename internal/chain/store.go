package chain

import (
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
	Hosts    []*model.Host          `json:"hosts"`
	Chains   []*model.Chain         `json:"chains"`
	Users    []*model.User          `json:"users,omitempty"`
	Settings *model.PanelSettings   `json:"settings,omitempty"`
	NodeInfos []*model.NodeInfo     `json:"node_infos,omitempty"`
	Metrics  []*model.NodeMetrics   `json:"metrics,omitempty"`
	KnownHosts []*model.KnownHost   `json:"known_hosts,omitempty"`
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

	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("store: write: %w", err)
	}
	return nil
}
