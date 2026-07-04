package takeover

// takeover.go — the takeover executor: detect (already done) → convert →
// install sing-box → snapshot old VPN → cutover (disable old + push sing-box
// config) → on success leave old disabled + audit; on failure auto-rollback to
// the old VPN (re-enable + restart + restore config).
//
// Mirrors chain.ApplyStandaloneNode's shape but adds the cutover + rollback-to-
// old-vpn steps that a normal apply doesn't have.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// TakeoverResult is the outcome of a takeover attempt.
type TakeoverResult struct {
	Status            string `json:"status"` // "taken" | "rolled-back" | "failed-both" | "nothing"
	FromType          string `json:"from_type"`
	OldService        string `json:"old_service"`
	OldConfigBackup   string `json:"old_config_backup,omitempty"`
	ConvertedInbounds int    `json:"converted_inbounds"`
	RollbackOccurred  bool   `json:"rollback_occurred"`
	Message           string `json:"message"`
}

// Takeover executes the full takeover flow against a node given a prior
// Detection. store persists the NodeInfo (+ TakeoverState); factory creates the
// sing-box backend for install + config generation.
func Takeover(ctx context.Context, store *chain.Store, f ports.Factory, host model.Host, useSudo bool, det *Detection, connector ...ports.SSHConnector) (*TakeoverResult, error) {
	if det == nil || det.Type == DetectedNone {
		return &TakeoverResult{Status: "nothing", Message: "no existing VPN detected to take over"}, nil
	}

	// 1. Convert the detected config → NodeInbounds (+ extra raw inbounds).
	inbounds, extra, err := Convert(det)
	if err != nil {
		return &TakeoverResult{Status: "rolled-back", FromType: string(det.Type), Message: "convert failed: " + err.Error()}, err
	}

	// Load/create the NodeInfo and persist the converted inbounds + takeover state.
	info := chain.NodeInfoForTakeover(store, host)
	info.UseSudo = useSudo
	info.Inbounds = inbounds
	info.Takeover = &model.TakeoverState{
		DetectedType:      string(det.Type),
		OldServiceName:    det.ServiceName,
		OldConfigPath:     det.ConfigPath,
		OldEnabled:        det.IsEnabled,
		ConvertedInbounds: len(inbounds) + len(extra),
		Status:            "converting",
	}
	if err := store.SaveNodeInfo(info); err != nil {
		return nil, fmt.Errorf("takeover: save node info: %w", err)
	}

	// 2. Connect (kept open for the whole cutover so backup/disable/push share it).
	conn := sshclient.DefaultConnector
	if len(connector) > 0 && connector[0] != nil {
		conn = connector[0]
	}
	client, err := conn.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("takeover: ssh connect: %w", err)
	}
	defer client.Close()

	// 3. Pre-snapshot the old VPN config (for rollback). Reuse createBackup.
	if det.ConfigPath != "" {
		backupPath, berr := chain.CreateBackup(client, det.ConfigPath)
		if berr == nil && backupPath != "" {
			info.Takeover.OldConfigBackup = backupPath
			_ = store.SaveNodeInfo(info)
		}
	}

	// 4. Install sing-box (binary + unit + minimal config). Does NOT touch the
	// old VPN service. Uses the injected factory so takeover does not hard-code
	// a concrete backend (mirrors the H5 fix for the CLI).
	backend := f.Create()
	if _, derr := backend.DeployWithOptions(ctx, host, model.DeployOptions{UseSudo: useSudo}); derr != nil {
		return &TakeoverResult{Status: "rolled-back", FromType: string(det.Type), Message: "sing-box install failed: " + derr.Error()}, derr
	}

	// 5. Render the sing-box config. AWG uses a dedicated renderer that
	// preserves the imported server keypair + amnezia + peer list (the generic
	// path pulls amnezia from a preset and hardcodes one peer, which would break
	// existing AWG clients). Other types go through renderTakeoverConfig.
	var cfgContent string
	if det.Type == DetectedAWG {
		imp, ierr := chain.ImportAWGConfigsViaClient(client, useSudo, nil)
		if ierr != nil || imp == nil || imp.ServerConfig == nil {
			res := &TakeoverResult{Status: "rolled-back", FromType: string(det.Type), Message: "awg import failed: " + errString(ierr)}
			return res, fmt.Errorf("takeover: awg import: %v", ierr)
		}
		cfgContent, err = renderAWGTakeoverConfig(imp.ServerConfig, imp.Peers)
	} else {
		cfgContent, err = renderTakeoverConfig(f, inbounds, extra)
	}
	if err != nil {
		// Render failed — nothing pushed yet; old VPN untouched. Surface error.
		return &TakeoverResult{Status: "rolled-back", FromType: string(det.Type), Message: "render failed: " + err.Error()}, err
	}

	// 6. Cutover: disable old VPN (except AWG), then push the sing-box config.
	res := &TakeoverResult{FromType: string(det.Type), OldService: det.ServiceName, OldConfigBackup: info.Takeover.OldConfigBackup, ConvertedInbounds: len(inbounds) + len(extra)}

	// Disable the old VPN service to free its port(s) — EXCEPT for AWG. Under
	// the kernel-AWG architecture, takeover KEEPS awg-quick@awg0 running (it
	// owns the AWG interface, peers, and amnezia obfuscation — userspace
	// wireguard-go would panic with chacha20poly1305 under AmneziaWG) and pushes
	// a sing-box TUN-overlay config that captures awg0 via include_interface.
	// Disabling awg-quick@awg0 would drop every existing client; we leave it
	// untouched and let sing-box route on top of it. The old config backup is
	// still taken (step 3) so rollback can restore it if sing-box fails.
	if det.Type != DetectedAWG && det.ServiceName != "" {
		if err := chain.DisableService(client, det.ServiceName, useSudo); err != nil {
			// Couldn't disable old — abort before pushing (leave old running).
			res.Status = "rolled-back"
			res.Message = "failed to disable old VPN " + det.ServiceName + ": " + err.Error()
			return res, err
		}
	}

	// 7. Push the sing-box config (backup → check → restart → health-probe, with
	// its own rollback to the previous /etc/sing-box/config.json on failure).
	if _, perr := chain.PushConfig(client, host.ID, cfgContent, useSudo); perr != nil {
		// sing-box did NOT come up. Auto-rollback to the old VPN.
		res.RollbackOccurred = true
		if rbErr := rollbackToOldVPN(ctx, client, det, info.Takeover, useSudo); rbErr != nil {
			// Old VPN ALSO failed to come back — critical, do not hide.
			res.Status = "failed-both"
			res.Message = fmt.Sprintf("sing-box failed: %v; AND rollback to %s also failed: %v", perr, det.ServiceName, rbErr)
			info.Takeover.Status = "failed-both"
			_ = store.SaveNodeInfo(info)
			chain.WriteTakeoverAudit(store, host.ID, string(det.Type), "failed-both", res.ConvertedInbounds)
			return res, fmt.Errorf("takeover: %s", res.Message)
		}
		res.Status = "rolled-back"
		res.Message = fmt.Sprintf("takeover failed (sing-box did not come up: %v); rolled back to %s — old VPN restored and active", perr, det.ServiceName)
		info.Takeover.Status = "rolled-back"
		_ = store.SaveNodeInfo(info)
		chain.WriteTakeoverAudit(store, host.ID, string(det.Type), "rolled-back", res.ConvertedInbounds)
		return res, fmt.Errorf("takeover: %s", res.Message)
	}

	// 8. Success: sing-box is up, old VPN disabled. Record deploy hash + audit.
	chain.RecordDeploySuccess(store, host.ID, cfgContent)
	info.Takeover.Status = "taken"
	info.Takeover.ConvertedAt = time.Now()
	_ = store.SaveNodeInfo(info)
	chain.WriteTakeoverAudit(store, host.ID, string(det.Type), "taken", res.ConvertedInbounds)
	res.Status = "taken"
	oldMsg := ""
	if det.ServiceName != "" {
		oldMsg = fmt.Sprintf(" Old VPN %s disabled (config backed up at %s).", det.ServiceName, info.Takeover.OldConfigBackup)
	}
	res.Message = fmt.Sprintf("Taken over from %s → sing-box. %d inbound(s) converted.%s", det.Type, res.ConvertedInbounds, oldMsg)
	return res, nil
}

