**Languages:** [English](README.md) | [Russian](README.ru.md) | [Chinese](README.zh.md) | [Farsi](README.fa.md)

# Angry-BOX

**完全自研的 SSH-only orchestrator / control plane。**

Angry-BOX 是从零开始编写的原创产品，不是 3x-ui、LucX-UI、x-ui 或任何其他面板的 fork。

所有管理均通过 SSH 完成，目标节点上只运行 **sing-box-extended**（可选 xray）内核 + 最小配置，无任何 agent。

## Features

- Pure SSH control plane without persistent agents
- 2026 powerful stealth presets (Russia/Iran/China/Maximum Stealth)
- Advanced AWG + full CPS + realistic QUIC/SIP/DNS generators
- High-quality XHTTP transport (padding, XMUX, realistic headers), both sing-box and xray
- Stable user credentials (AWG keys and CPS generated once per chain)
- Excellent router support (Keenetic .ipk + OpenWRT)
- Native Windows build
- Web UI + full CLI

## v0.7.2 新修复

**v0.7.1 之后的修复和加固：**
- 修复 Standalone Inbounds 保存时 Host 数据丢失的问题。
- 通过 ApplyStandaloneNode 拦截解决 Standalone Inbounds 与 Chain Transport Inbounds 的冲突。
- 修复两处 `flow: "xtls-rprx-vision"` 相关 bug。
- 在真实流量下验证了 Graceful Rollback（无效配置自动回滚到上一个工作版本）。
- 改进 merged config builder 中的 outbounds 标签提取。

## v0.7.1 新特性

- **统一节点配置（Merged Config）** -- 节点现在可以同时提供独立的入站连接（standalone inbounds），并参与多条代理链。新的 `apply-merged` 引擎构建了一个单一的 `config.json`，合并了所有角色：独立入站 + 链传输入站 + 链用户入口 + 统一的路由/DNS。不再有配置相互覆盖的问题。
- **预检 SSH 检查** -- 在修改任何远程配置之前验证所有节点的 SSH 连通性，防止部分部署失败。
- **端口冲突检测** -- 统一配置生成器在部署前会检测并报告代理链和独立入站之间的端口冲突。
- **标签差异（Tag Diff）可观测性** -- 每次应用配置后，UI 会显示与上一次相比新增（`+tag`）和移除（`-tag`）的入站/出站标签。
- **自动生成 SSH 密钥** -- 添加节点（Capture Node）的 UI 可以自动生成 SSH 密钥对，并一键安装到远程主机。
- **多语言（i18n）支持** -- Web UI 提供英语、俄语、中文和波斯语。
- **独立的节点配置** -- 独立应用单个节点的入站配置，并享有与代理链相同的回滚（rollback）保护机制。

## Quick Start

```bash
# 1. Install
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# 2. Add node
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# 3. Create chain with 2026 preset
angry-box chain create mychain --nodes node1 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# 4. Deploy (uses merged config!)
angry-box apply-chain mychain

# 5. Or apply merged config for a single node
angry-box apply-merged node1
```

Web UI at `http://localhost:8090`.

## Installation

### One-liner script (recommended)

```bash
# Latest version
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# Specific version
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.7.2
```

### Pre-built binaries

Download from [Releases](https://github.com/alexeylcp/angry-box/releases).

**Linux**
```bash
tar -xzf angry-box-0.7.2-linux-amd64.tar.gz
cd angry-box-0.7.2-linux-amd64
./angry-box --help
```

**Windows**
- Download `angry-box-0.7.2-windows-amd64.zip` or `.exe`
- Run `angry-box.exe`
- Web UI: `http://localhost:8090`

### Routers (Keenetic / OpenWRT)

See router section below.

## Architecture

Angry-BOX is only the **control plane**.

- The orchestrator does not forward traffic.
- All operations via SSH.
- Remote nodes run only a lightweight proxy (sing-box or xray) + small config.

**Two connection types:**
- **Transport** -- internal chain hops (XHTTP recommended)
- **User** -- real client entry points (TUIC v5 or AmneziaWG)

## 2026 Stealth Presets

Professional presets optimized for current DPI:

| Preset                    | Target             | Key techniques                      |
|---------------------------|--------------------|-------------------------------------|
| `russia_2026`             | Russia             | Balanced XHTTP + AWG                |
| `iran_2026`               | Iran               | Aggressive XHTTP + Reality          |
| `china_2026`              | China              | Strong obfuscation + fragmentation  |
| `maximum_stealth_2026`    | Maximum stealth    | Full XHTTP + AWG CPS                |
| `pro_2026`                | Professional use   | Forced CPS 3 + QUIC 1200B           |
| `xhttp_max_stealth_2026`  | Extreme XHTTP      | Maximum padding + XMUX              |

## Router Support

Native `.ipk` packages.

| Platform          | Architecture           | Example package                           |
|-------------------|------------------------|-------------------------------------------|
| Keenetic          | `mipsel_24kc`          | `angry-box_X.Y.Z_mipsel_24kc.ipk`         |
| Keenetic/OpenWRT  | `aarch64_cortex-a53`   | `angry-box_X.Y.Z_aarch64_cortex-a53.ipk`  |

## Building from source

```bash
git clone https://github.com/alexeylcp/angry-box.git
cd angry-box

# Production build
make build

# Dev mode
make dev
```

## Acknowledgements

Built on anti-censorship community research. Key contributors: pumbaX / awg-multi-script, Xray team (RPRX), Hysteria2, NaiveProxy, Telemt.

## License

**PolyForm Noncommercial License 1.0.0**

Free for personal, non-commercial, educational, and scientific use.
**Any commercial use is prohibited.**

See [LICENSE](LICENSE).

## Support

- Issues -> [GitHub Issues](https://github.com/alexeylcp/angry-box/issues)
- Discussion -> GitHub Discussions

---

**Current version:** 0.7.2 -- unified merged config, pre-flight checks, i18n, standalone config.
