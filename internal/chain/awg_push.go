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
	var needRestart []awgPushRecord
	for _, f := range files {
		// Decide BEFORE the file is overwritten: when the CURRENT on-disk
		// conf's [Interface] section is identical to the new one and the
		// service is active, the peer set can be synced live (awg set) — a
		// restart drops every connected client, so the frequent user
		// add/remove deploy must not restart (LucX SyncPeers pattern). The
		// comparison MUST happen pre-write: after the overwrite the on-disk
		// conf always equals the new one and interface changes would be
		// silently skipped (live-found bug 2026-07-19).
		synced := tryPeerSync(ctx, client, f, useSudo)
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
		if synced {
			log.Printf("pushAWGConfs: %s — interface unchanged, peers synced live (no restart)", f.ServiceName)
		} else {
			needRestart = append(needRestart, awgPushRecord{file: f, backupPath: backupPath})
		}
	}
	// All files written — now restart the services whose interface section
	// changed (or that failed peer sync — the restart re-reads the full conf
	// and self-heals a half-applied sync). If a restart fails, roll back all
	// files (the sing-box config push hasn't happened yet).
	for _, rec := range needRestart {
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

// teardownAWGInterfaces stops+disables the awg-quick units for interfaces this
// node must no longer run (AWG 3.0 inbounds bind the port from inside sing-box
// — see AWGTeardownInterfaces). Returns the units that were ACTIVE before the
// teardown so a failed sing-box push can bring them back (rollback symmetry
// with rollbackAWGConfs).
//
// Idempotent and non-fatal: an already-inactive or nonexistent unit is not an
// error (`|| true`), so a redeploy on a clean node never fails here. A teardown
// failure is logged but does not abort the deploy — if the port really is still
// held, sing-box's own check/restart surfaces it with a precise error and the
// normal rollback applies.
func teardownAWGInterfaces(ctx context.Context, client ports.SSHClient, ifaces []string, useSudo bool) []string {
	var restored []string
	for _, iface := range ifaces {
		service := awgServiceName(iface)
		wasActive := serviceActive(ctx, client, service, useSudo)
		cmd := fmt.Sprintf("systemctl disable --now %s || true ; ip link delete %s || true", service, iface)
		if _, _, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, cmd), 60*time.Second); err != nil {
			log.Printf("teardownAWGInterfaces: %s: %v (continuing — sing-box will report a real port clash)", service, err)
			continue
		}
		if wasActive {
			restored = append(restored, iface)
			log.Printf("teardownAWGInterfaces: %s stopped+disabled, link %s deleted (port freed)", service, iface)
		}
	}
	return restored
}

// restoreAWGInterfaces re-enables units that teardownAWGInterfaces stopped —
// used when the sing-box push fails, so the node returns to its pre-deploy
// state for BOTH layers. Errors are logged, not fatal (a partial restore beats
// aborting the rollback mid-way).
func restoreAWGInterfaces(ctx context.Context, client ports.SSHClient, ifaces []string, useSudo bool) {
	for _, iface := range ifaces {
		service := awgServiceName(iface)
		cmd := fmt.Sprintf("systemctl enable --now %s", service)
		if _, _, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, cmd), 60*time.Second); err != nil {
			log.Printf("restoreAWGInterfaces: rollback FAILED for %s: %v", service, err)
		}
	}
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

// PushConfigWithAWG is the exported AWG-aware deploy push — the same as
// pushConfigWithAWG but callable from outside the chain package (the takeover
// package uses it to atomically push a fresh awg0.conf + sing-box config, with
// rollback of both on failure — CTO-review §13.3 takeover re-render).
func PushConfigWithAWG(ctx context.Context, client ports.SSHClient, nodeID, cfgContent string, awgFiles []AWGConfFile, useSudo bool) (string, error) {
	return pushConfigWithAWG(ctx, client, nodeID, cfgContent, awgFiles, useSudo)
}

// PushConfigWithAWGTeardown is PushConfigWithAWG plus the AWG3 kernel-unit
// teardown (see AWGTeardownInterfaces): the listed interfaces are stopped and
// disabled before the sing-box config is pushed, and restored if that push
// fails. Exported for the takeover package, which shares the deploy pipeline.
func PushConfigWithAWGTeardown(ctx context.Context, client ports.SSHClient, nodeID, cfgContent string, awgFiles []AWGConfFile, teardownIfaces []string, useSudo bool) (string, error) {
	return pushConfigWithAWGTeardown(ctx, client, nodeID, cfgContent, awgFiles, teardownIfaces, useSudo)
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
	return pushConfigWithAWGTeardown(ctx, client, nodeID, cfgContent, awgFiles, nil, useSudo)
}

// pushConfigWithAWGTeardown is the full AWG-aware deploy push: kernel .conf
// push + AWG3 kernel-unit teardown + sing-box push, all under ONE host lock and
// with a symmetric rollback of every layer it touched.
func pushConfigWithAWGTeardown(ctx context.Context, client ports.SSHClient, nodeID, cfgContent string, awgFiles []AWGConfFile, teardownIfaces []string, useSudo bool) (string, error) {
	if len(awgFiles) == 0 && len(teardownIfaces) == 0 {
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
		// 2. Stop+disable kernel AWG units whose port an AWG3 userspace endpoint
		//    is about to bind — MUST happen before the sing-box restart, or
		//    sing-box fails with "bind: address already in use" and crash-loops
		//    (PROGRESS §39). Inside the same lock so no concurrent apply can
		//    bring the unit back between teardown and restart.
		restoreIfaces := teardownAWGInterfaces(ctx, client, teardownIfaces, useSudo)
		// 3. Push sing-box config directly via pushConfigLocked (we already hold
		//    the lock — calling pushConfig would re-acquire it and deadlock).
		out, cfgErr := pushConfigLocked(ctx, client, cfgContent, useSudo)
		if cfgErr != nil {
			// 4. sing-box failed — roll back BOTH the kernel AWG .confs and the
			//    units we disabled, so the node returns to its pre-deploy state
			//    for every layer.
			restoreAWGInterfaces(ctx, client, restoreIfaces, useSudo)
			rollbackAWGConfs(client, awgRecords, useSudo)
			return result{out: out, err: cfgErr}
		}
		return result{out: out}
	})
	return r.out, r.err
}

// renderAWGConfsForDeploy is the thin adapter from the deploy path's inputs to
// RenderNodeAWGConfs. It exists so ApplyChain/ApplyMergedNode can call it
// without each re-deriving the users-by-chain/inbound maps. Returns the .conf
// files plus any warnings (e.g. a chain AWG entry + standalone AWG inbound
// colliding on awg0 — AGENTS.md #10).
func renderAWGConfsForDeploy(
	store *Store,
	nodeInfo *model.NodeInfo,
	nodeChains []*model.Chain,
) ([]AWGConfFile, []string) {
	return RenderNodeAWGConfs(
		nodeInfo,
		nodeChains,
		usersByChainMap(store, nodeChains),
		usersByInboundMap(store, nodeInfo.Inbounds),
	)
}

// renderAWGDeployPlan is renderAWGConfsForDeploy plus the AWG3 teardown set —
// the two must be computed together because the teardown list is defined
// relative to the rendered files (never tear down an interface we still render).
func renderAWGDeployPlan(
	store *Store,
	nodeInfo *model.NodeInfo,
	nodeChains []*model.Chain,
) (files []AWGConfFile, teardown []string, warnings []string) {
	files, warnings = renderAWGConfsForDeploy(store, nodeInfo, nodeChains)
	teardown = AWGTeardownInterfaces(nodeInfo, nodeChains, files)
	return files, teardown, warnings
}
