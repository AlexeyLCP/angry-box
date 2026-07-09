package web

// handlers_offsite_test.go — covers the P2a offsite backup handlers: save
// target config (POST /ui/backup/offsite/save), backup-now (POST
// /ui/backup/offsite/now), and restore from an encrypted ABBKP1 blob (POST
// /ui/backup/import with passphrase). Uses the webFakeConnector so no real SSH.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// seedOffsiteConfig writes a complete offsite target config to the store.
func seedOffsiteConfig(t *testing.T, ts *testServer) {
	t.Helper()
	st := ts.srv.store()
	settings, _ := st.GetSettings()
	settings.OffsiteBackup = &model.OffsiteBackupConfig{
		Enabled: true, Host: "bk.example:22", User: "bk", SSHKeyID: "k1",
		RemotePath: "/home/bk/ab.abbkp", Passphrase: "s3cret",
	}
	if err := st.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
}

// TestHandler_SaveOffsite_PersistsConfig — posting the offsite form saves the
// config (enabled + host + passphrase) to the store, leaving other settings.
func TestHandler_SaveOffsite_PersistsConfig(t *testing.T) {
	ts := newTestServer(t)
	// Set a non-offsite field so we can prove it is NOT clobbered by the offsite save.
	st := ts.srv.store()
	s, _ := st.GetSettings()
	s.PanelCountry = "RU"
	st.SaveSettings(s)

	form := url.Values{
		"offsite_enabled":     {"on"},
		"offsite_host":        {"backup.example:22"},
		"offsite_user":        {"backup"},
		"offsite_ssh_key_id":  {"k1"},
		"offsite_remote_path": {"/b/ab.abbkp"},
		"offsite_passphrase":  {"mypass"},
		"offsite_interval":    {"120"},
	}
	w := ts.post("/ui/backup/offsite/save", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Offsite backup settings saved")

	got, _ := ts.srv.store().GetSettings()
	if got.OffsiteBackup == nil || !got.OffsiteBackup.Enabled || got.OffsiteBackup.Host != "backup.example:22" {
		t.Fatalf("offsite config not persisted: %+v", got.OffsiteBackup)
	}
	if got.OffsiteBackup.Passphrase != "mypass" {
		t.Fatalf("passphrase = %q, want mypass", got.OffsiteBackup.Passphrase)
	}
	if got.PanelCountry != "RU" {
		t.Fatal("offsite save clobbered PanelCountry (must only touch offsite)")
	}
}

// TestHandler_SaveOffsite_EmptyHostDisables — a blank host flips Enabled off
// (without deleting the record, preserving LastBackupAt history).
func TestHandler_SaveOffsite_EmptyHostDisables(t *testing.T) {
	ts := newTestServer(t)
	seedOffsiteConfig(t, ts)

	w := ts.post("/ui/backup/offsite/save", url.Values{"offsite_host": {""}})
	ts.assertStatus(w, http.StatusOK)

	got, _ := ts.srv.store().GetSettings()
	if got.OffsiteBackup == nil || got.OffsiteBackup.Enabled {
		t.Fatalf("expected disabled, got %+v", got.OffsiteBackup)
	}
}

// TestHandler_BackupNow_NotConfigured — backup now without a config → 400.
func TestHandler_BackupNow_NotConfigured(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/backup/offsite/now", url.Values{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (not configured)", w.Code)
	}
}

