package chain

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

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
	Hosts  []*model.Host  `json:"hosts"`
	Chains []*model.Chain `json:"chains"`
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
