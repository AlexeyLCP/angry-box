//go:build !nosqlite

package takeover

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// panel_db_sqlite.go — the real SQLite-backed panel DB parser. Built by
// default; excluded with `-tags nosqlite` on the 32-bit MIPS router targets
// (modernc.org/libc has no 32-bit MIPS support — the build fails with "build
// constraints exclude all Go files"). Those builds use the panel_db_stub.go
// fallback, which reports panel import as unavailable.

// ParsePanelDB parses the raw SQLite bytes of a 3x-ui/lucx-ui panel DB. The
// bytes are written to a temp file (modernc.org/sqlite is file-backed) and
// removed afterwards. Both panels share the schema: table `inbounds`
// (protocol/port/settings/stream_settings JSON), table `client_traffics`
// (per-email usage), table `settings` (key/value — the xrayTemplateConfig blob
// carries the routing rules).
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
