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
└── scripts/             # install.sh, systemd service, Keenetic init, build-opkg
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

## E2E Testing Infrastructure

- **GCloud project:** `project-d4c6c72c-4f10-4288-902`
- **Test servers:**
  - `vps-de-test-1` — 34.40.120.7 (Debian 12, key: `google_compute_engine`)
  - `vps-de-test-2` — 35.198.166.183 (Ubuntu 24.04, key: `id_ed25519`)
  - `vps-de-test-3` — 35.198.100.1 (Ubuntu 24.04, key: `id_ed25519`, свежий)
- Run E2E: `go test -tags e2e ./internal/chain/ -run TestE2E -v -timeout 300s`
- Auth: `gcloud auth login lucipoher@gmail.com`

## sing-box-extended (NOT plain sing-box)

- Project uses **sing-box-extended** (`1.13.14-extended-2.5.0-patched`) — NOT official sing-box.
  This is a patched build (see `patches/`: wireguard-go chacha20poly1305 overlap fix + fallback round-robin).
  The full rebasing procedure + the `patchcheck` regression test (gated by the
  `patchcheck` build tag) are documented in **`docs/PATCHES.md`** — that is the
  law for bumping the upstream sing-box-extended / wireguard-go tags.
- Binary in `deps/sing-box-1.13.14-extended-2.5.0-patched-linux-amd64.tar.gz`
- Installed by `angry-box deploy` which downloads from the project's GitHub deps (weak VPSes never compile Go — they just download).
- Supports: amnezia field on wireguard endpoints, CPS/I1-I5 packets, MTProto, XHTTP max obfuscation.
- AWG kernel module built from `deps/amneziawg-src.tar.gz` (kernel awg-quick + sing-box `bind_interface`).
- Module requires: `curve25519_x86_64`, `libcurve25519_generic`, `udp_tunnel`, `ip6_udp_tunnel`

## Known Issues & Workarounds

