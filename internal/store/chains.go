package store

import "time"

type Chain struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Status    string      `json:"status"`
	AppliedAt *time.Time  `json:"applied_at"`
	CreatedAt time.Time   `json:"created_at"`
	Nodes     []ChainNode `json:"nodes,omitempty"`
}

type ChainNode struct {
	ChainID        string `json:"chain_id"`
	ServerID       string `json:"server_id"`
	BackendType    string `json:"backend_type"`
	Protocol       string `json:"protocol"`
	Position       int    `json:"position"`
	Role           string `json:"role"`
	InboundSpec    string `json:"inbound_spec"`
	HopInboundSpec string `json:"hop_inbound_spec"`
	OutboundSpec   string `json:"outbound_spec"`
	InboundResult  string `json:"inbound_result"`
	OutboundResult string `json:"outbound_result"`
}

func (s *Store) CreateChain(c *Chain) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO chains (id, name, status) VALUES (?, ?, ?)`, c.ID, c.Name, c.Status)
	if err != nil {
		return err
	}
	for _, n := range c.Nodes {
		_, err = tx.Exec(`INSERT INTO chain_nodes (chain_id, server_id, backend_type, protocol, position, role, inbound_spec, hop_inbound_spec, outbound_spec, inbound_result, outbound_result) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			n.ChainID, n.ServerID, n.BackendType, n.Protocol, n.Position, n.Role, n.InboundSpec, n.HopInboundSpec, n.OutboundSpec, n.InboundResult, n.OutboundResult)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetChain(id string) (*Chain, error) {
	c := &Chain{}
	err := s.db.QueryRow(`SELECT id, name, status, applied_at, created_at FROM chains WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &c.Status, &c.AppliedAt, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT chain_id, server_id, backend_type, protocol, position, role, inbound_spec, hop_inbound_spec, outbound_spec, inbound_result, outbound_result FROM chain_nodes WHERE chain_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n ChainNode
		if err := rows.Scan(&n.ChainID, &n.ServerID, &n.BackendType, &n.Protocol, &n.Position, &n.Role, &n.InboundSpec, &n.HopInboundSpec, &n.OutboundSpec, &n.InboundResult, &n.OutboundResult); err != nil {
			return nil, err
		}
		c.Nodes = append(c.Nodes, n)
	}
	return c, rows.Err()
}

func (s *Store) ListChains() ([]Chain, error) {
	rows, err := s.db.Query(`SELECT id, name, status, applied_at, created_at FROM chains ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chains := make([]Chain, 0)
	for rows.Next() {
		var c Chain
		if err := rows.Scan(&c.ID, &c.Name, &c.Status, &c.AppliedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		chains = append(chains, c)
	}
	return chains, rows.Err()
}

func (s *Store) UpdateChainStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE chains SET status = ?, applied_at = CASE WHEN ? = 'active' THEN CURRENT_TIMESTAMP ELSE applied_at END WHERE id = ?`, status, status, id)
	return err
}

func (s *Store) UpdateChainNodeResult(chainID string, position int, inboundResult, outboundResult string) error {
	_, err := s.db.Exec(`UPDATE chain_nodes SET inbound_result = ?, outbound_result = ? WHERE chain_id = ? AND position = ?`, inboundResult, outboundResult, chainID, position)
	return err
}

func (s *Store) UpdateChainNode(chainID string, position int, inboundSpec, hopInboundSpec string) error {
	if hopInboundSpec != "" {
		_, err := s.db.Exec(`UPDATE chain_nodes SET inbound_spec = ?, hop_inbound_spec = ? WHERE chain_id = ? AND position = ?`,
			inboundSpec, hopInboundSpec, chainID, position)
		return err
	}
	_, err := s.db.Exec(`UPDATE chain_nodes SET inbound_spec = ? WHERE chain_id = ? AND position = ?`,
		inboundSpec, chainID, position)
	return err
}

func (s *Store) GetChainNode(chainID string, position int) (*ChainNode, error) {
	n := &ChainNode{}
	err := s.db.QueryRow(`SELECT chain_id, server_id, backend_type, protocol, position, role, inbound_spec, hop_inbound_spec, outbound_spec, inbound_result, outbound_result FROM chain_nodes WHERE chain_id = ? AND position = ?`, chainID, position).
		Scan(&n.ChainID, &n.ServerID, &n.BackendType, &n.Protocol, &n.Position, &n.Role, &n.InboundSpec, &n.HopInboundSpec, &n.OutboundSpec, &n.InboundResult, &n.OutboundResult)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Store) DeleteChain(id string) error {
	_, err := s.db.Exec(`DELETE FROM chains WHERE id = ?`, id)
	return err
}
