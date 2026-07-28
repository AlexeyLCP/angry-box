# Angry-box — Agent Operating Manual

This file is the law for every agent working on the Angry-box project. Read it completely before touching any code.

---

## Workflow: How an Agent Executes a Task

```
1. READ    → Read AGENTS.md, the latest sections of docs/PROGRESS.md,
             `git log --oneline -15` — understand the current state before changing anything.
2. AUDIT   → Read all relevant files, trace data flow end-to-end (e.g., UI → Store → Applier → SSH).
3. PLAN    → Write a short plan: which files to change, what logic to update. Ask for permission if architectural changes are needed.
4. CODE    → Implement changes cleanly following Go, HTMX, and Templ best practices.
5. TEMPL   → Run `templ generate` if any `.templ` files were modified.
6. BUILD   → Run `go build ./...` to ensure no compile-time errors.
7. TEST    → Run tests if applicable (`go test ./...`).
8. COMMIT  → `git add` specific files (never `git add -A` blindly), prefix per Commit Convention.
9. STATUS  → After each commit output `git status` + `git log --oneline -5`.
9.5 PUSH-GUARD → BEFORE `git push` ALWAYS check open PRs and issues:
             `gh pr list --repo AlexeyLCP/angry-box --state open`
             `gh issue list --repo AlexeyLCP/angry-box --state open`
             If there are unhandled PRs (not yours) or issues — do NOT push silently.
             Tell the user what is open and by whom, and propose an order:
             (a) review/merge the PR first, (b) fix the issue first, (c) push after.
             Never push on top of someone's unreviewed work.
10. DOCS   → Every feature/fix commit gets an entry in docs/PROGRESS.md (what was
             done, which files, which tests). Architecture changes → update AGENTS.md
             (Known Issues, Debugging Patterns, Project Structure). No gaps: if you
             fixed something — write it down. These files are project law.
```

---

## Core Philosophy

1. **Orchestrator Pattern:** Angry-box is a central orchestrator. It does NOT route traffic itself. It generates configurations centrally and pushes them to remote nodes via SSH.
2. **Node-First Architecture:** The Node (`model.NodeInfo`) is the primary standalone entity. A user can run a node perfectly fine without any chains.
3. **Chains as an Overlay:** Chains (`model.Chain`) are an optional overlay to link nodes together. Chains generate "transport inbounds" under the hood, but these should not permanently overwrite a node's standalone configuration.
4. **Declarative State:** The `internal/chain/store.go` is the single source of truth. The `Applier` reads the state, generates a sing-box config, and forces the remote server into that state.

### Product Focus (v0.2.x — do NOT expand scope)

**Ship first:** AWG (kernel + balancer), VLESS+Reality+XHTTP (inter-node transport + standalone), MTProxy/Telemt.

