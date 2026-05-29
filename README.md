**Languages:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

# Angry-BOX

**Lightweight SSH-only orchestrator** for **sing-box** (primary) and **xray** (secondary).

No agents on the nodes. Everything is managed over SSH. Deploy minimal proxy configs on remote machines and routers (including Keenetic).

## Features

- Pure SSH control plane — zero persistent agents on targets
- Strong 2026 obfuscation presets (Russia / Iran / China / Maximum Stealth)
- Advanced AWG with full CPS + realistic QUIC/SIP/DNS generators
- High-quality XHTTP transport (padding, XMUX, realistic headers) on both sing-box and xray
- Stable user credentials (AWG keys + CPS generated once per chain)
- First-class router support (Keenetic .ipk + OpenWRT)
- Native Windows build
- Web UI + full CLI

## Quick Start

```bash
# 1. Install
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# 2. Add hosts
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# 3. Create a chain with a strong 2026 preset
angry-box chain create mychain --nodes node1 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# 4. Deploy
angry-box apply-chain mychain
```

The web interface will be available at `http://localhost:8090`.

## Installation

### One-line installer (Linux + Keenetic)

```bash
# Latest
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# Specific version
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.5.2
```

### Pre-built binaries

Download from [Releases](https://github.com/AlexeyLCP/angry-box/releases).

**Linux**
```bash
tar -xzf angry-box-0.5.2-linux-amd64.tar.gz
cd angry-box-0.5.2-linux-amd64
./angry-box --help
```

**Windows**
- Download `angry-box-0.5.2-windows-amd64.zip`
- Extract and run `angry-box.exe`
- Web UI: `http://localhost:8090`

### Routers (Keenetic & OpenWRT)

See the detailed router installation guide below.

## Architecture

Angry-BOX is a **control plane only**.

- It never forwards traffic itself.
- All management happens over SSH.
- On remote nodes you deploy only a lightweight proxy (sing-box or xray) + small config.

**Two types of connections:**
- **Transport** — internal hops for chaining (XHTTP recommended)
- **User** — real client entry points (TUIC v5 or AmneziaWG)

## 2026 Stealth Presets

The project ships with research-grade presets tuned for current DPI systems:

| Preset                    | Focus                  | Key Techniques                     |
|---------------------------|------------------------|------------------------------------|
| `russia_2026`             | Russia                 | Balanced XHTTP + AWG               |
| `iran_2026`               | Iran                   | Aggressive XHTTP + Reality         |
| `china_2026`              | China                  | Strong obfuscation + fragmentation |
| `maximum_stealth_2026`    | Maximum resistance     | Full XHTTP + AWG CPS               |
| `pro_2026`                | Professional use       | Force CPS level 3 + 1200B QUIC     |
| `xhttp_max_stealth_2026`  | Extreme XHTTP          | Maximum padding + XMUX             |

## Router Support

Angry-BOX provides native `.ipk` packages for routers.

| Platform          | Architecture         | Package example                          | Notes                  |
|-------------------|----------------------|------------------------------------------|------------------------|
| Keenetic          | `mipsel_24kc`        | `angry-box_X.Y.Z_mipsel_24kc.ipk`        | MIPS models            |
| Keenetic/OpenWRT  | `aarch64_cortex-a53` | `angry-box_X.Y.Z_aarch64_cortex-a53.ipk` | ARM64 models           |

All router packages use the **outer-tar format** (MagiTrickle style) and fully static binaries.

See the [Releases](https://github.com/AlexeyLCP/angry-box/releases) page for the latest packages.

## Building from Source

```bash
git clone https://github.com/alexeylcp/angry-box.git
cd angry-box

# Production build (everything embedded in binary)
make build

# Development mode (static files from disk, edit without rebuild)
make dev
```

## Credits & Acknowledgments

Angry-BOX would not exist without the incredible public research from the anti-censorship community.

**Core techniques:**
- pumbaX / awg-multi-script — CPS, QUIC, SIP, DNS generators
- Xray team (RPRX) — XHTTP transport and advanced obfuscation
- Hysteria2, NaiveProxy, Telemt and many community researchers

Full credits are available in the repository.

## License

**PolyForm Noncommercial License 1.0.0**

This project is distributed under the PolyForm Noncommercial License 1.0.0.  
Permitted use: **personal, non-commercial, educational, and scientific purposes only.**  
**Any commercial use is prohibited.**

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

The author assumes no responsibility for any damage resulting from the use of this software.  
See [LICENSE](LICENSE) for the full text.

## Support

- Bug reports & feature requests → [GitHub Issues](https://github.com/alexeylcp/angry-box/issues)
- General discussion → GitHub Discussions
- Real-world DPI test results (Russia, Iran, China) are extremely valuable.

---

**Current version:** 0.5.2 — Web UI, hybrid dev/prod mode, legal notices.