//go:build nosqlite

package takeover

import "fmt"

// panel_db_stub.go — fallback for `-tags nosqlite` builds (the 32-bit MIPS
// router targets, where modernc.org/sqlite does not compile). Panel import is
// reported as unavailable; the detect + wipe paths still work (they don't need
// to parse the DB).

// ParsePanelDB reports that panel import is not supported in this build.
func ParsePanelDB(data []byte) (*PanelDB, error) {
	return nil, fmt.Errorf("panel import is not supported in this build (compiled with -tags nosqlite for a MIPS target)")
}
