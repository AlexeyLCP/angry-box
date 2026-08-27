package takeover

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// panel_db.go — parses a 3x-ui/lucx-ui panel SQLite DB (pulled over SSH into
// the orchestrator; weak nodes never run the parser). Both panels share the
// schema: table `inbounds` (protocol/port/settings/stream_settings JSON),
// table `client_traffics` (per-email usage), table `settings` (key/value —
// the xrayTemplateConfig blob carries the routing rules).

// PanelInbound is one row of the panel's `inbounds` table.
type PanelInbound struct {
	ID             int64
	Remark         string
	Port           int
	Protocol       string
	Enable         bool
	Settings       string // JSON: {"clients":[...], ...}
	StreamSettings string // JSON: {"network","security","realitySettings",...}
}

// PanelClientTraffic is one row of `client_traffics` (bytes; ms epochs).
type PanelClientTraffic struct {
	Email        string
	Up           int64
	Down         int64
	Total        int64 // limit, 0 = unlimited
	ExpiryTime   int64 // ms epoch, 0 = unlimited
	LastSubFetch int64 // ms epoch, 0 = never
}

// PanelDB is the parsed slice of the panel database angry-box imports.
type PanelDB struct {
	Inbounds    []PanelInbound
	Traffics    map[string]PanelClientTraffic // keyed by email
	RoutingJSON string                        // xrayTemplateConfig (routing.rules live inside)
}

// ParsePanelDB parses the raw SQLite bytes. The bytes are written to a temp
// file (modernc.org/sqlite is file-backed) and removed afterwards.
func ParsePanelDB(data []byte) (*PanelDB, error) {
	tmp, err := os.CreateTemp("", "ab-panel-*.db")
	if err != nil {
		return nil, fmt.Errorf("panel db: temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("panel db: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("panel db: close temp: %w", err)
	}

	db, err := sql.Open("sqlite", tmp.Name())
	if err != nil {
		return nil, fmt.Errorf("panel db: open: %w", err)
	}
	defer db.Close()

	out := &PanelDB{Traffics: map[string]PanelClientTraffic{}}

	rows, err := db.Query(`SELECT id, remark, port, protocol, enable, settings, stream_settings FROM inbounds`)
	if err != nil {
		return nil, fmt.Errorf("panel db: inbounds: %w", err)
	}
	for rows.Next() {
		var pi PanelInbound
		var remark, settings, stream sql.NullString
		var enable sql.NullBool
		if err := rows.Scan(&pi.ID, &remark, &pi.Port, &pi.Protocol, &enable, &settings, &stream); err != nil {
			rows.Close()
			return nil, fmt.Errorf("panel db: scan inbound: %w", err)
		}
		pi.Remark = remark.String
		pi.Enable = enable.Bool
		pi.Settings = settings.String
		pi.StreamSettings = stream.String
		out.Inbounds = append(out.Inbounds, pi)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("panel db: inbounds rows: %w", err)
	}

	if trows, err := db.Query(`SELECT email, up, down, total, expiry_time, last_sub_fetch FROM client_traffics`); err == nil {
		for trows.Next() {
			var ct PanelClientTraffic
			if err := trows.Scan(&ct.Email, &ct.Up, &ct.Down, &ct.Total, &ct.ExpiryTime, &ct.LastSubFetch); err == nil && ct.Email != "" {
				out.Traffics[ct.Email] = ct
			}
		}
		trows.Close()
	} // client_traffics missing = usage simply not imported

	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'xrayTemplateConfig'`).Scan(&out.RoutingJSON); err != nil {
		out.RoutingJSON = "" // no template configured — nothing to import
	}
	return out, nil
}

// ─── settings/stream JSON shapes (the lucx-ui/3x-ui conventions) ─────────────

// panelClient is the common client object inside inbound settings.clients[]
// (all protocols share this shape; protocol-specific credential fields like
// mtproto's secret are read separately).
type panelClient struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Enable     bool   `json:"enable"`
	ExpiryTime int64  `json:"expiryTime"` // ms epoch, 0 = unlimited
	TotalGB    int64  `json:"totalGB"`    // bytes, 0 = unlimited
	TgID       int64  `json:"tgId"`
	SubID      string `json:"subId"`
	Flow       string `json:"flow"`
	Secret     string `json:"secret"` // mtproto FakeTLS secret ("ee"+hex)
}

// panelSettings is the inbound settings JSON envelope.
type panelSettings struct {
	Clients       []panelClient `json:"clients"`
	FakeTLSDomain string        `json:"fakeTlsDomain"` // mtproto
}

// panelStream is the inbound stream_settings JSON envelope.
type panelStream struct {
	Network  string `json:"network"`
	Security string `json:"security"`
	Reality  *struct {
		PrivateKey string   `json:"privateKey"`
		ShortIDs   []string `json:"shortIds"`
		ServerName []string `json:"serverNames"`
		Target     string   `json:"target"`
		Settings   *struct {
			PublicKey string `json:"publicKey"`
		} `json:"settings"`
	} `json:"realitySettings"`
}

// parsePanelClients extracts the client list from an inbound's settings JSON.
func parsePanelClients(settingsJSON string) []panelClient {
	var s panelSettings
	if err := json.Unmarshal([]byte(settingsJSON), &s); err != nil {
		return nil
	}
	return s.Clients
}

// parsePanelStream extracts the stream settings (network/security/reality).
func parsePanelStream(streamJSON string) *panelStream {
	var s panelStream
	if err := json.Unmarshal([]byte(streamJSON), &s); err != nil {
		return nil
	}
	return &s
}

// parseMTProtoSecret splits an "ee<32 hex><hex(domain)>" FakeTLS secret into
// the raw 32-hex secret + the decoded domain. Returns ok=false when the shape
// does not match.
func parseMTProtoSecret(full string) (secretHex, domain string, ok bool) {
	full = strings.TrimSpace(full)
	if !strings.HasPrefix(full, "ee") || len(full) < 2+32 {
		return "", "", false
	}
	secretHex = full[2 : 2+32]
	domHex := full[2+32:]
	domBytes, err := hex.DecodeString(domHex)
	if err != nil || len(domBytes) == 0 {
		return secretHex, "", true
	}
	return secretHex, string(domBytes), true
}
