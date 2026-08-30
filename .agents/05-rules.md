# 05 — Rules

Extracted from AGENTS.md. This file is project law.

---

## The 10 Rules

### 1. HTMX + Templ UI Only
Do NOT write React, Vue, or heavy vanilla JavaScript.
- All UI is built with **Go Templ** (`web/templates/*.templ`).
- All interactivity uses **HTMX** (`hx-get`, `hx-post`, `hx-target`, `hx-swap`).
- Styling uses **TailwindCSS** and **DaisyUI**.
- All user-facing strings MUST be wrapped in `i18n.T(ctx, "key")` (templates) or `i18n.T(r.Context(), "key")` (handlers in `ui.go`), with the key added to BOTH `en` and `ru` blocks in `internal/i18n/i18n.go`. JS-side strings use the server-rendered `window.AB_I18N` + `abt("key")` helper (see `base.templ` / `app.js`). Never hardcode English UI text.
- *Always run `templ generate` after modifying UI files.*

### 2. Strict State Management (Store)
The `Store` (`internal/chain/store.go`) uses a `sync.Mutex` and writes to a JSON file (`store.json`).
- NEVER call a locked method from inside another locked method (Deadlock!).
- `ResolveNodes` does not hold a lock, but it calls `GetNodeInfo` which does. Be careful with lock scopes.

### 3. SSH TOFU (Trust On First Use)
Remote connections use SSH. 
- Host keys are verified via `CheckHostKey`.
- First-seen keys are stored **untrusted** and the connection is refused until the operator confirms the fingerprint in the UI (`HostKeyWarning` modal). Same modal for a later key change.
- Do not auto-trust. Do not bypass this security mechanism.

### 4. Config Generation Separation
- `internal/singbox/config/types.go`: The base sing-box config structures + standalone generation — including `AwgEndpointOptions` (carries jc/jmin/jmax/s1-s4/h1-h4/i1-i5 flat + AWG3 HeaderProtectionKey/ContentPaddingAddition/RekeyAfterTime, see #5).
- `internal/backend/singbox/roles.go`: Role-based renderers (`RenderProxyNode`, `RenderAWGBalancer`, `RenderAWGHop`) — NO amnezia/ECH/curve_preferences on REALITY inbound, XHTTP headers as `map[string]string`.
- `internal/chain/applier_build.go`: Contains the complex logic for building multi-hop chain configurations, transit keys, and strategy routing (pure config generation + the `ApplyChain` orchestrator).
- `internal/chain/applier_push.go`: The SSH I/O layer of the deploy pipeline only (`createBackup`/`performRollback`/`pushConfig`/`probeServiceUp`/`ensureCertForTLSInbounds`) — split out of the old `applier.go` so config generation and remote I/O are not mixed in one file (AGENTS.md #4 layering).
- `internal/chain/merged_config.go`: `RenderMergedNodeConfig` builds the merged single-node config (standalone + chain roles).
- `internal/takeover/`: VPN takeover (detect existing AWG/sing-box/Xray/MTProxy → convert → cutover with rollback-to-old-VPN).
- Do not mix UI logic with config generation logic.

### 5. Persistent Transit Keys
When a chain is created, transit links require credentials (e.g., VLESS Reality PrivateKey, ShortID, UUID).
- These MUST be generated once and persisted in `model.ChainNode`.
- `generateHopParams` must reuse existing keys to prevent client connections from dropping upon redeployment.
- Always call `st.SaveChain(c)` after `ApplyChain` to save any newly generated transit keys.

### 6. No Silent Failures
- Never ignore errors with `_` unless explicitly documented why it is safe.
- UI handlers must return clear error messages to the user (e.g., via `templates.ApplyResult` or HTMX alerts).
- Log significant backend events.

### 7. Graceful Rollback
When `ApplyChain` or `ApplyMergedNode` runs:
1. It connects via SSH.
2. It pushes the new config.
3. It restarts sing-box.
4. It verifies the service is running.
5. If it fails, it **rolls back** to the previous config automatically.
Do NOT break this rollback chain.

### 8. Port Conflict Prevention
Nodes can have both Standalone Inbounds and Chain Transport Inbounds.
- Always check for port conflicts before saving or applying.
- Chain inbounds are read-only in the UI and their ports (usually 443, 8443) cannot be overridden by standalone inbounds.

### 9. Test Before Reporting
If you modify Go code, run `go build ./...`. 
If you modify Templ, run `templ generate` THEN `go build ./...`.
Do not tell the user a task is done if the code does not compile.
**TUIC is FROZEN — see Known Issues #6.** Do not write, run, or fix TUIC tests without an explicit user request.

### 10. Documentation Updates
If you add a new core feature (e.g., a new protocol, a new routing strategy), document it in the relevant task artifact or implementation plan so the user is aware of how it integrates with the rest of the system.

### 11. Changelog Style (lucx-ui)
Every release gets a `CHANGELOG.md` entry written in the **lucx-ui style** (same as the sibling project `lucx-ui` `RELEASE-NOTES-*`). The release workflow extracts the `## [vX.Y.Z]` section as the GitHub release body, so the header MUST stay `## [vX.Y.Z] — date`. Style:
- Catchy `### <emoji> Title` line (RU first), short 1–2 sentence intro saying WHY the release exists.
- `**Что изменилось**` — bullet list, each item starts with a **bold lead phrase** then explains the change AND the reasoning/trade-off in plain language (like lucx-ui: "MTU по умолчанию: 1320 → 1420 …").
- `**Обновление:**` — how to upgrade + what migrations/none + what to do on a staging node.
- Friendly closing line with emoji (e.g. `⚡️ Приятного использования!`).
- Then `---` and the full **English** mirror (`### <emoji> Title`, `**What changed**`, `**Upgrade:**`, `⚡️ Enjoy!`).
- Never a dry feature dump; write for the operator, explain the "why".
