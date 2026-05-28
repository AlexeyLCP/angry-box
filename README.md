**Languages:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

# Angry-BOX

Lightweight SSH-only orchestrator for **sing-box** (primary) and **xray** (secondary) on remote machines and routers.

No agents on the nodes. Everything happens over SSH + minimal proxy install on the far side.

## Architecture (simple and honest)

- The **orchestrator** is only the "head". It never proxies traffic itself.
- Management is **SSH only**. No persistent agents on nodes (including Keenetic).
- On remote machines (VPS, Keenetic, other routers) you only deploy the proxy (sing-box or xray) + tiny config + init script.
- You can even run angry-box itself on a Keenetic (it acts purely as the control plane).

### Two connection types

- **Transport** — technical hops that chain the nodes together (XHTTP recommended, Reality+TCP as fallback).
- **User** — real entry points for clients (TUIC v5 or AmneziaWG).

## 2026 Stealth Presets (the killer feature)

We took the best public obfuscation research from the community as of mid-2026 (pumbaX/awg-multi-script, Xray XHTTP #4113 RPRX, Hysteria Gecko ideas, NaiveProxy headers, Hiddify/3x-ui patterns) and turned them into modular, force-enabled profiles.

**Security > Compatibility** is the policy for the strong profiles.

Included out of the box:
- `russia_2026`, `iran_2026`, `china_2026`
- `maximum_stealth_2026`
- `pro_2026` — forces full CPS level 3 + 1200-byte QUIC Initial (Chrome fingerprint) for AWG
- `xhttp_max_stealth_2026` — extreme XHTTP padding + XMUX + full CPS3+QUIC

AWG now ships with proper I1-I5 generators (QUIC Initial exactly 1200B, realistic SIP REGISTER, DNS+EDNS0, short-header packets) when you use the pro/stealth presets.

XHTTP transport gets realistic browser headers + padding ranges on both sing-box and xray backends.

### Advanced XHTTP Obfuscation

We ported and implemented many state-of-the-art techniques from the community:
- Random-range header padding
- XMUX-style multiplexing controls
- Realistic browser-like headers
- Upstream/Downstream separation hints

These are available for both sing-box and xray backends.

**Credits / Acknowledgments** (we stand on the shoulders of giants):

- pumbaX / awg-multi-script (the entire CPS + QUIC/SIP/DNS generator approach — "бери все")
- RPRX + Xray community (XHTTP padding, XMUX, extra, realistic flow control — PR #4113 and related)
- Hysteria2 Gecko obfuscation ideas
- NaiveProxy header realism
- Hiddify, 3x-ui, Telemt and the broader Russian/Iranian/Chinese proxy research community (2025-2026)

## Installation

# Or with a local binary
sh scripts/install.sh --local ./angry-box
```

### Keenetic (Entware / NDMS)

```bash
# After opkg install (see below) or via the generic installer
sh scripts/install.sh --version 0.2.0
```

### OpenWRT / routers via .ipk (new in 0.2.0)

```bash
# Keenetic (mipsel_24kc)
opkg install angry-box_0.2.0_mipsel_24kc.ipk

# OpenWRT aarch64 (e.g. many modern devices)
opkg install angry-box_0.2.0_aarch64_cortex-a53.ipk
```

The packages install the binary to `/usr/bin/angry-box` (or `/opt/bin` on Entware), create directories, and provide a basic S99 init script.

## Quick Start

```bash
# 1. Register hosts (the orchestrator talks to them only via SSH)
angry-box host add node1 --addr 10.0.0.1:22 --user root --key ~/.ssh/id_ed25519
angry-box host add node2 --addr 10.0.0.2:22 --user root --key ~/.ssh/id_ed25519

# 2. Deploy sing-box (or xray) on them
angry-box deploy -addr 10.0.0.1 ...
angry-box deploy -addr 10.0.0.2 ...

# 3. Create a chain using a 2026 stealth preset
angry-box chain create mychain --nodes node1,node2 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# 4. Apply (this is where the magic CPS + XHTTP generators run)
angry-box apply-chain mychain

# 5. Get client configs (including ready AWG with the exact I1-I5 that were deployed)
angry-box config -type user --protocol awg --profile pro_2026 --client-pubkey <pub>
```

## Support

- GitHub Issues: https://github.com/alexeylcp/angry-box/issues
- For router-specific problems (opkg, init scripts, Entware) please include the exact device model + OpenWRT/NDMS version.

## License

MIT

---

**This is v0.2.0** — the first release with the full 2026 obfuscation engine and real router packaging.
=======
# Latest version
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# Specific version
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.2.0

# Local binary
sh scripts/install.sh --local ./angry-box
```

The script automatically detects Linux (systemd) and Keenetic (Entware/NDMS) environments.

### Uninstall / Update

```bash
sh scripts/install.sh --uninstall
sh scripts/install.sh --version 0.3.0   # update
```

## Quick Start

```bash
# 1. Add hosts
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519
angry-box host add node2 --addr 203.0.113.11:22 --user root --key ~/.ssh/id_ed25519

# 2. Deploy sing-box to hosts
angry-box deploy --host node1
angry-box deploy --host node2

# 3. Create and apply a chain with strong 2026 profile
angry-box chain create mychain --nodes node1,node2 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# 4. Apply (generates configs, pushes via SSH, returns rich report with AWG keys + CPS)
angry-box apply-chain mychain

# 5. Check status
angry-box chain show mychain
angry-box status --host node1
```

Standalone config generation (no chain needed):

```bash
angry-box config -type user --protocol awg --profile xhttp_max_stealth_2026
```

## Features

- Pure SSH management + automatic rollback on failure
- Rich per-node ApplyReport (including AWG server public key + stable CPS I1-I5 when applicable)
- Stable AWG user-entry credentials (keys + CPS packets generated once)
- Advanced XHTTP transport with research-grade obfuscation parameters
- Modular 2026 presets with external JSON support
- Full parity between `apply-chain` and standalone `config` commands
- Cross-build support (amd64, arm64, Keenetic mipsel)

## Support

- Open an issue on GitHub for bugs and feature requests.
- For general discussion and help with setups in censored networks, use GitHub Discussions.
- Real-world feedback from Russia, Iran and China environments is extremely valuable.

If you run this in production or lab conditions against real DPI, please share sanitized results — it helps improve the presets.

## Acknowledgments / Credits

See the detailed credits at the bottom of this document (and in the language-specific versions). Angry-BOX heavily builds upon public research and tools from the anti-censorship community.

## License

PolyForm Noncommercial License 1.0.0

---

## Language / Язык / 语言 / زبان

[English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

---

## Acknowledgments / Credits

Angry-BOX stands on the shoulders of the broader anti-censorship community. We drew heavy inspiration (and in some cases directly ported ideas for our preset generators) from the following projects and researchers.

### Core Obfuscation Techniques & Research
- **pumbaX / awg-multi-script** — The excellent AmneziaWG CPS/I1–I5 generators (QUIC Initial 1200B Chrome-like, SIP REGISTER, DNS+EDNS0, Pro ranges). We took "бери все".
- **XTLS / Xray-core team (especially RPRX)** — The groundbreaking XHTTP transport and "XHTTP: Beyond REALITY" research. Header padding ranges, XMUX, upstream/downstream separation.
- **klzgrad / NaiveProxy** — Real Chromium network stack + realistic preamble behavior.
- **apernet / Hysteria2 team** — Salamander and the 2026 Gecko obfuscation.
- **telemt / telemt** — Modern high-quality Fake-TLS MTProto proxy with excellent double-hop patterns.

### Community Configs, Installers & Presets
- TheyCallMeSecond/config-examples
- mack-a/v2ray-agent
- Hiddify-Manager
- CELERITY-panel, 3x-ui forks, and many Russian/Iranian/Chinese community configs.

Special thanks to everyone publishing real-world test results against RKN, GFW, and Iranian DPI in 2025–2026.