// TestHandler_BackupNow_OK — with a configured target + fake SSH, the push
// succeeds (200 + success alert) and the upload is recorded.
func TestHandler_BackupNow_OK(t *testing.T) {
	fake := newWebFakeSSH()
	ts := newTestServerWithConnector(t, &webFakeConnector{client: fake})
	seedOffsiteConfig(t, ts)

	w := ts.post("/ui/backup/offsite/now", url.Values{})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Backup sent to offsite target")

	if len(fake.uploads) != 1 {
		t.Fatalf("uploads = %v, want 1", fake.uploads)
	}
	// RemotePath is now a directory; the blob is written to
	// <RemotePath>/angry-box-<timestamp>.abbkp.
	if path := fake.uploads[0]; !strings.HasPrefix(path, "/home/bk/ab.abbkp/angry-box-") || !strings.HasSuffix(path, ".abbkp") {
		t.Fatalf("upload path = %q, want <dir>/angry-box-<ts>.abbkp", path)
	}
	// LastBackupAt should now be stamped in the persisted config.
	got, _ := ts.srv.store().GetSettings()
	if got.OffsiteBackup == nil || got.OffsiteBackup.LastBackupAt.IsZero() {
		t.Fatal("LastBackupAt not stamped after a successful backup now")
	}
}

// TestHandler_BackupNow_ConnectFails — a failing connector surfaces an error
// alert (not a 500 panic) and does not stamp LastBackupAt.
func TestHandler_BackupNow_ConnectFails(t *testing.T) {
	ts := newTestServerWithConnector(t, &webFakeConnector{err: errLine("ssh: dial refused")})
	seedOffsiteConfig(t, ts)

	w := ts.post("/ui/backup/offsite/now", url.Values{})
	ts.assertStatus(w, http.StatusOK) // alert rendered, not a 500
	ts.assertContains(w, "Backup failed")

	got, _ := ts.srv.store().GetSettings()
	if !got.OffsiteBackup.LastBackupAt.IsZero() {
		t.Fatal("LastBackupAt stamped despite a failed push")
	}
}

// TestHandler_ImportBackup_EncryptedRestore — pasting an ABBKP1 blob + the
// passphrase restores the store (the blob decrypts to a store backup, which
// ImportStore replaces the panel with).
func TestHandler_ImportBackup_EncryptedRestore(t *testing.T) {
	ts := newTestServer(t)
	// Build an encrypted blob from a store that has a host "restored-host".
	src := newTestStore(t)
	src.SaveHost(&model.Host{ID: "restored-host", Addr: "9.9.9.9:22", User: "root", KeyPath: "/k"})
	plain, err := src.ExportStore()
	if err != nil {
		t.Fatalf("ExportStore: %v", err)
	}
	blob, err := chain.EncryptBackup(plain, "the-pass")
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}

	form := url.Values{
		"backup_json": {string(blob)},
		"passphrase":  {"the-pass"},
		"force":       {"on"},
	}
	w := ts.post("/ui/backup/import", form)
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Store imported")

	// The restored host must now be in the live store.
	if h, err := ts.srv.store().GetHost("restored-host"); err != nil || h == nil {
		t.Fatalf("restored-host not present after import: %v", err)
	}
}

// TestHandler_ImportBackup_EncryptedWrongPassphrase — wrong passphrase → error
// alert, store unchanged.
func TestHandler_ImportBackup_EncryptedWrongPassphrase(t *testing.T) {
	ts := newTestServer(t)
	blob, _ := chain.EncryptBackup([]byte(`{"store":{}}`), "right")

	w := ts.post("/ui/backup/import", url.Values{
		"backup_json": {string(blob)},
		"passphrase":  {"wrong"},
	})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "import failed")
}

// TestHandler_ImportBackup_EncryptedNoPassphrase — ABBKP1 blob without a
// passphrase field → error alert.
func TestHandler_ImportBackup_EncryptedNoPassphrase(t *testing.T) {
	ts := newTestServer(t)
	blob, _ := chain.EncryptBackup([]byte(`{"store":{}}`), "right")

	w := ts.post("/ui/backup/import", url.Values{"backup_json": {string(blob)}})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "passphrase is required")
}

// errLine is a trivial error for the connector-failure tests.
type errLine string

func (e errLine) Error() string { return string(e) }

// newTestStore builds a chain.Store backed by a temp file (for building a
// source store to encrypt into a restore blob). Local to this test file.
func newTestStore(t *testing.T) *chain.Store {
	t.Helper()
	return chain.NewStore(t.TempDir() + "/store.json")
}