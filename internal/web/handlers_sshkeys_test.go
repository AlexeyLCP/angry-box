package web

// handlers_sshkeys_test.go — tests for the Settings SSH-key handlers added in
// plan Task 7: Add (fingerprint), Set-default, Test, Import-from-~/.ssh,
// Export/Import registry.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/ssh"
)

// addStoredKey generates a real ed25519 keypair, POSTs it to the add-key
// endpoint, and returns the new stored key's registry ID. Asserts the POST
// succeeded so callers can proceed against the saved registry.
func addStoredKey(t *testing.T, ts *testServer, name string) string {
	t.Helper()
	priv, _, err := ssh.GenerateSSHKeypair()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	form := url.Values{"name": {name}, "key_data": {priv}}
	w := ts.post("/ui/settings/ssh-keys", form)
	ts.assertStatus(w, http.StatusOK)
	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	for _, k := range settings.SSHKeys {
		if k.Name == name {
			return k.ID
		}
	}
	t.Fatalf("key %q not saved in registry", name)
	return ""
}

// jsonString marshals s to a valid JSON string literal (used to embed a PEM
// blob inside a hand-built JSON array in TestHandler_ImportKeys).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestHandler_AddSSHKey_ComputesFingerprint(t *testing.T) {
	ts := newTestServer(t)
	id := addStoredKey(t, ts, "test-key")
	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	for _, k := range settings.SSHKeys {
		if k.ID == id {
			if k.Fingerprint == "" {
				t.Errorf("fingerprint not computed")
			}
			if k.Source != model.SourceStored {
				t.Errorf("source: got %q want %q", k.Source, model.SourceStored)
			}
			return
		}
	}
	t.Fatalf("key %s not found in registry", id)
}

func TestHandler_SetDefaultKey(t *testing.T) {
	ts := newTestServer(t)
	id := addStoredKey(t, ts, "def-key")
	w := ts.post("/ui/settings/default-key", url.Values{"ssh_key_id": {id}})
	ts.assertStatus(w, http.StatusOK)
	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	if settings.DefaultSSHKeyID != id {
		t.Errorf("default not set: got %q want %q", settings.DefaultSSHKeyID, id)
	}
}

func TestHandler_SetDefaultKey_RejectsStaleID(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/settings/default-key", url.Values{"ssh_key_id": {"key-nope"}})
	ts.assertStatus(w, http.StatusBadRequest)
}

func TestHandler_ExportKeys(t *testing.T) {
	ts := newTestServer(t)
	addStoredKey(t, ts, "exp-key")
	w := ts.get("/ui/settings/ssh-keys/export")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "exp-key")
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type: got %q want application/octet-stream", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd == "" {
		t.Errorf("Content-Disposition header missing")
	}
}

func TestHandler_ImportKeys(t *testing.T) {
	ts := newTestServer(t)
	priv, _, _ := ssh.GenerateSSHKeypair()
	incoming := `[{"id":"key-x","name":"X","key_data":` + jsonString(priv) + `,"source":"stored"}]`
	w := ts.post("/ui/settings/ssh-keys/import", url.Values{"keys_json": {incoming}})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Imported")
	// Verify the key was actually persisted.
	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	found := false
	for _, k := range settings.SSHKeys {
		if k.ID == "key-x" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("imported key key-x not persisted in registry")
	}
}