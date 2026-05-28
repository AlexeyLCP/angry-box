# Angry-BOX

**Languages:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

[![Build & Test](https://github.com/alexeylcp/angry-box/actions/workflows/build.yml/badge.svg)](https://github.com/alexeylcp/angry-box/actions/workflows/build.yml)
[![Release](https://github.com/alexeylcp/angry-box/actions/workflows/release.yml/badge.svg)](https://github.com/alexeylcp/angry-box/actions/workflows/release.yml)

Lightweight orchestrator for managing high-obfuscation proxy chains on remote machines **via SSH only**.

**sing-box** is the primary backend. **xray** is supported as secondary (best-effort).

## Architecture Principles

- The orchestrator is a pure "head" — it never participates in the traffic chain itself.
- Management is **SSH-only**. No persistent agents are deployed to nodes.
- On remote machines (VPS, Keenetic routers, etc.) only the actual proxy (sing-box or xray) + minimal configuration + init script is installed.
- You can run Angry-BOX itself on a Keenetic device. In this case it acts only as a management head and does **not** become a proxy node in your chains.

### Two Types of Connections

- **Transport** — technical connections used to link hops inside the chain (XHTTP recommended in 2026).
- **User** — client-facing entry points (TUIC v5, AmneziaWG with advanced CPS, etc.).

## 2026 Obfuscation Presets (Security-First)

Global profile can be set in config (`default_obfuscation_profile`) or overridden per chain with `--profile`.

Current presets:
- `russia_2026`, `iran_2026`, `china_2026` — balanced regional profiles
- `maximum_stealth_2026` — aggressive
- `pro_2026` — full pumbaX Pro 2026 ranges + complete AWG CPS I1-I5 chain (level 3, QUIC primary)
- `xhttp_max_stealth_2026` — **extreme** XHTTP-focused preset (heavy random padding, aggressive XMUX, upstream/downstream separation hints + pro AWG)

**Security > Compatibility** is the explicit policy for `pro_2026` and `xhttp_max_stealth_2026`. They deliver the strongest known 2026 DPI resistance against RKN, GFW and Iranian systems, at the cost of larger client configs and possible issues with very old clients.

AWG entry credentials (server keypair + CPS I1-I5) are generated **once** at chain creation time and never rotate on re-apply.

### Advanced XHTTP Obfuscation

We ported and implemented many state-of-the-art techniques:
- Random-range header padding
- XMUX-style multiplexing controls with ranges
- Realistic browser-like headers (inspired by real Chromium stacks)
- Upstream/Downstream separation hints
- Mode selection (packet-up / stream-up)

These are available both for sing-box and xray backends.

## Installation

### From GitHub Releases (recommended)

Download the latest tarball for your architecture from [Releases](https://github.com/alexeylcp/angry-box/releases), or use the installer:

```bash
# Latest stable
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh
```

### From source / development

The recommended way is the official installer script:

```bash
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