// errString returns the error's message, or "" for nil — used in AWG import
// failure reporting where the error may be nil but the result still unusable.
func errString(err error) string {
	if err == nil {
		return "no server config parsed"
	}
	return err.Error()
}

// renderTakeoverConfig produces the sing-box config JSON from the converted
// inbounds. For native protocols (vless-reality/tuic/awg/hysteria2) it uses
// Backend.GenerateConfig (ConfigStandaloneNode). For protocols with extra raw
// JSON (vmess/trojan/ss/mtproto) it post-processes the rendered config to
// append them to Inbounds[].
func renderTakeoverConfig(f ports.Factory, inbounds []model.NodeInbound, extra []json.RawMessage) (string, error) {
	b := f.Create()
	cfg, err := b.GenerateConfig(model.ConfigStandaloneNode, model.ConfigParams{
		Extra: map[string]any{"inbounds": inbounds},
	})
	if err != nil {
		// If GenerateConfig fails because there are no native inbounds (only
		// extra), build a minimal config from scratch with just the extra.
		if len(inbounds) == 0 && len(extra) > 0 {
			return buildMinimalConfigWithExtra(extra)
		}
		return "", err
	}
	if len(extra) == 0 {
		return cfg.Content, nil
	}
	// Append extra raw inbounds to the rendered config.
	var sc struct {
		Log       *json.RawMessage `json:"log,omitempty"`
		Endpoints []json.RawMessage `json:"endpoints,omitempty"`
		Inbounds  []json.RawMessage `json:"inbounds"`
		Outbounds []json.RawMessage `json:"outbounds"`
		Route     *json.RawMessage `json:"route,omitempty"`
	}
	if err := json.Unmarshal([]byte(cfg.Content), &sc); err != nil {
		return "", fmt.Errorf("takeover: parse rendered config to append extra: %w", err)
	}
	sc.Inbounds = append(sc.Inbounds, extra...)
	out, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// buildMinimalConfigWithExtra builds a bare sing-box config carrying only the
// extra raw inbounds (used when ALL converted inbounds are non-native, e.g. a
// pure vmess/trojan xray server).
func buildMinimalConfigWithExtra(extra []json.RawMessage) (string, error) {
	cfg := struct {
		Log       json.RawMessage   `json:"log"`
		Inbounds  []json.RawMessage `json:"inbounds"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}{
		Log:       json.RawMessage(`{"level":"info"}`),
		Inbounds:  extra,
		Outbounds: []json.RawMessage{json.RawMessage(`{"type":"direct","tag":"direct"}`)},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// rollbackToOldVPN re-enables + restarts the old VPN service and restores its
// config from the backup. Returns an error if the old VPN could not be brought
// back (the caller marks the result "failed-both"). For AWG the takeover does
// NOT disable awg-quick@awg0 (the kernel-AWG architecture keeps it running and
// sing-box sits on top via a TUN overlay), so this rollback only restores the
// sing-box config.json's predecessor — the awg-quick service stays up the whole
// time. EnableService on an already-running unit is idempotent (enable + restart
// of a healthy service is a no-op).
func rollbackToOldVPN(ctx context.Context, client ports.SSHClient, det *Detection, state *model.TakeoverState, useSudo bool) error {
	if det.ServiceName == "" {
		return fmt.Errorf("no old service name to roll back to")
	}
	// Restore the old config from backup if we have one.
	if state.OldConfigBackup != "" && state.OldConfigPath != "" {
		_ = chain.RestoreFile(client, state.OldConfigBackup, state.OldConfigPath, useSudo)
	}
	// Re-enable + start the old service (only re-enable if it was enabled before).
	if state.OldEnabled {
		if err := chain.EnableService(client, det.ServiceName, useSudo); err != nil {
			return fmt.Errorf("re-enable %s: %w", det.ServiceName, err)
		}
	} else {
		// Was disabled before takeover — just start it to restore connectivity.
		cmd := "systemctl start " + det.ServiceName
		if useSudo {
			cmd = fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
		}
		_, _, _, _ = client.RunWithOutput(ctx, cmd, 30*time.Second)
	}
	// Verify the old VPN came back.
	if err := chain.ProbeServiceUp(client, det.ServiceName, useSudo); err != nil {
		return fmt.Errorf("old VPN %s did not come back: %w", det.ServiceName, err)
	}
	return nil
}