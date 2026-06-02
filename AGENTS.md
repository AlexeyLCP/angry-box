# Angry-box — Agent Operating Manual

This file is the law for every agent working on the Angry-box project. Read it completely before touching any code.

---

## Workflow: How an Agent Executes a Task

```
1. READ    → Read recent conversation history, artifacts, and understand the user's intent.
2. AUDIT   → Read all relevant files, trace data flow end-to-end (e.g., UI → Store → Applier → SSH).
3. PLAN    → Write a short plan: which files to change, what logic to update. Ask for permission if architectural changes are needed.
4. CODE    → Implement changes cleanly following Go, HTMX, and Templ best practices.
5. TEMPL   → Run `templ generate` if any `.templ` files were modified.
6. BUILD   → Run `go build ./...` to ensure no compile-time errors.
7. TEST    → Run tests if applicable (`go test ./...`).
8. DOCS    → Update task tracking artifacts and summaries.
```

---

## Core Philosophy

1. **Orchestrator Pattern:** Angry-box is a central orchestrator. It does NOT route traffic itself. It generates configurations centrally and pushes them to remote nodes via SSH.
2. **Node-First Architecture:** The Node (`model.NodeInfo`) is the primary standalone entity. A user can run a node perfectly fine without any chains.
3. **Chains as an Overlay:** Chains (`model.Chain`) are an optional overlay to link nodes together. Chains generate "transport inbounds" under the hood, but these should not permanently overwrite a node's standalone configuration.
4. **Declarative State:** The `internal/chain/store.go` is the single source of truth. The `Applier` reads the state, generates a sing-box config, and forces the remote server into that state.

---

## The 10 Rules

### 1. HTMX + Templ UI Only
Do NOT write React, Vue, or heavy vanilla JavaScript.
- All UI is built with **Go Templ** (`web/templates/*.templ`).
- All interactivity uses **HTMX** (`hx-get`, `hx-post`, `hx-target`, `hx-swap`).
- Styling uses **TailwindCSS** and **DaisyUI**.
- *Always run `templ generate` after modifying UI files.*

### 2. Strict State Management (Store)
The `Store` (`internal/chain/store.go`) uses a `sync.Mutex` and writes to a JSON file (`store.json`).
- NEVER call a locked method from inside another locked method (Deadlock!).
- `ResolveNodes` does not hold a lock, but it calls `GetNodeInfo` which does. Be careful with lock scopes.

### 3. SSH TOFU (Trust On First Use)
Remote connections use SSH. 
- Host keys are verified via `CheckHostKey`.
- If a key changes, the deploy fails, and the user must explicitly approve the new fingerprint via the UI (`HostKeyWarning` modal).
- Do not bypass this security mechanism.

### 4. Config Generation Separation
- `internal/backend/singbox/config.go`: Defines the base sing-box config structures and standalone generation.
- `internal/chain/applier.go`: Contains the complex logic for building multi-hop chain configurations, transit keys, and strategy routing.
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
When `ApplyChain` or `ApplyStandaloneNode` runs:
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

### 10. Documentation Updates
If you add a new core feature (e.g., a new protocol, a new routing strategy), document it in the relevant task artifact or implementation plan so the user is aware of how it integrates with the rest of the system.

---

## Project Structure

```
/
├── cmd/
│   └── server/          # Main entrypoint (main.go)
├── internal/
│   ├── backend/
│   │   └── singbox/     # Sing-box config generation and JSON types
│   ├── chain/           # Core business logic
│   │   ├── applier.go   # Applies configs to remote nodes via SSH
│   │   ├── presets.go   # Protocol presets (Reality, XHTTP, TUIC)
│   │   └── store.go     # JSON/BoltDB persistence layer
│   ├── domain/
│   │   ├── model/       # Core data structures (Chain, NodeInfo, User)
│   │   └── ports/       # Interfaces (Factory, Backend, SSHClient)
│   ├── sshclient/       # SSH connection handling, file pushing, service control
│   └── web/
│       └── ui.go        # HTTP/HTMX handlers, routing
├── web/
│   ├── static/          # CSS, JS, assets
│   └── templates/       # .templ files for the UI
└── test_server.go       # E2E / local testing stubs
```

