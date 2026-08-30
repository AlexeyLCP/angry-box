package web

// handlers_backups_test.go — HTTP tests for the backup export/import endpoints
// (/ui/backup/store, /ui/backup/nodes/{id}, /ui/backup/import). Uses the
// testServer harness; no network.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// addStoredNode registers a host + node info + (optionally) a chain slot via
// the store so the backup handlers have something to export.
func addStoredNode(t *testing.T, ts *testServer, id, addr string) {
	t.Helper()
	st := ts.srv.store()
	st.SaveHost(&model.Host{ID: id, Addr: addr, User: "root", KeyPath: "/key"})
	st.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: id, Addr: addr, User: "root"}})
}

// TestHandler_ExportStoreBackup verifies GET /ui/backup/store returns an
// attachment with a valid store backup envelope.
func TestHandler_ExportStoreBackup(t *testing.T) {
	ts := newTestServer(t)
	addStoredNode(t, ts, "n1", "1.1.1.1:22")
	w := ts.post("/ui/backup/store", url.Values{})
	ts.assertStatus(w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type: got %q want application/octet-stream", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd == "" || !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition: got %q, want attachment", cd)
	}
	// Body must be a store-format backup the import handler would accept.
	if format, err := chain.DetectBackupFormat(w.Body.Bytes()); err != nil || format != chain.BackupFormatStore {
		t.Errorf("exported body is not a store backup: format=%q err=%v", format, err)
	}
}

// TestHandler_ExportNodeBackup verifies GET /ui/backup/nodes/{id} returns a
// node-format backup; 404 for an unknown ID.
func TestHandler_ExportNodeBackup(t *testing.T) {
	ts := newTestServer(t)
	addStoredNode(t, ts, "n1", "1.1.1.1:22")

	w := ts.post("/ui/backup/nodes/n1", url.Values{})
	ts.assertStatus(w, http.StatusOK)
	if cd := w.Header().Get("Content-Disposition"); cd == "" || !strings.Contains(cd, "angry-box-node-n1.json") {
		t.Errorf("Content-Disposition: got %q, want angry-box-node-n1.json attachment", cd)
	}
	if format, err := chain.DetectBackupFormat(w.Body.Bytes()); err != nil || format != chain.BackupFormatNode {
		t.Errorf("exported body is not a node backup: format=%q err=%v", format, err)
	}

	// Unknown ID → 404.
	w2 := ts.post("/ui/backup/nodes/ghost", url.Values{})
	ts.assertStatus(w2, http.StatusNotFound)
}

// TestHandler_ImportBackup_NodeRoundtrip verifies a node export → import
// roundtrip through the HTTP endpoints: export n1, import it onto a fresh
// server (pre-creating the chain stub), the host reappears.
func TestHandler_ImportBackup_NodeRoundtrip(t *testing.T) {
	src := newTestServer(t)
	addStoredNode(t, src, "n1", "1.1.1.1:22")
	src.srv.store().SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{{ID: "n1"}}})

	w := src.post("/ui/backup/nodes/n1", url.Values{})
	ts2 := newTestServer(t)
	ts2.srv.store().SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{{ID: "n1"}}})
	w2 := ts2.post("/ui/backup/import", url.Values{"backup_json": {w.Body.String()}})
	ts2.assertStatus(w2, http.StatusOK)
	ts2.assertContains(w2, "Node imported")
	if h, err := ts2.srv.store().GetHost("n1"); err != nil || h.Addr != "1.1.1.1:22" {
		t.Errorf("import did not restore host: %+v %v", h, err)
	}
}

// TestHandler_ImportBackup_StoreRoundtrip verifies a store export → import
// roundtrip restores hosts onto a fresh server.
func TestHandler_ImportBackup_StoreRoundtrip(t *testing.T) {
	src := newTestServer(t)
	addStoredNode(t, src, "n1", "1.1.1.1:22")
	addStoredNode(t, src, "n2", "2.2.2.2:22")

	w := src.post("/ui/backup/store", url.Values{})
	ts2 := newTestServer(t)
	w2 := ts2.post("/ui/backup/import", url.Values{"backup_json": {w.Body.String()}})
	ts2.assertStatus(w2, http.StatusOK)
	ts2.assertContains(w2, "Store imported")
	if h, err := ts2.srv.store().GetHost("n1"); err != nil || h.Addr != "1.1.1.1:22" {
		t.Errorf("import did not restore n1: %+v %v", h, err)
	}
	if h, err := ts2.srv.store().GetHost("n2"); err != nil || h.Addr != "2.2.2.2:22" {
		t.Errorf("import did not restore n2: %+v %v", h, err)
	}
}

// TestHandler_ImportBackup_RefusesNonEmptyStore verifies importing a store
// backup onto a non-empty server without force is refused (wipe protection).
func TestHandler_ImportBackup_RefusesNonEmptyStore(t *testing.T) {
	src := newTestServer(t)
	addStoredNode(t, src, "n1", "1.1.1.1:22")
	payload := src.post("/ui/backup/store", url.Values{}).Body.String()

	dst := newTestServer(t)
	addStoredNode(t, dst, "existing", "9.9.9.9:22") // non-empty target
	w := dst.post("/ui/backup/import", url.Values{"backup_json": {payload}}) // no force
	ts := dst
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
	// Existing host preserved (no wipe).
	if h, err := dst.srv.store().GetHost("existing"); err != nil || h == nil {
		t.Errorf("non-empty target was wiped without force: %+v %v", h, err)
	}
}

// TestHandler_ImportBackup_InvalidJSON verifies a non-backup payload is
// rejected with an error alert, not a 500.
func TestHandler_ImportBackup_InvalidJSON(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/backup/import", url.Values{"backup_json": {"not a backup"}})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alert-error")
}

// TestHandler_ImportBackup_EmptyPayload verifies an empty payload is rejected
// with the "backup json is required" alert (en value "backup JSON is required").
func TestHandler_ImportBackup_EmptyPayload(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/backup/import", url.Values{"backup_json": {""}})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "backup JSON is required")
}

// TestHandler_ImportBackup_ForceOverwritesStore verifies force=on lets a store
// import overwrite a non-empty target.
func TestHandler_ImportBackup_ForceOverwritesStore(t *testing.T) {
	src := newTestServer(t)
	addStoredNode(t, src, "n1", "1.1.1.1:22")
	payload := src.post("/ui/backup/store", url.Values{}).Body.String()

	dst := newTestServer(t)
	addStoredNode(t, dst, "existing", "9.9.9.9:22")
	w := dst.post("/ui/backup/import", url.Values{"backup_json": {payload}, "force": {"on"}})
	ts := dst
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Store imported")
	// force overwrote: existing is gone, n1 is present.
	if _, err := dst.srv.store().GetHost("existing"); err == nil {
		t.Errorf("force import did not overwrite (existing host still present)")
	}
	if h, err := dst.srv.store().GetHost("n1"); err != nil || h.Addr != "1.1.1.1:22" {
		t.Errorf("force import did not restore n1: %+v %v", h, err)
	}
}