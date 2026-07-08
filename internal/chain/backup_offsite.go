package chain

// backup_offsite.go — the offsite backup push (P2a). Runs one backup cycle:
// ExportStore (plaintext) → EncryptBackup (passphrase-derived key) → SSH
// UploadText to the offsite target → stamp LastBackupAt. The master-key file
// is NEVER read or transmitted here — ExportStore already returns decrypted
// plaintext in-process, and the offsite layer re-encrypts only with the
// passphrase (see backup_crypto.go). This is the security boundary: losing
// the offsite target compromises nothing without the passphrase; losing the
// passphrase compromises only the offsite blobs, not the live at-rest store.
//
// PushOffsiteBackup does not own scheduling — the periodic loop
// (web/server.go StartOffsiteBackupLoop) and the "Backup now" handler both
// call this. It returns an error so the caller can audit + surface the result.

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// DefaultOffsiteBackupInterval is the backup loop interval when
// OffsiteBackupConfig.IntervalMin is 0 (360 min = 6h).
const DefaultOffsiteBackupInterval = 360

// PushOffsiteBackup runs one encrypted offsite backup cycle against cfg.
// It reads the current store via ExportStore (plaintext, in-process decrypt),
// encrypts with cfg.Passphrase, and pushes the blob to cfg.Host:cfg.RemotePath
// over SSH (key resolved by cfg.SSHKeyID through the registry). On success it
// stamps cfg.LastBackupAt and persists settings. Returns an error so the
// caller can audit + alert; the store is never modified on a push failure
// (only LastBackupAt, and only on success).
//
// connector may be nil — callers that only want the encrypt step (e.g. a
// future "download encrypted blob" without pushing) can pass nil and a
// non-empty blob is returned alongside the (nil) error. For the normal push
// path connector must be non-nil.
func PushOffsiteBackup(ctx context.Context, store *Store, cfg *model.OffsiteBackupConfig, connector ports.SSHConnector) error {
	if store == nil || cfg == nil {
		return fmt.Errorf("backup: store or config is nil")
	}
	if cfg.Host == "" || cfg.RemotePath == "" {
		return fmt.Errorf("backup: offsite target not configured (host/path empty)")
	}
	if cfg.Passphrase == "" {
		return fmt.Errorf("backup: passphrase not set")
	}
	if connector == nil {
		return fmt.Errorf("backup: SSH connector is nil")
	}

	// 1. Export plaintext store (in-process; master key never enters this path).
	plain, err := store.ExportStore()
	if err != nil {
		return fmt.Errorf("backup: export store: %w", err)
	}

	// 2. Encrypt with the passphrase-derived key (scrypt + AES-256-GCM).
	blob, err := EncryptBackup(plain, cfg.Passphrase)
	if err != nil {
		return fmt.Errorf("backup: encrypt: %w", err)
	}

	// 3. Push over SSH. SSHKeyID is resolved by the connector's key resolver
	//    (the same registry nodes use). User defaults to the current SSH user
	//    if empty (the connector / ssh.Connect handles "" as the OS user).
	user := cfg.User
	keyRef := cfg.SSHKeyID
	client, err := connector.Connect(cfg.Host, user, keyRef)
	if err != nil {
		return fmt.Errorf("backup: ssh connect %s: %w", cfg.Host, err)
	}
	defer client.Close()

	if err := client.UploadText(ctx, string(blob), cfg.RemotePath, 0o600); err != nil {
		return fmt.Errorf("backup: upload to %s:%s: %w", cfg.Host, cfg.RemotePath, err)
	}

	// 4. Stamp LastBackupAt + persist settings (best-effort; a failed save
	//    does not undo the successful push — the blob is already off-host).
	cfg.LastBackupAt = time.Now().UTC()
	if settings, gerr := store.GetSettings(); gerr == nil {
		settings.OffsiteBackup = cfg
		if serr := store.SaveSettings(settings); serr != nil {
			log.Printf("backup: pushed to %s but failed to persist LastBackupAt: %v", cfg.Host, serr)
		}
	} else {
		log.Printf("backup: pushed to %s but failed to read settings for LastBackupAt: %v", cfg.Host, gerr)
	}
	return nil
}