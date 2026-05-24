package store

import "time"

// Server represents a managed remote host.
type Server struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	Username   string     `json:"username"`
	AuthMethod string     `json:"auth_method"`
	Credential string     `json:"-"` // never serialized
	OS         string     `json:"os"`
	Arch       string     `json:"arch"`
	Status     string     `json:"status"`
	Source     string     `json:"source"`
	Tags       string     `json:"tags"`
	LastSeen   *time.Time `json:"last_seen"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ServerBackend represents a backend service installed on a server
// (e.g. xray, amnezia-wg, telemetry-agent).
type ServerBackend struct {
	ServerID      string `json:"server_id"`
	BackendType   string `json:"backend_type"`
	Version       string `json:"version"`
	Status        string `json:"status"`
	ConfigPath    string `json:"config_path"`
	APIEndpoint   string `json:"api_endpoint"`
	ConfigManaged bool   `json:"config_managed"`
}

// CreateServer inserts a new server row.
func (s *Store) CreateServer(srv *Server) error {
	_, err := s.db.Exec(`
		INSERT INTO servers (id, name, host, port, username, auth_method, credential, os, arch, status, source, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		srv.ID, srv.Name, srv.Host, srv.Port, srv.Username, srv.AuthMethod, srv.Credential,
		srv.OS, srv.Arch, srv.Status, srv.Source, srv.Tags)
	return err
}

// GetServer returns a single server by id, or sql.ErrNoRows.
// NOTE: Includes credential in the result — caller must not expose it (json:"-" on field).
func (s *Store) GetServer(id string) (*Server, error) {
	srv := &Server{}
	err := s.db.QueryRow(`SELECT id, name, host, port, username, auth_method, credential, os, arch, status, source, tags, last_seen, created_at FROM servers WHERE id = ?`, id).
		Scan(&srv.ID, &srv.Name, &srv.Host, &srv.Port, &srv.Username, &srv.AuthMethod, &srv.Credential, &srv.OS, &srv.Arch, &srv.Status, &srv.Source, &srv.Tags, &srv.LastSeen, &srv.CreatedAt)
	if err != nil {
		return nil, err
	}
	return srv, nil
}

// ListServers returns all servers ordered by creation time (newest first).
func (s *Store) ListServers() ([]Server, error) {
	rows, err := s.db.Query(`SELECT id, name, host, port, username, auth_method, os, arch, status, source, tags, last_seen, created_at FROM servers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.ID, &srv.Name, &srv.Host, &srv.Port, &srv.Username, &srv.AuthMethod, &srv.OS, &srv.Arch, &srv.Status, &srv.Source, &srv.Tags, &srv.LastSeen, &srv.CreatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, srv)
	}
	return servers, rows.Err()
}

// UpdateServerStatus updates the status and last_seen timestamp of a server.
func (s *Store) UpdateServerStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE servers SET status = ?, last_seen = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	return err
}

// DeleteServer removes a server by id.
func (s *Store) DeleteServer(id string) error {
	_, err := s.db.Exec(`DELETE FROM servers WHERE id = ?`, id)
	return err
}

// UpsertServerBackend inserts or updates a server_backend row.
func (s *Store) UpsertServerBackend(sb *ServerBackend) error {
	_, err := s.db.Exec(`
		INSERT INTO server_backends (server_id, backend_type, version, status, config_path, api_endpoint, config_managed, installed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(server_id, backend_type) DO UPDATE SET
			version = excluded.version, status = excluded.status,
			config_path = excluded.config_path, api_endpoint = excluded.api_endpoint,
			config_managed = excluded.config_managed`,
		sb.ServerID, sb.BackendType, sb.Version, sb.Status, sb.ConfigPath, sb.APIEndpoint, sb.ConfigManaged)
	return err
}

// GetServerBackends returns all backends for a given server.
func (s *Store) GetServerBackends(serverID string) ([]ServerBackend, error) {
	rows, err := s.db.Query(`SELECT server_id, backend_type, version, status, config_path, api_endpoint, config_managed FROM server_backends WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var backends []ServerBackend
	for rows.Next() {
		var sb ServerBackend
		if err := rows.Scan(&sb.ServerID, &sb.BackendType, &sb.Version, &sb.Status, &sb.ConfigPath, &sb.APIEndpoint, &sb.ConfigManaged); err != nil {
			return nil, err
		}
		backends = append(backends, sb)
	}
	return backends, rows.Err()
}
