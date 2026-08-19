# 02 — Architecture

Extracted from AGENTS.md. This file is project law.
amnezia-box rebase law: `docs/PATCHES.md`.

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

## amnezia-box (our base sing-box fork, NOT plain sing-box)

- Project uses **amnezia-box** — our fork `AlexeyLCP/amnezia-box` (a fork of
  `hoaxisr/amnezia-box`, which is itself sing-box 1.14 beta). It carries:
  - the AWG3 userspace endpoint `type:"awg"` (amneziawg-go `/v3` pinned in
    the fork's go.mod — `hoaxisr/amneziawg-go/v3 @ e32b3b0`, the InputPackets API
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
  `type:"awg"` endpoint is used for: (a) INTER-NODE TRANSPORT (linear transit),
  (b) the legacy CLI standalone path (userspace amneziawg-go in-process), and
  (c) AWG 3.0 user-entry — opt-in per-inbound (`AWG3Mode`), userspace-only
  because the kernel module rejects the AWG3 fields; live-verified on n1, see
  AGENTS #5 / PROGRESS §38. Kernel awg-quick `.conf` path
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
