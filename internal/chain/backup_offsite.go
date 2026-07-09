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
	"sort"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// DefaultOffsiteBackupInterval is the backup loop interval when
// OffsiteBackupConfig.IntervalMin is 0 (360 min = 6h).
const DefaultOffsiteBackupInterval = 360

// DefaultOffsiteBackupRetention is how many recent blobs to keep on the offsite
// target when OffsiteBackupConfig.Retention is 0 (rotation via ls+rm after push).
const DefaultOffsiteBackupRetention = 5

// backupBlobPrefix is the filename prefix for rotated offsite blobs; the full
// name is <prefix>-<RFC3339-ish timestamp>.abbkp under cfg.RemotePath (which is
// treated as a directory).
const backupBlobPrefix = "angry-box"

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

	// 2. Encrypt with the passphrase-derived key (scrypt + AES-256-GCM). ScryptN
	//    is tunable per-target (cfg.ScryptN); 0 = package default. The blob stores
	//    the chosen N so a later decrypt reads it back regardless of this setting.
	scryptN := cfg.ScryptN
	if scryptN <= 0 {
		scryptN = backupScryptN
	}
	blob, err := EncryptBackupWithParams(plain, cfg.Passphrase, scryptN, backupScryptR, backupScryptP)
	if err != nil {
		return fmt.Errorf("backup: encrypt: %w", err)
	}

	// 3. Push over SSH. SSHKeyID is resolved by the connector's key resolver
	//    (the same registry nodes use). User defaults to the current SSH user
	//    if empty (the connector / ssh.Connect handles "" as the OS user).
	//    cfg.RemotePath is treated as a DIRECTORY on the offsite target; the blob
	//    is written to <RemotePath>/angry-box-<timestamp>.abbkp so each push is a
	//    distinct file (rotation keeps the last cfg.Retention, default 5).
	user := cfg.User
	keyRef := cfg.SSHKeyID
	client, err := connector.Connect(cfg.Host, user, keyRef)
	if err != nil {
		return fmt.Errorf("backup: ssh connect %s: %w", cfg.Host, err)
	}
	defer client.Close()

	blobName := fmt.Sprintf("%s-%s.abbkp", backupBlobPrefix, time.Now().UTC().Format("20060102-150405"))
	blobPath := strings.TrimRight(cfg.RemotePath, "/") + "/" + blobName
	if err := client.UploadText(ctx, string(blob), blobPath, 0o600); err != nil {
		return fmt.Errorf("backup: upload to %s:%s: %w", cfg.Host, blobPath, err)
	}

	// 3b. Rotate: keep the last `retention` blobs, remove older ones. Best-effort
	//     — a rotation failure (ls/rm error, weird target) does NOT fail the push:
	//     the new blob is already off-host; rotation is housekeeping. The ls lists
	//     only angry-box-*.abbkp files in the directory, sorted chronologically
	//     (the timestamp format sorts lexically = chronologically).
	if rerr := rotateOffsiteBlobs(ctx, client, cfg); rerr != nil {
		log.Printf("backup: pushed to %s but rotation failed (blobs may accumulate): %v", cfg.Host, rerr)
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

// rotateOffsiteBlobs lists the angry-box-*.abbkp blobs in cfg.RemotePath and
// removes the oldest ones beyond cfg.Retention (default 5). Best-effort: any
// ls/rm error is returned but the caller treats it as non-fatal (the new blob is
// already pushed). The timestamp in the filename (20060102-150405) sorts
// lexically = chronologically, so the newest N are the last N in sorted order.
func rotateOffsiteBlobs(ctx context.Context, client ports.SSHClient, cfg *model.OffsiteBackupConfig) error {
	_ = ctx
	retention := cfg.Retention
	if retention <= 0 {
		retention = DefaultOffsiteBackupRetention
	}
	dir := strings.TrimRight(cfg.RemotePath, "/")
	// ls -1 → one filename per line. Quote the glob so the shell does not expand
	// it locally; the remote shell expands it against the directory.
	out, err := client.Run(fmt.Sprintf("ls -1 %q/%s-*.abbkp 2>/dev/null", dir, backupBlobPrefix))
	if err != nil {
		return fmt.Errorf("ls: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var paths []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		// Keep only files that match our prefix (ls may include the literal glob
		// if no match — filter those out).
		base := l
		if idx := strings.LastIndex(l, "/"); idx >= 0 {
			base = l[idx+1:]
		}
		if !strings.HasPrefix(base, backupBlobPrefix+"-") || !strings.HasSuffix(base, ".abbkp") {
			continue
		}
		paths = append(paths, l)
	}
	sort.Strings(paths) // chronological (timestamp sorts lexically)
	if len(paths) <= retention {
		return nil
	}
	// Remove the oldest (len - retention) blobs.
	toRemove := paths[:len(paths)-retention]
	for _, p := range toRemove {
		if _, rerr := client.Run(fmt.Sprintf("rm -f %q", p)); rerr != nil {
			// Log + continue — a single rm failure should not stop the rest.
			log.Printf("backup: rotation rm %s failed: %v", p, rerr)
		}
	}
	return nil
}