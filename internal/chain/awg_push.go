package chain

// awg_push.go — AWG-aware deploy push for the kernel-AWG architecture. Wraps
// pushConfig so that, for nodes running kernel AWG interfaces, the kernel
// awg-quick .conf files (RenderNodeAWGConfs) are pushed and their
// awg-quick@... services enabled/restarted BEFORE the sing-box config — and
// rolled back together with the sing-box config when check/restart/probe fails.
//
// The per-host lock is held across BOTH pushes so the backup→write→restart→
// rollback sequence for awg0/awg-exit-nX and config.json is atomic relative to
// other concurrent applies targeting the same node. pushConfig's own
// withHostLock is not reentrant, so this wrapper drives pushConfigLocked
// directly under a single withHostLock (mirroring pushConfig's structure).
//
// For non-AWG nodes (no kernel .conf files) this falls through to the plain
// pushConfig path — no behavior change.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// awgConfDirExists ensures /etc/amnezia/amneziawg exists (awg-quick refuses to
// bring up an interface whose conf dir is missing). Idempotent.
func awgConfDirExists(ctx context.Context, client ports.SSHClient, useSudo bool) error {
	cmd := "mkdir -p " + awgConfDir
	if useSudo {
		cmd = "sudo " + cmd
	}
	_, _, _, err := client.RunWithOutput(ctx, cmd, 30*time.Second)
	if err != nil {
		return fmt.Errorf("ensure %s: %w", awgConfDir, err)
	}
	return nil
}

