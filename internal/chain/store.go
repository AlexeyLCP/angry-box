package chain

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// Store provides JSON-file persistence for hosts and chains.
type Store struct {
	mu   sync.Mutex
	path string
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
	s.mu.Lock()
	defer s.mu.Unlock()

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
	s.mu.Lock()
	defer s.mu.Unlock()

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
	s.mu.Lock()
	defer s.mu.Unlock()

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
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: read: %w", err)
	}
	return sf.Chains, nil
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
		resolved = append(resolved, model.ChainNode{
			ID:      host.ID,
			Addr:    host.Addr,
			User:    host.User,
			KeyPath: host.KeyPath,
			Port:    n.Port,
		})
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if err != nil {
		if os.IsNotExist(err) {
			return &model.PanelSettings{MetricsInterval: 240}, nil
		}
		return nil, err
	}
	if sf.Settings == nil {
		return &model.PanelSettings{MetricsInterval: 240}, nil
	}
	if sf.Settings.MetricsInterval <= 0 {
		sf.Settings.MetricsInterval = 240
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