**Paused (do NOT implement, test, or expose in UI for new configs):**
- **TUIC** — user entry + standalone (QUIC/TLS cert hassle + unresolved bugs; AGENTS.md #6).
- **Hysteria2** — transport + standalone + user entry (same QUIC/TLS cert class as TUIC; builder not written; AGENTS.md #11/#13).

Existing store entries that already use TUIC/Hysteria2 may remain for display/edit, but new chains/inbounds must be rejected (`internal/chain/frozen.go`).

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
- If a key changes, the deploy fails, and the user must explicitly approve the new fingerprint via the UI (`HostKeyWarning` modal).
- Do not bypass this security mechanism.

### 4. Config Generation Separation
- `internal/backend/singbox/config.go`: Defines the base sing-box config structures and standalone generation.
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
**TUIC is FROZEN — see Known Issues #6.** Do not write, run, or fix TUIC tests without an explicit user request.

### 10. Documentation Updates
If you add a new core feature (e.g., a new protocol, a new routing strategy), document it in the relevant task artifact or implementation plan so the user is aware of how it integrates with the rest of the system.

---

## Project Structure

```
/
├── cmd/
│   └── angry-box/       # Main entrypoint (main.go) — CLI + serve
├── internal/
│   ├── backend/
│   │   ├── factory/     # Backend factory
│   │   ├── singbox/     # sing-box config generation (config.go, roles.go, singbox.go)
│   │   └── xray/        # Xray backend (dual-core support)
│   ├── chain/           # Core business logic
│   │   ├── applier_build.go # Pure config-gen + ApplyChain orchestrator
│   │   ├── applier_push.go  # SSH I/O deploy pipeline (pushConfig + rollback)
│   │   ├── merged_config.go  # Role-based merged config builder
│   │   ├── presets.go / protocolpresets.go / routingpresets.go
│   │   ├── migrate_v2.go   # schema v1→v2: standalone→профили, chain entry→профиль, flat→Levels
│   │   ├── profile_deploy.go # ApplyProfileToNodes — diff-семантика размещения профилей
│   │   ├── strategygroup.go  # group outbounds (fallback/urltest/failover/selector) + ValidateChainTopology
│   │   ├── chain_entry_material.go # материализация entry-инбаунда из профиля (ApplyChain)
│   │   ├── awgpresets_gen.go / awg_cps.go / awgcapture.go / awgimport.go
│   │   ├── cryptogen.go      # Reality/WG/Trojan/SS/Hysteria2/TUIC/MTProxy key gen
│   │   ├── audit.go / deploystatus.go / autoapply.go
│   │   └── store.go     # JSON persistence layer (single source of truth)
│   ├── takeover/        # VPN takeover (detect/convert/cutover + rollback-to-old)
│   ├── domain/
│   │   ├── model/       # Core data structures (Chain, NodeInfo, User, PanelSettings, AuditLog)
│   │   └── ports/       # Interfaces (Factory, Backend, SSHClient)
│   ├── i18n/            # Translations (en/ru) — i18n.T(ctx, "key")
│   ├── ssh/             # SSH connection handling, file pushing, service control, TOFU
│   └── web/             # HTTP/HTMX handlers (server.go + chains/nodes/users/settings/spider/presets/profiles/takeover/dashboard/auth/csrf/...)
├── web/
│   ├── static/          # CSS, JS, assets
│   └── templates/       # .templ files for the UI
└── scripts/             # install.sh, systemd service, Keenetic init (S99), ndm-hook.sh, build-ipk.sh (router packages)
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
- **Cause:** You guessed the field name in the sing-box config structs instead of looking it up.
- **Fix:** ALWAYS check `internal/backend/singbox/config.go` (the base config + standalone generation) and `internal/backend/singbox/roles.go` (`RenderProxyNode`/`RenderAWGBalancer`/`RenderAWGHop`). For example, routing rules are `RouteRuleEntry`, not `RoutingRule`.

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

## Test Servers & E2E Infrastructure

### Личные тестовые VPS (МОЖНО трогать; SSH `root@<ip>`, ключ `~/.ssh/id_ed25519`)

| Alias | IP | OS / kernel | Состояние / правила |
|---|---|---|---|
| `n1` | 144.31.224.212 | Debian 13, 6.12.95 | **ЕДИНСТВЕННЫЙ тестовый сервер, полностью наш** (2026-07-19: lucx-ui снят — x-ui disabled/removed, xray убит, awg1 down; бэкап /root/cleanup-backup-20260719/). amneziawg-tools v1.0.20260618-2 + DKMS module 1.0.20260611 + iptables/nftables/openresolv + tcpdump. Живёт v0.8 trial-деплой (awg-quick@awg0 :51840 + sing-box TUN overlay). Same-host клиент НЕ проверяет egress (IP клиента локален на kernel) — использовать **netns-изоляцию** (veth pair, endpoint на host-veth IP, PROGRESS §28). |
| ~~`n2`~~ | 144.31.157.106 | — | **БОЛЬШЕ НЕ НАШ** (2026-07-19: отдан под тестирование другого продукта). НЕ ТРОГАТЬ. |

### GCloud тестовые (project `project-d4c6c72c-4f10-4288-902`) — могут быть ОСТАНОВЛЕНЫ, проверять доступность до использования:
  - `vps-de-test-1` — 34.40.120.7 (Debian 12, key: `google_compute_engine`)
  - `vps-de-test-2` — 35.198.166.183 (Ubuntu 24.04, key: `id_ed25519`)
  - `vps-de-test-3` — 35.198.100.1 (Ubuntu 24.04, key: `id_ed25519`)

### ПРОДОВЫЕ GCloud (entry 34.14.98.64, middle 207.175.1.227, exit 35.189.235.61) — НЕ ТРОГАТЬ. Никаких деплоев, проб, рестартов, дебаг-команд.

- Run E2E: `go test -tags e2e ./internal/chain/ -run TestE2E -v -timeout 300s`
- Auth: `gcloud auth login lucipoher@gmail.com`

## amnezia-box (our base sing-box fork, NOT plain sing-box)

- Project uses **amnezia-box** — our fork `AlexeyLCP/amnezia-box` (a fork of
  `hoaxisr/amnezia-box`, which is itself sing-box 1.14 alpha). It carries:
  - the AWG3 userspace endpoint `type:"awg"` (amneziawg-go `feat/awg3` pinned in
    the fork's go.mod — `hoaxisr/amneziawg-go awg3 @ fc48874`, the InputPackets API
    that `transport/awg/port.go` needs). AWG3 obfuscation fields
    (HeaderProtectionKey / ContentPaddingAddition / RekeyAfterTime) are FLAT on
    the `awg` endpoint (no nested `amnezia:{}` block, unlike the old
    sing-box-extended `wireguard` endpoint). S1-S4 must be >= 12 when
    HeaderProtectionKey is set (`HeaderCipherNonceSize=12`).
  - **our ports from sing-box-extended**: mtproxy (`with_mtproxy` build tag,
    `protocol/mtproxy/` + mtg-multi `shtorm-7/mtg-multi` extended fork) and
    fallback round-robin (`protocol/group/fallback.go` + the rr patch — our prod
    strategy #18 "Round-robin (fallback)", committed to the fork's tree, NOT a
    `patches/` file here).
- The full rebasing procedure + the `patchcheck` version-match test (gated by the
  `patchcheck` build tag) are documented in **`docs/PATCHES.md`** — that is the
  law for bumping the amnezia-box fork ref + the amneziawg-go pin.
- Binary in `deps/sing-box-<short-sha>-amnezia-linux-amd64.tar.gz` (built by
  `scripts/build-singbox.sh`, published to the GitHub release).
- Installed by `angry-box deploy` which downloads from the project's GitHub deps
  (weak VPSes never compile Go — they just download). Detection: `isPatchedExtended`
  matches the `with_awg` build tag in `sing-box version` (the old
  sing-box-extended canary tags `with_trusttunnel`/`with_sudoku` are gone).
- User-facing AWG servers stay kernel (`awg-quick@awg0` + sing-box TUN-overlay
  `include_interface:["awg0"]` — AGENTS #10/#11 rework preserved). The sing-box
  `type:"awg"` endpoint is the INTER-NODE TRANSPORT + legacy CLI standalone path
  (userspace amneziawg-go in-process). Kernel awg-quick `.conf` path
  (`RenderServerAWGConf` etc.) is INI text, unaffected by the `type:"awg"` migration.
- Supports: AWG3 fields (HPK/CPM/RAT), CPS/I1-I5 packets, MTProxy (telemt), XHTTP
  max obfuscation, fallback round-robin groups.
- AWG kernel module built from `deps/amneziawg-src.tar.gz` (kernel awg-quick +
  sing-box `bind_interface`/TUN-overlay). Module requires: `curve25519_x86_64`,
  `libcurve25519_generic`, `udp_tunnel`, `ip6_udp_tunnel`.
- **Historical:** the project previously used `sing-box-extended`
  (`1.13.14-extended-2.5.0-patched`, shtorm-7 fork) with `patches/`
  (wireguard-go-awg-overlap + fallback-round-robin). That stack is superseded by
  amnezia-box (sing-box 1.14 + amneziawg-go AWG3); the `patches/` directory was
  removed (fallback is in the fork's tree; the overlap fix is irrelevant — AWG3
  runs through amneziawg-go, not the shtorm-7 wireguard-go userspace path that
  panicked). See `docs/PROGRESS.md` §31 for the migration.

## Known Issues & Workarounds

1. **TUIC requires TLS cert** — auto-generated via `buildTUICTLSOptions()`, written with base64 (heredoc fails)
2. **DNS/Route disabled** in merged config (sing-box 1.13 detour bugs) — minimal config works. The previously-retained `buildMergedRouting`/`buildMergedDNS` dead builders were removed (CTO-review M10); re-implement against the live sing-box version when the detour bug is fixed.
3. **Multi-node chains** need Route/DNS re-enabled when detour is fixed
4. **No Python on test servers** — use `python3` explicitly when available
5. **AWG obfuscation fields are FLAT on `type:"awg"` endpoint** (amnezia-box 1.14) — NOT a nested `amnezia:{}` block (the old sing-box-extended `wireguard`+`amnezia` shape is gone). `AwgEndpointOptions` (internal/singbox/config/types.go) carries jc/jmin/jmax/s1-s4/h1-h4/i1-i5 flat + AWG3 HeaderProtectionKey/ContentPaddingAddition/RekeyAfterTime. `AmneziaOptions` stays as a holder for the kernel awg-quick `.conf` INI path (Itime json:"-"). S1-S4 >= 12 for HPK.
6. **TUIC is FROZEN — do NOT test or fix.** TUIC has too many unresolved issues right now. Do NOT run TUIC E2E tests, do NOT write new TUIC tests, do NOT attempt TUIC bug fixes. Keep the existing code in place but treat the protocol as unresearched. Revisit only after explicit user request. Any TUIC-related test cases in `e2e_heavy_test.go` / `clientconfig_test.go` / etc. should be skipped or excluded from the active test set.
7. **Per-client routing has TWO mechanisms — AWG is the primary.** AWG (the main product protocol) routes by `source_ip_cidr` (each user = a WireGuard peer with a unique inner IP `User.AWGAddress`; the peer's IP is preserved end-to-end through the inter-node XHTTP tunnel, so transit nodes can route by source IP and a pin to ANY downstream node works). TUIC/VLESS/Hysteria2 route by `auth_user` (inbound identity, only re-asserted on the entry, so a pin beyond one hop is NOT expressible there). The `auth_user` path (commits B1–B6) is now **legacy/secondary** — keep it, but new per-client work should target the AWG peer/source-IP model. AWG per-user creds: `User.AWGPrivateKey/AWGPublicKey/AWGAddress` (generated by `EnsureUserCreds` + `EnsureUserAWGAddress`); multi-peer endpoint via `buildAWGUserInboundMulti`; client `.conf` via `RenderClientAWGConf`. Route+DNS still gated behind `AB_ROUTE_DNS=1`. The AWG per-client E2E is a skip stub (`TestE2E_Heavy_PerClientRouting`) until the AWG kernel module is staged on the test VPSes; the routing logic is covered by unit tests (`TestBuildMergedRoute_PerClientAWG_*`).
8. **`TransportAWG` is IMPLEMENTED; `TransportHysteria2` is FROZEN.** AWG is a working inter-node chain transport (the link between chain nodes), alongside Reality and XHTTP. `buildChainRoleInOut` (`merged_config.go`) branches on `c.Transport`: AWG builds a `WireGuardEndpoint` transit inbound (`buildAWGTransportInbound`, tag `ch-<chain>-transport-in`) + a `WireGuardEndpoint` client on the previous node (`buildAWGTransportOutbound`, tag `ch-<chain>-out-awg-<nextID>`). Per-link WireGuard keypairs are generated in `ApplyChain` and persisted on `model.ChainNode` (`TransitAWGServerPriv/Pub`, `TransitAWGClientPriv/Pub`, `TransitAWGAddress` — inner subnet `10.9.0.0/24`, separate from the user-entry `10.8.0.0/24`). `InstallAWGModuleWithOptions` is gated on `UserProtocol == AWG || Transport == AWG` so transit nodes get the module. Route rules are transport-agnostic (tag-based), so source-IP per-client routing works through AWG transit too. **`TransportHysteria2` is FROZEN (see #11)** — the constant + UI dropdown exist, but the builder has no Hysteria2 branch; `buildChainRoleInOut` now refuses loudly (a `MergeReport.Warning` + no inbound/outbound emitted) instead of silently falling back to Reality. Do NOT assume Hysteria2-transport works.
9. **sing-box-extended 1.13 has NO wireguard OUTBOUND.** The wireguard outbound was deprecated in sing-box 1.11 and removed in 1.13; sing-box-extended 1.13.14 rejects `outbounds[].server` / `local_address` with `json: unknown field`. The client side of a WireGuard link is therefore a `WireGuardEndpoint` with a `peers[]` entry carrying `address`+`port`+`public_key` (the shape `RenderAWGHop` already uses), NOT a `wireguard` outbound. `WireGuardOutbound` struct exists in `types.go` for reference but is NOT used by the chain path. Confirmed by real `sing-box check` (see `TestAWGTransport_*`).
10. **AWG amnezia obfuscation — four P0 fixes (handshake-breaking), all verified by a real awg-quick tunnel on the test VPS (`TestE2E_Heavy_PerClientRouting`, PASS, `latest handshake: 5 seconds ago`).** Userspace (`System: false`) amnezia WORKS in our patched build — `patches/wireguard-go-awg-overlap.patch` fixes the upstream `chacha20poly1305` panic that crashed userspace amnezia; do NOT switch to `System: true` to "make amnezia work". The fixes:
    **SUPERSEDED by #11 / the kernel-AWG rework (2026-07-03):** the user-facing AWG servers (chain entry, standalone, exit) NO LONGER use userspace endpoints — they use kernel `awg-quick@awg0` + sing-box TUN-overlay (`include_interface:["awg0"]`). See `docs/PROGRESS.md` §1.A. The "do NOT switch to System: true" note applied to the userspace-endpoint path that's now gone for user-facing servers. AWG inter-node transit (linear multihop) STILL uses userspace endpoints (point-to-point between nodes, not a DPI surface) — there #10's note still applies. Do NOT revert the kernel-AWG rework back to userspace endpoints for user-facing AWG (it was unstable — `VPN/docs/sing-box-extended.md` documents the userspace amnezia panic; the kernel path is the documented stable one).
    - **Persist CPS I1-I5 on the chain** (`model.Chain.AWGCPSI1..I5` + level/mimicry). `EnsureChainAWGMaterial` generates them once in `ApplyChain` (idempotent); `ChainAWGObfsMaterial` reconstructs; `BuildAWGAmnezia`/`BuildAmneziaSection` take a `*AWGObfsMaterial` and reuse it instead of generating fresh random I1-I5 per render. Without this server and client got different I1-I5 → CPS handshake broke for every default preset (all CPS=on).
    - **Client .conf uses the chain's preset** (`resolveChainPreset(c)` + `ChainAWGObfsMaterial(c)`), not `GetDefaultPreset()`. A chain with a non-default `ObfuscationProfile` previously got a server endpoint on one preset and a client .conf on the default → divergent Jc/S1/S2/H → handshake fail.
    - **awg-quick .conf: amnezia fields go in `[Interface]`, BEFORE `[Peer]`.** `awg-quick` strips the .conf and passes it to `awg setconf`, which parses amnezia fields only within `[Interface]`; after `[Peer]` it fails with `Line unrecognized: Jc=...`. `amneziawg-tools` v1.0.20260618-2 (latest) `setconf` is by-design plain-WireGuard-only; amnezia goes via UAPI (`awg set jc ...`), and awg-quick handles it only when the fields sit in `[Interface]`.
    - **No TUN inbound for the userspace AWG user-entry.** The userspace endpoint manages its own interface; an extra `TUNInbound{AutoRoute: true}` hijacked the host's default route and broke the VPS's own networking. TUN is only for kernel-mode AWG (`System: true` + `bind_interface`), which the chain user-entry does not use.
    **Open:** egress through the AWG tunnel (curl via the interface) was traced via real-VPS sing-box trace logs: the AWG side is fine (handshake passes, `router: match[0] inbound=user-in => route(out-www)` fires, `outbound/vless[out-www]` dials exit). The failure is in the **Reality transit handshake** — exit logs `hs.c.isHandshakeComplete: false`, `forwarded SNI:` empty. Reality keys/SNI/shortid/fingerprint all match (verified by X25519-derive + config diff). This is a **separate Reality-transit bug**, NOT AWG — tracked as its own open item, not an AWG gap.
    **Fixed audit gaps:** S3/S4/ITime now held in `AWGPreset` (`BuildAmneziaSection` copies S3/S4; ITime is held in Go only via `AmneziaOptions.ITime json:"-"` and is NEVER written to any `.conf` — awg setconf rejects "Itime" and sing-box UAPI rejects "itime", see commit 6f1a108); H1-H4 now proper quadrant ranges (`GenAWGParams` wired into `GenerateAWGObfsMaterial`, persisted on `model.Chain.AWGH1..H4`, server↔client identical, width >= 1000); `RenderAWGHop` MTU 1280→1420 (matches all other AWG endpoints). **SUPERSEDED by the kernel-AWG rework (#11 / 2026-07-03):** standalone AWG multi-peer and the takeover userspace-endpoint approach described below were the PRE-rework state. Now: standalone AWG emits a kernel awg0.conf (RenderServerAWGConf) + TUN-overlay, NOT a userspace endpoint; AWG takeover KEEPS awg-quick@awg0 running (does NOT disable it) and pushes a TUN-overlay config (renderAWGTakeoverConfig). See `docs/PROGRESS.md` §1.A. The legacy CLI `Backend.ApplyConfig` standalone-AWG path still uses `RenderAWGHop` (userspace) — known follow-up.
    **Still-open audit gaps:** `I1Packet` parsed but unused; **takeover'd AWG per-client routing CLOSED v0.4.0** — `MaterializeAWGPeersAsUsers` creates `model.User` entries from imported peers (deterministic ID, dedup, idempotent), `RenderTakeoverAWGConf` renders a fresh awg0.conf from the materialized users + imported ServerConfig, pushed via `PushConfigWithAWG` (atomic awg0.conf + sing-box, rollback both). **server-IP collision CLOSED v0.4.0** — `NodeInbound.AWGServerAddress` (default 10.8.0.1/24) + multi-AWG-interface (awg0/awg1): a chain AWG entry (awg0, 10.8.0.1/24) + a standalone AWG inbound with a distinct subnet (`AWGServerAddress`, e.g. 10.8.1.1/24) now COEXIST on one node — the chain entry keeps awg0, the standalone deploys on a SECOND kernel AWG interface (awg1) with its own `awg-quick@awg1` unit + subnet + PostUp FORWARD rules. `RenderNodeAWGConfs` multi-file emit. `tunIncludeInterfacesForNode(node, nodeInfo)` appends awg1 to the TUN overlay `include_interface` list. Tests: multi-interface render, InterfaceName PostUp, include list. User `AWGAddress` reuse across inbounds on different nodes is safe.
11. **Hysteria2 is FROZEN (transport + standalone + user entry) — do NOT implement or fix.** Like TUIC (#6), all Hysteria2 paths are paused: QUIC requires TLS (self-signed cert plumbing — same hassle class as TUIC, deferred until AWG/Reality+XHTTP/MTProxy are stable — the agreed base minimum). Inter-node `TransportHysteria2` has no builder — `buildMergedNodeConfig` hard-errors. Standalone hysteria2 inbound exists in code but is **not a product target**; UI blocks new selection. Do NOT write Hysteria2 builders, E2E tests, or cert/TLS fixes. Revisit only after explicit user request AND core stack (AWG, Reality+XHTTP, MTProxy) is done. **Frozen enforcement is centralized in `internal/chain/frozen.go`** (`FrozenTransports` / `FrozenUserProtocols` / `FrozenStandaloneProtocols` + `Validate*` guards) and wired into every entry point: chain create/edit (`web/chains.go`), spider link create (`web/spider.go`), standalone inbound add (`web/nodes.go`), default protocol (`web/settings.go`). UI dropdowns render frozen options as `<option ... selected disabled>` (edit-only, never newly selectable) in `chains.templ` / `nodes.templ` / `users.templ` / `settings.templ`. **Edit-guard nuance (fix 2026-07-04):** existing chains/inbounds that already use a frozen protocol may be re-saved as-is — `handleUpdateChain` only validates when the value actually *changes* (`!= c.Transport` / `!= c.UserProtocol`), mirroring the `settings.go` `DefaultProtocol != dp` guard. Submitting the unchanged `selected disabled` option (empty form value) preserves the frozen protocol. Switching a non-frozen chain TO a frozen one is rejected with 400. Covered by `TestHandler_UpdateChain_PreservedFrozenProtocol` + `TestHandler_UpdateChain_RejectsSwitchToFrozen`. Also note (kernel-AWG rework, 2026-07-03): user-facing AWG servers (chain entry, standalone, exit) now use kernel `awg-quick@awg0` + sing-box TUN-overlay — NOT userspace `WireGuardEndpoint` (that path is unstable under amnezia; see `docs/PROGRESS.md` §1.A). The legacy CLI `Backend.ApplyConfig` standalone-AWG path (`cmd/angry-box/main.go:673` via `RenderAWGHop`) STILL uses a userspace endpoint — a known follow-up to convert to `pushConfigWithAWG` or deprecate in favor of `ApplyMergedNode`. Do NOT revert the kernel-AWG rework.
13. **Traffic status (live VPS, re-confirmed 2026-07-08 on fresh GCloud VPSes — see `docs/PROGRESS.md` §13.4):** Inter-node forwarding (XHTTP/Reality transport) **works** — `TestE2E_Heavy_Protocol_AWG_Kernel_2Hop` PASS. Kernel AWG handshake + balancer deploy **work** — `TestE2E_Heavy_Protocol_AWG_Kernel` PASS (full self-staging on a clean Debian 12: sing-box-extended binary + amneziawg kernel module via PPA + awg-quick@awg0 + TUN-overlay). AWG per-client handshake **VERIFIED** — `TestE2E_Heavy_PerClientRouting` PASS: `latest handshake: 5 seconds ago` (persisted CPS I1-I5 server↔client identical — амнезия обфускация end-to-end работает на свежих VPS). Earlier session (2026-07-04, old VPSes) also verified full client→internet egress `curl --interface awg0 ifconfig.me → exit IP`; 3 routing bugs found & fixed then (see `docs/PROGRESS.md` §11): (1) exit `MASQUERADENetwork` covers BOTH user `10.8.0.0/24` AND balancer-link `10.10.0.0/24`; (2) `tunIncludeInterfaces` includes `awg-exit-nX`; (3) `RenderServerAWGConf`/`RenderExitAWGConf`/`RenderExitServerAWGConf` PostUp emit `sysctl net.ipv4.conf.<iface>.rp_filter=0`. **CLOSED 2026-07-18 (§22):** egress через client-side AWG tunnel **VERIFIED** на реальной cross-machine топологии n1→n2 (оба kernel 6.12): `curl ifconfig.me` через туннель вернул IP сервера (144.31.157.106). A/B показал, что egress работает и БЕЗ auto_redirect (ip-rule include_interface path на 6.12 корректно захватывает forwarded ingress) — симптом §13.4 был артефактом same-host-client топологии теста (клиент на той же VPS, hairpin через внешний IP), не продуктовым багом. auto_redirect = opt-in harness (`AB_AWG_AUTO_REDIRECT=1`). See `docs/PROGRESS.md` §8/§11/§13.4/§21/§22.
14. **AWG CPS live capture = QUIC only, not plain TCP TLS.** `quic-live` + `AWGCPSCaptureDomain` → `CaptureQUICSignature` (UDP/QUIC, TLS ClientHello inside QUIC CRYPTO frame) **works** and is integrated in `EnsureChainAWGMaterial`. Plain TCP TLS capture (TLS handshake over TCP:443 without QUIC wrapper) is **unsupported** for AWG (awg-manager: crashes). Do not confuse the two — see `docs/PROGRESS.md` §0.7, `awgcapture.go` header.
12. **AWG multi-node = BALANCER architecture, NOT linear chain with userspace WG transport.** The working dns.idoctor.mom reference uses kernel `awg-exit-n1..n4` + `bind_interface` (0 userspace WG endpoints), NOT a linear chain with `Transport=AWG` (userspace WG inter-node — handshake works, data plane fails under amnezia). The `NodeRoleExit` + `ExitTargets` + `AWGExitLink[]` model (§1.A) implements this. `RenderExitAWGConf` MUST emit `Table = off` in [Interface] — without it awg-quick installs a default route (AllowedIPs=0.0.0.0/0) through the exit tunnel, capturing ALL egress including SSH → **VPS lockout**. sing-box `bind_interface` handles routing instead (no route table entry needed). Exit server awg0.conf MUST have `MASQUERADE` (PostUp) for the user subnet (10.8.0.0/24) — without it the exit sends packets with the user's private IP as source, the internet can't route responses back. User-entry awg0.conf MUST have PostUp/PostDown `FORWARD` rules between awg0 and sing-box-tun (the TUN overlay interface). See `docs/PROGRESS.md` §8 for the full dns.idoctor.mom comparison. **NEVER bring up a full-tunnel AWG client (AllowedIPs=0.0.0.0/0) on a VPS you're SSH'd into without Table=off** — it will lock you out (happened twice on server-1).

15. **`ResolveNodes` preserves ALL transit/exit/role fields (v0.5.0 fix).** `ResolveNodes` (`internal/chain/store.go`) copies the stored `ChainNode` wholesale then overwrites only the live-Host fields (`ID/Addr/User/KeyPath`) + `Inbounds` (from `NodeInfo`). The previous version rebuilt a fresh struct copying only `Port + TransitPrivKey/ShortID/UUID + Inbounds`, dropping `Role, ExitTargets, TransitAWG*, ExitAWG*, ExitAWGLinks` — a latent re-apply bug (AWG chains broke after an orchestrator restart because keys were regenerated) that also blocked relocation. Do NOT add a field to `ChainNode` without making sure `ResolveNodes` carries it (it now does so implicitly via the wholesale copy — but a new field that must be Host-sourced needs an explicit overwrite line). Backups (`ExportNode`/`ImportNode`) + relocation (`RelocateNode`) both rely on this: a node's transit material is reused across re-apply/relocate so other nodes + existing clients are not reconfigured. See `docs/PROGRESS.md` §14.
16. **kernel 6.12 (Debian 13): `awg setconf` REJECT'ит I1-I5 в теле .conf — НИКОГДА не пиши CPS в server confs.** I1-I5 (CPS decoy-пакеты) — initiator-only: receive-путь модуля amneziawg не читает ispecs вообще (`receive.c` — 0 использований; responder дропает CPS как unknown junk). На kernel 6.1 парсер setconf их принимал (мягче), на 6.12 — `Unable to modify interface: Invalid argument` и awg-quick откатывает интерфейс (live-проверено на n2). Поэтому (fix dc72ca3): `RenderServerAWGConf`/`RenderExitServerAWGConf` НИКОГДА не пишут I1-I5; `RenderExitAWGConf` (инициатор exit-линка) применяет их через PostUp `awg set <iface> i1..i5` (netlink set-путь принимает на всех ядрах). Client app confs (`RenderClientAWGConf`, web/users.go) сохраняют I1-I5 inline — приложения AmneziaWG парсят нативно; для awg-quick-клиента на Linux 6.12 выносить их в PostUp `awg set` (см. `cmd/awgtrial`). **Peer `AdvancedSecurity`** — auto-detect: модуль выставляет его по `mh_validate` входящего init (noise.c consume_initiation), руками включать не нужно. **Debian 13 prerequisites на ноде:** `apt install iptables nftables openresolv` — Debian 13 не ставит iptables из коробки (наши PostUp MASQUERADE/FORWARD используют iptables-shim → без пакета awg-quick up падает), `auto_redirect` в sing-box требует nftables. **Standalone AWG рендерит H1-H4 как деградацию `1984-1984`** (BuildAmneziaSection nil-material fallback из int-пресета) — работает, но fingerprintable (фиксированные маленькие type-значения); chain-путь использует persisted material с proper quadrant ranges. Follow-up: persisted obfs material для standalone AWG инбаундов.
17. **Jc=120 (дефолтный пресет) убивает handshake на lossy-сетях.** Перед каждым handshake initiation клиент шлёт Jc junk-пакетов плотным UDP-флудом; бюджетные хостинги (play2go и подобные) дропают часть флуда — включая сам init (1 пакет из ~126) → handshake никогда не завершается (live-воспроизведено n1→n2: с Jc=120 handshake=0 со всеми junk/CPS долетающими, с `awg set <iface> jc 3` — мгновенный handshake + egress). На premium-сетях (GCloud) Jc=120 проходит. **Серверный Jc вреден даже сильнее**: response сервера тоже едет в 120-пакетном флуде и дропается на return-path (live-подтверждено 2026-07-19: client init долетает, response теряется → handshake=0 при полном совпадении ключей/H). Это DPI/robustness tradeoff пресетов (`awgpresets_gen.go`): при "handshake не идёт на боевом VPS" — первым делом проверить Jc пресета и попробовать меньше (`awg set <iface> jc 3` на ОБЕИХ сторонах). Пресет по умолчанию НЕ менять без явного запроса (DPI-профиль — продуктовое решение).
18. **v0.8 IA-модель: InboundProfile + Chain Levels (schema v2).** Инварианты, которые нельзя нарушать:
    - **Размещение профиля = `NodeInbound.ProfileID`** (единственный source of truth). У профиля НЕТ списка нод — `store.ProfileNodes` вычисляет его. Единственный писатель связи — `ApplyProfileToNodes` (`profile_deploy.go`, diff-семантика: pre-flight port-conflict, креды один раз, remove отказан при chain-ссылке).
    - **`Chain.Levels` — source of truth топологии; `Chain.Nodes` — производная** (синк в `SaveChain`). Читать узлы ТОЛЬКО через `AllNodes()` (копия!) / мутировать через `EachNode`/`NodeByID`/`SetAllNodes` — иначе ключи уйдут в throwaway-копию и потеряются при SaveChain.
    - **`ChainNode.InboundRef`** — ссылка entry-ноды на профиль (обязательна на level 0; на transit/exit — параметризация transport-listener, заложено, UI «advanced»). Entry render читает материализованный инбаунд; поля `Chain.AWGEntry*`/`AWGCPS*` — legacy fallback для немигрированных.
    - **Chain-sourced инбаунды (`Source="chain:*"`) пропускаются во всех standalone-циклах** (`IsChainSourcedInbound`) — merged config, AWG confs, detectPortConflicts, TUN includes, web links. Пропуск забыть = двойной listener / фантомный port-conflict / лишний awg1.
    - **AWG inter-node транспорт — только линейный** (1 нода/уровень; `ValidateChainTopology` отказывает громко). Группы — через XHTTP/Reality; multi-exit AWG — через существующий kernel-балансер (ExitTargets/AWGExitLinks, нетронут).
    - **Стратегия групп: дефолт `fallback` («Round-robin (fallback)», патч sing-box-extended, прод-проверен)**; urltest — только явный выбор (probes через транзит флаки); failover ≈ urltest tight; selector → native.
    - **Миграция v1→v2** (`migrate_v2.go`): standalone→профили (collapse с audit), chain entry→`chain-entry-<name>` с СУЩЕСТВУЮЩИМИ кредами (render-equivalence тест = байт-идентичность awg0.conf). Backup v1 при ImportStore прогоняет её же.
    - **Services удалены** (страница/роуты/applyServiceToUser); `PanelSettings.Services` dormant (backup-compat), `User.ServiceID` игнорируется. Клиент = имя + цепочки; protocols derive из цепочек.
## Commit Convention

- `fix:` — bug fixes
- `feat:` — new features
- `test:` — test additions
- `docs:` — documentation
- `refactor:` — code restructuring
- Сообщения коммитов — на русском (если не запрошено иное).
- Commits end with: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`

---

**Remember:** You are building a premium, centralized orchestrator. The code should be clean, the UI should be fast and responsive, and the remote nodes should be treated as disposable execution environments dictated by the orchestrator's state.