---

## Debugging Patterns

### Pattern 1: HTMX UI Not Updating
- **Cause:** You forgot to run `templ generate`, so the Go backend is serving the old compiled template.
- **Fix:** Run `templ generate` and rebuild.

### Pattern 2: Sing-box Fails to Start on Remote
- **Cause:** Invalid JSON config generated by the Applier.
- **Fix:** Check the `report` returned by `ApplyChain`. It contains the exact `sing-box check` error from the remote server. Look at `buildNodeConfig` to see what fields are missing or incorrectly typed.

### Pattern 3: Compilation Error on Config Types
- **Cause:** You guessed the field name in `config.SingboxConfig` instead of looking it up.
- **Fix:** ALWAYS check `internal/backend/singbox/config/types.go`. For example, routing rules are `RouteRuleEntry`, not `RoutingRule`.

### Pattern 4: Deadlocks in Store
- **Cause:** `SaveChain` locks `mu`, and inside it calls `GetHost` which also locks `mu`.
- **Fix:** Do not nest locked calls. Use unlocked internal helpers (e.g., `readStore`) when already inside a lock.

---

## UI Components & Templ Best Practices

- **Components:** Break down large templates into smaller components (e.g., `Nodes()`, `NodeRow()`, `NodeInboundsForm()`).
- **Conditional Classes:** Use `templ.KV("class-name", condition)` for dynamic styling.
- **Icons:** Use inline SVG icons (Heroicons).
- **Modals:** Use DaisyUI modals. Open them via HTMX targeting `#modal-container`.

---

## E2E Testing Infrastructure

- **GCloud project:** `project-d4c6c72c-4f10-4288-902`
- **Test servers:**
  - `vps-de-test-1` — 34.40.120.7 (Debian 12, key: `google_compute_engine`)
  - `vps-de-test-2` — 35.198.166.183 (Ubuntu 24.04, key: `id_ed25519`)
  - `vps-de-test-3` — 35.198.100.1 (Ubuntu 24.04, key: `id_ed25519`, свежий)
- Run E2E: `go test -tags e2e ./internal/chain/ -run TestE2E -v -timeout 300s`
- Auth: `gcloud auth login lucipoher@gmail.com`

## sing-box-extended (NOT plain sing-box)

- Project uses **sing-box-extended** (`1.13.11-extended-2.1.0`) — NOT official sing-box
- Binary in `deps/sing-box-1.13.11-extended-2.1.0-linux-amd64.tar.gz`
- Installed by `angry-box deploy` which downloads from project's GitHub deps
- Supports: amnezia field on wireguard endpoints, CPS/I1-I5 packets, MTProto
- AWG kernel module built from `deps/amneziawg-src.tar.gz`
- Module requires: `curve25519_x86_64`, `libcurve25519_generic`, `udp_tunnel`, `ip6_udp_tunnel`

## Known Issues & Workarounds

1. **TUIC requires TLS cert** — auto-generated via `buildTUICTLSOptions()`, written with base64 (heredoc fails)
2. **DNS/Route disabled** in merged config (sing-box 1.13 detour bugs) — minimal config works
3. **Multi-node chains** need Route/DNS re-enabled when detour is fixed
4. **No Python on test servers** — use `python3` explicitly when available
5. **AMG amnezia field** — only works with sing-box-extended, skipped for plain sing-box

## Commit Convention

- `fix:` — bug fixes
- `feat:` — new features
- `test:` — test additions
- `docs:` — documentation
- `refactor:` — code restructuring
- Commits end with: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`

---

**Remember:** You are building a premium, centralized orchestrator. The code should be clean, the UI should be fast and responsive, and the remote nodes should be treated as disposable execution environments dictated by the orchestrator's state.
