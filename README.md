<div align="center">
  <img src="docs/assets/logo.png" alt="Angry-BOX Logo" width="250"/>
  <h1>Angry-BOX</h1>
  <p><strong>The Ultimate Automated Proxy Orchestrator for sing-box-extended</strong></p>

  <p>
    <a href="https://github.com/AlexeyLCP/angry-box/releases"><img src="https://img.shields.io/github/v/release/AlexeyLCP/angry-box" alt="Release"></a>
    <a href="https://golang.org"><img src="https://img.shields.io/github/go-mod/go-version/AlexeyLCP/angry-box" alt="Go Version"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-PolyForm%20Noncommercial-blue.svg" alt="License"></a>
  </p>
  <p>
    <i>Build impenetrable multi-hop, heavily obfuscated VPN chains with zero manual configuration.</i>
  </p>
</div>

---

**[English](README.md) | [Russian](README.ru.md) | [Chinese](README.zh.md) | [Farsi](README.fa.md)**

## Overview

**Angry-BOX** is an advanced, lightweight orchestrator designed to fully automate the deployment, configuration, and management of anti-DPI proxy nodes across multiple servers. 

Built exclusively around **[sing-box-extended](https://github.com/shtorm-7/sing-box-extended)**, Angry-BOX seamlessly configures complex proxy topologies (such as multi-hop chains with `VLESS-Reality`, `XHTTP`, and `AmneziaWG`) directly over SSH, removing all the friction from setting up robust, censorship-resistant infrastructure.

## Features

- **Automated Orchestration:** No need to manually write complex `sing-box` JSON configs. Angry-BOX generates, validates, and deploys configs over SSH in seconds.
- **Advanced Obfuscation Protocols:** Native support for `AmneziaWG`, `XHTTP`, `VLESS-Reality`, and `Hysteria2`.
- **Multi-Hop Chains:** Easily construct 2-node or 3-node proxy chains to route traffic securely through multiple jurisdictions.
- **Failover & Load Balancing:** Built-in support for `urltest`, `failover`, and `selector` strategies.
- **Modern Web UI:** Control everything from a sleek, responsive dashboard built with HTMX and TailwindCSS.
- **100% Independent:** Angry-BOX stores all critical dependencies (like `sing-box-extended` binaries and `amneziawg` kernel modules) locally.
- **Zero-Footprint:** Node servers only run the bare `sing-box` core. The orchestrator lives entirely on your control machine.

## What's New in v0.7.1

- **Unified Merged Node Config** -- nodes can simultaneously serve standalone inbounds AND participate in multiple proxy chains. The new `apply-merged` engine builds a single `config.json` combining all roles: standalone inbounds + chain transport inbounds + chain user entries + unified routing/DNS. No more config overwrites.
- **Pre-Flight SSH Check** -- verifies SSH connectivity to all nodes BEFORE touching any remote config, preventing partial deployments.
- **Port Conflict Detection** -- the merged config builder detects and reports port conflicts between chains and standalone inbounds before deploy.
- **Tag Diff Observability** -- after each apply, the UI displays which inbound/endpoint tags were added and removed compared to the previous config.
- **Automatic SSH Key Generation** -- Capture Node UI can auto-generate SSH keypairs and install them on remote hosts in one click.
- **i18n Support** -- Web UI available in English, Russian, Chinese, and Farsi.
- **Standalone Node Config** -- apply individual node inbounds independently, with the same rollback protection as chains.

## Screenshots

<div align="center">
  <img src="docs/assets/dashboard.png" alt="Dashboard" width="800" style="border-radius: 8px; box-shadow: 0 4px 10px rgba(0,0,0,0.5); margin-bottom: 20px;"/>
  <br>
  <em>The Angry-BOX Web UI Dashboard</em>
</div>

## Architecture

Unlike traditional panels that require heavy agents on every server, Angry-BOX takes a **stateless agentless approach**:

```mermaid
graph LR
    Client((Client<br/>AmneziaWG)) -->|Obfuscated Traffic| Node1[Entry Node<br/>VPS 1]
    Node1 -->|XHTTP / Reality| Node2[Exit Node<br/>VPS 2]
    Node2 -->|Clean Traffic| Web((Internet))
    
    Orchestrator[Angry-BOX<br/>Control Server] -.->|SSH / Config Push| Node1
    Orchestrator -.->|SSH / Config Push| Node2
```

## Getting Started

### 1. Installation

Download the latest release for your platform from the [Releases](https://github.com/AlexeyLCP/angry-box/releases) page, or run the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/AlexeyLCP/angry-box/main/scripts/install.sh | sh
```

### 2. Starting the Web UI

```bash
angry-box serve -listen 0.0.0.0:8090
```

*Note: On first run, a random secure password is generated for the Web UI.*

### 3. CLI Quick Start

```bash
# 1. Add your VPS nodes
angry-box host add entry-node --addr 1.2.3.4:22 --user root --key ~/.ssh/id_ed25519
angry-box host add exit-node --addr 5.6.7.8:22 --user root --key ~/.ssh/id_ed25519

# 2. Deploy sing-box core to the nodes
angry-box deploy -addr 1.2.3.4 -key ~/.ssh/id_ed25519
angry-box deploy -addr 5.6.7.8 -key ~/.ssh/id_ed25519

# 3. Create a chain
angry-box chain create my-chain --nodes entry-node,exit-node --user-protocol awg --transport xhttp

# 4. Apply the chain (uses merged config -- preserves standalone inbounds!)
angry-box apply-chain my-chain

# 5. Or apply a single node's merged config
angry-box apply-merged entry-node
```

## Third-Party Components

- **[sing-box](https://github.com/SagerNet/sing-box)** and **[sing-box-extended](https://github.com/shtorm-7/sing-box-extended)** (GPLv3)
- **[AmneziaWG Linux Kernel Module](https://github.com/amnezia-vpn/amneziawg-linux-kernel-module)** (GPLv2)
- **[awg-multi-script by pumbaX](https://github.com/pumbaX/awg-multi-script)** - AmneziaWG obfuscation best practices
- **HTMX, TailwindCSS, and DaisyUI** (MIT / BSD)

## Acknowledgements

- Special thanks to **Aleksandr SacredX** for extensive testing and valuable ideas.

## License

**PolyForm Noncommercial License 1.0.0**

Free for personal, educational, and research purposes. Commercial use requires written permission.

See [LICENSE](LICENSE) for full text.