// ensureIPForward enables IPv4 forwarding live AND persists it, so the kernel
// routes packets between awg0 (kernel AWG) and sing-box-tun (the TUN overlay).
// Without ip_forward=1, tunneled client traffic lands on awg0 but the kernel
// drops it instead of handing it to sing-box — curl-through-tunnel gets no
// reply. The reference deploy (VPN/docs/server-dns-idoctor-mom.md) sets this;
// it was a missed step in the kernel-AWG rework (the "egress routing polish"
// open item). Idempotent: a /etc/sysctl.d file is written once; `sysctl -w`
// applies it live. No MASQUERADE (that would bypass sing-box — see
// nuances-bugs-patches.md §source_ip_cidr vs inbound).
func ensureIPForward(ctx context.Context, client ports.SSHClient, useSudo bool) error {
	sudoB := func(cmd string) string {
		if !useSudo {
			return cmd
		}
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	// Write the persistence file once (idempotent — tee -a would duplicate on
	// redeploys; a single 99-angry-box.conf is cleaner than editing sysctl.conf).
	cmd := sudoB(`cat > /etc/sysctl.d/99-angry-box.conf << 'EOF'
net.ipv4.ip_forward = 1
EOF
sysctl -w net.ipv4.ip_forward=1 >/dev/null`)
	if _, _, _, err := client.RunWithOutput(ctx, cmd, 30*time.Second); err != nil {
		return fmt.Errorf("enable ip_forward: %w", err)
	}
	return nil
}

// pushAWGConfFile uploads one kernel awg-quick .conf to its remote path with
// mode 0600, mirroring pushConfigLocked's useSudo temp-file-then-sudo-cp dance.
func pushAWGConfFile(ctx context.Context, client ports.SSHClient, file AWGConfFile, useSudo bool) error {
	if useSudo {
		tmp := "/tmp/angry-awg-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".conf"
		if err := client.UploadText(ctx, file.Content, tmp, 0o600); err != nil {
			return fmt.Errorf("write %s (tmp): %w", file.Path, err)
		}
		_, _, _, err := client.RunWithOutput(ctx,
			fmt.Sprintf("sudo bash -c 'cp %s %s && chmod 600 %s && rm -f %s'",
				tmp, file.Path, file.Path, tmp), 30*time.Second)
		if err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
		return nil
	}
	if err := client.UploadText(ctx, file.Content, file.Path, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", file.Path, err)
	}
	return nil
}

// enableAWGService enables+restarts one awg-quick@... unit, mirroring the
// sing-box enable/start idiom (daemon-reload && enable && reset-failed &&
// restart). Returns an error if the unit fails to come up (probeServiceUp
// retries is-active with a journalctl tail on failure).
func enableAWGService(ctx context.Context, client ports.SSHClient, service string, useSudo bool) error {
	sudoB := func(cmd string) string {
		if !useSudo {
			return cmd
		}
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	cmd := sudoB("systemctl daemon-reload && systemctl enable " + service +
		" && systemctl reset-failed " + service + " ; systemctl restart " + service)
	if _, _, _, err := client.RunWithOutput(ctx, cmd, 60*time.Second); err != nil {
		return fmt.Errorf("enable/restart %s: %w", service, err)
	}
	if err := probeServiceUp(ctx, client, service, useSudo); err != nil {
		return fmt.Errorf("%s not active after restart: %w", service, err)
	}
	return nil
}

// pushAWGConfs pushes all kernel awg-quick .conf files for a node and brings
// their services up. Returns the list of (service, backupPath) pairs it created
// so the caller can roll them back on a sing-box check/restart/probe failure.
// Backups use createBackup per file (each in its own timestamped dir).
func pushAWGConfs(ctx context.Context, client ports.SSHClient, files []AWGConfFile, useSudo bool) ([]awgPushRecord, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if err := awgConfDirExists(ctx, client, useSudo); err != nil {
		return nil, err
	}
	// Enable IPv4 forwarding before bringing up awg0 — without it the kernel
	// drops tunneled packets between awg0 and sing-box-tun (the egress-routing
	// polish: the reference deploy sets this; a missed step in the rework).
	if err := ensureIPForward(ctx, client, useSudo); err != nil {
		return nil, err
	}
	var records []awgPushRecord
	for _, f := range files {
		backupPath, backupErr := createBackup(client, f.Path)
		if backupErr != nil {
			log.Printf("pushAWGConfs: backup warning for %s: %v", f.Path, backupErr)
		}
		records = append(records, awgPushRecord{file: f, backupPath: backupPath})
		if err := pushAWGConfFile(ctx, client, f, useSudo); err != nil {
			// Roll back the files already written this round, then return.
			rollbackAWGConfs(client, records[:len(records)-1], useSudo)
			return nil, err
		}
	}
	// All files written — now enable+restart each service. If one fails, roll
	// back all of them (the sing-box config push hasn't happened yet).
	for _, rec := range records {
		if err := enableAWGService(ctx, client, rec.file.ServiceName, useSudo); err != nil {
			rollbackAWGConfs(client, records, useSudo)
			return nil, err
		}
	}
	return records, nil
}

// awgPushRecord ties a pushed .conf file to its backup path for rollback.
type awgPushRecord struct {
	file       AWGConfFile
	backupPath string
}

// rollbackAWGConfs restores each .conf from its backup (cp, preserved) and
// restarts the awg-quick service. A missing backup (first deploy) makes the
// restore a no-op but the restart still runs so the service reflects whatever
// was on disk. Errors are logged, not fatal — a partial rollback is better
// than aborting the rollback mid-way.
func rollbackAWGConfs(client ports.SSHClient, records []awgPushRecord, useSudo bool) {
	for _, rec := range records {
		if err := performRollback(client, rec.file.Path, rec.backupPath, rec.file.ServiceName, useSudo); err != nil {
			log.Printf("pushAWGConfs: rollback FAILED for %s: %v", rec.file.Path, err)
		}
	}
}

// pushConfigWithAWG is the AWG-aware deploy push. For nodes with kernel AWG
// .conf files it: (1) pushes the awg-quick .confs and enables their services,
// (2) pushes the sing-box config (check → restart → probe), (3) on any sing-box
// failure rolls back BOTH the sing-box config and the awg-quick .confs. For
// nodes with no kernel AWG files it delegates to pushConfig (no change).
//
// nodeID drives the per-host lock; the whole AWG+sing-box sequence runs under a
// single withHostLock so concurrent applies can't interleave (mirrors
// pushConfig's single-chokepoint invariant — CTO-review C2).
func pushConfigWithAWG(ctx context.Context, client ports.SSHClient, nodeID, cfgContent string, awgFiles []AWGConfFile, useSudo bool) (string, error) {
	if len(awgFiles) == 0 {
		return pushConfig(ctx, client, nodeID, cfgContent, useSudo)
	}
	type result struct {
		out string
		err error
	}
	r := withHostLock(nodeID, func() result {
		// 1. Push kernel AWG .confs + enable services (under the lock so the
		//    sing-box push below sees awg0/awg-exit-nX already up).
		awgRecords, awgErr := pushAWGConfs(ctx, client, awgFiles, useSudo)
		if awgErr != nil {
			return result{err: fmt.Errorf("awg push: %w", awgErr)}
		}
		// 2. Push sing-box config directly via pushConfigLocked (we already hold
		//    the lock — calling pushConfig would re-acquire it and deadlock).
		out, cfgErr := pushConfigLocked(ctx, client, cfgContent, useSudo)
		if cfgErr != nil {
			// 3. sing-box failed — roll back the kernel AWG .confs too so the
			//    node returns to its pre-deploy state for both layers.
			rollbackAWGConfs(client, awgRecords, useSudo)
			return result{out: out, err: cfgErr}
		}
		return result{out: out}
	})
	return r.out, r.err
}

// renderAWGConfsForDeploy is the thin adapter from the deploy path's inputs to
// RenderNodeAWGConfs. It exists so ApplyChain/ApplyMergedNode can call it
// without each re-deriving the users-by-chain/inbound maps.
func renderAWGConfsForDeploy(
	store *Store,
	nodeInfo *model.NodeInfo,
	nodeChains []*model.Chain,
) []AWGConfFile {
	return RenderNodeAWGConfs(
		nodeInfo,
		nodeChains,
		usersByChainMap(store, nodeChains),
		usersByInboundMap(store, nodeInfo.Inbounds),
	)
}