1. **TUIC requires TLS cert** — auto-generated via `buildTUICTLSOptions()`, written with base64 (heredoc fails)
2. **DNS/Route disabled** in merged config (sing-box 1.13 detour bugs) — minimal config works. The previously-retained `buildMergedRouting`/`buildMergedDNS` dead builders were removed (CTO-review M10); re-implement against the live sing-box version when the detour bug is fixed.
3. **Multi-node chains** need Route/DNS re-enabled when detour is fixed
4. **No Python on test servers** — use `python3` explicitly when available
5. **AMG amnezia field** — only works with sing-box-extended, skipped for plain sing-box
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
13. **Traffic status (live VPS, re-confirmed 2026-07-08 on fresh GCloud VPSes — see `docs/PROGRESS.md` §13.4):** Inter-node forwarding (XHTTP/Reality transport) **works** — `TestE2E_Heavy_Protocol_AWG_Kernel_2Hop` PASS. Kernel AWG handshake + balancer deploy **work** — `TestE2E_Heavy_Protocol_AWG_Kernel` PASS (full self-staging on a clean Debian 12: sing-box-extended binary + amneziawg kernel module via PPA + awg-quick@awg0 + TUN-overlay). AWG per-client handshake **VERIFIED** — `TestE2E_Heavy_PerClientRouting` PASS: `latest handshake: 5 seconds ago` (persisted CPS I1-I5 server↔client identical — амнезия обфускация end-to-end работает на свежих VPS). Earlier session (2026-07-04, old VPSes) also verified full client→internet egress `curl --interface awg0 ifconfig.me → exit IP`; 3 routing bugs found & fixed then (see `docs/PROGRESS.md` §11): (1) exit `MASQUERADENetwork` covers BOTH user `10.8.0.0/24` AND balancer-link `10.10.0.0/24`; (2) `tunIncludeInterfaces` includes `awg-exit-nX`; (3) `RenderServerAWGConf`/`RenderExitAWGConf`/`RenderExitServerAWGConf` PostUp emit `sysctl net.ipv4.conf.<iface>.rp_filter=0`. **OPEN (not a blocker):** egress routing through the *client-side* AWG tunnel (`curl --interface awge2e` from a per-user .conf brought up ON the entry VPS) returns empty on the 2026-07-08 fresh VPSes — handshake passes (the AWG-obfuscation proof) but sing-box TUN-overlay trace shows no router match for `awge2e` traffic. This is the same item as the earlier "Per-client `source_ip_cidr` under TUN-overlay still needs real-VPS verify" — routing polish for the client-side tunnel, a separate debugging task (tcpdump + sing-box trace debug). See `docs/PROGRESS.md` §8/§11/§13.4/§21 (§21: upstream WebSearch research — include_interface SHOULD capture forwarded ingress per docs/impl; candidates = auto_redirect not enabled + SagerNet#3805 multi-interface empty-set bug; diagnose via `nft list chain inet sing-box prerouting` + `ip rule show`).
14. **AWG CPS live capture = QUIC only, not plain TCP TLS.** `quic-live` + `AWGCPSCaptureDomain` → `CaptureQUICSignature` (UDP/QUIC, TLS ClientHello inside QUIC CRYPTO frame) **works** and is integrated in `EnsureChainAWGMaterial`. Plain TCP TLS capture (TLS handshake over TCP:443 without QUIC wrapper) is **unsupported** for AWG (awg-manager: crashes). Do not confuse the two — see `docs/PROGRESS.md` §0.7, `awgcapture.go` header.
12. **AWG multi-node = BALANCER architecture, NOT linear chain with userspace WG transport.** The working dns.idoctor.mom reference uses kernel `awg-exit-n1..n4` + `bind_interface` (0 userspace WG endpoints), NOT a linear chain with `Transport=AWG` (userspace WG inter-node — handshake works, data plane fails under amnezia). The `NodeRoleExit` + `ExitTargets` + `AWGExitLink[]` model (§1.A) implements this. `RenderExitAWGConf` MUST emit `Table = off` in [Interface] — without it awg-quick installs a default route (AllowedIPs=0.0.0.0/0) through the exit tunnel, capturing ALL egress including SSH → **VPS lockout**. sing-box `bind_interface` handles routing instead (no route table entry needed). Exit server awg0.conf MUST have `MASQUERADE` (PostUp) for the user subnet (10.8.0.0/24) — without it the exit sends packets with the user's private IP as source, the internet can't route responses back. User-entry awg0.conf MUST have PostUp/PostDown `FORWARD` rules between awg0 and sing-box-tun (the TUN overlay interface). See `docs/PROGRESS.md` §8 for the full dns.idoctor.mom comparison. **NEVER bring up a full-tunnel AWG client (AllowedIPs=0.0.0.0/0) on a VPS you're SSH'd into without Table=off** — it will lock you out (happened twice on server-1).

15. **`ResolveNodes` preserves ALL transit/exit/role fields (v0.5.0 fix).** `ResolveNodes` (`internal/chain/store.go`) copies the stored `ChainNode` wholesale then overwrites only the live-Host fields (`ID/Addr/User/KeyPath`) + `Inbounds` (from `NodeInfo`). The previous version rebuilt a fresh struct copying only `Port + TransitPrivKey/ShortID/UUID + Inbounds`, dropping `Role, ExitTargets, TransitAWG*, ExitAWG*, ExitAWGLinks` — a latent re-apply bug (AWG chains broke after an orchestrator restart because keys were regenerated) that also blocked relocation. Do NOT add a field to `ChainNode` without making sure `ResolveNodes` carries it (it now does so implicitly via the wholesale copy — but a new field that must be Host-sourced needs an explicit overwrite line). Backups (`ExportNode`/`ImportNode`) + relocation (`RelocateNode`) both rely on this: a node's transit material is reused across re-apply/relocate so other nodes + existing clients are not reconfigured. See `docs/PROGRESS.md` §14.
## Commit Convention

- `fix:` — bug fixes
- `feat:` — new features
- `test:` — test additions
- `docs:` — documentation
- `refactor:` — code restructuring
- Commits end with: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`

---

**Remember:** You are building a premium, centralized orchestrator. The code should be clean, the UI should be fast and responsive, and the remote nodes should be treated as disposable execution environments dictated by the orchestrator's state.
