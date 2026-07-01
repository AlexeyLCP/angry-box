**Languages:** [English](README.md) | [Russian](README.ru.md) | [Chinese](README.zh.md) | [Farsi](README.fa.md)

# Angry-BOX

**کاملاً خودنوشته — SSH-only orchestrator / control plane**

Angry-BOX یک محصول کاملاً اورجینال است که از صفر نوشته شده. این پروژه fork از 3x-ui، LucX-UI، x-ui یا هیچ پنل دیگری نیست.

مدیریت فقط از طریق SSH انجام می‌شود. روی نودها فقط هستهٔ **sing-box-extended** (و در صورت نیاز xray) به همراه کانفیگ حداقلی اجرا می‌شود — بدون هیچ agent.

## Features

- Pure SSH management without persistent agents
- Powerful 2026 stealth presets (Russia / Iran / China / Maximum Stealth)
- Advanced AWG with CPS generators + realistic QUIC/SIP/DNS
- High-quality XHTTP (padding, XMUX, realistic headers) on both backends
- Stable user credentials (AWG keys + CPS generated once)
- Excellent router support (Keenetic .ipk + OpenWRT)
- Native Windows build
- Web UI + full CLI

## رفع اشکالات v0.7.2

**رفع اشکالات و پایدارسازی (پس از v0.7.1):**
- رفع از دست رفتن داده‌های Host هنگام ذخیره Standalone Inbounds.
- حل تعارض Standalone Inbounds و Chain Transport Inbounds از طریق رهگیری ApplyStandaloneNode.
- رفع دو باگ مربوط به `flow: "xtls-rprx-vision"`.
- تأیید Graceful Rollback روی ترافیک واقعی (کانفیگ نامعتبر → بازگشت خودکار به نسخه کاری قبلی).
- بهبود استخراج تگ‌های outbounds در سازنده merged config.

## ویژگی‌های جدید در نسخه v0.7.1

- **پیکربندی یکپارچه نود (Merged Config)** -- اکنون نودها می‌توانند به طور همزمان به عنوان ورودی‌های مستقل (standalone) عمل کرده و در چندین زنجیره پروکسی شرکت کنند. موتور جدید `apply-merged` یک فایل یکپارچه `config.json` می‌سازد که همه نقش‌ها را ترکیب می‌کند. دیگر مشکل بازنویسی و حذف تنظیمات یکدیگر وجود ندارد.
- **بررسی پیش‌نیاز SSH** -- پیش از تغییر تنظیمات سرورها، اتصال SSH به همه نودها بررسی می‌شود تا از خرابی در استقرار ناقص جلوگیری شود.
- **تشخیص تداخل پورت‌ها** -- سیستم یکپارچه‌ساز پیش از اعمال تغییرات، تداخل پورت‌ها بین زنجیره‌ها و ورودی‌های مستقل را تشخیص می‌دهد.
- **مشاهده تغییرات (Tag Diff)** -- پس از هر اعمال تغییرات، رابط کاربری تگ‌های اضافه شده (`+tag`) و حذف شده (`-tag`) را نمایش می‌دهد.
- **تولید خودکار کلیدهای SSH** -- رابط کاربری بخش نودها اکنون می‌تواند جفت‌کلید SSH را به صورت خودکار تولید و در سرور مقصد نصب کند.
- **پشتیبانی از چند زبان (i18n)** -- رابط کاربری وب در زبان‌های انگلیسی، روسی، چینی و فارسی در دسترس است.
- **تنظیمات مستقل نودها** -- امکان اعمال مستقل ورودی‌های یک نود، همراه با قابلیت بازگردانی امن (rollback) مانند زنجیره‌ها.

## Quick Start

```bash
# 1. Install
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# 2. Add host
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# 3. Create chain with strong 2026 preset
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

See details below.

## Architecture

Angry-BOX is only the **control plane**.

- The orchestrator does not forward traffic.
- All operations via SSH.
- Remote nodes run only a lightweight proxy (sing-box or xray) + minimal config.

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

Built on anti-censorship community research. Key sources:
- **[hoaxisr/awg-manager](https://github.com/hoaxisr/awg-manager)** (MIT) — live QUIC signature capture algorithm (the "take over an existing VPN" capture logic: connect to domain:443 over UDP, send a QUIC Initial, capture server response packets as I1-I5).
- **[pumbaX/awg-multi-script](https://github.com/pumbaX/awg-multi-script)** (MIT) — CPS (QUIC/SIP/DNS) generators, AmneziaWG obfuscation profiles + invariants.
- Xray team (RPRX), Hysteria2, NaiveProxy, Telemt.

**Third-Party Components:** sing-box / sing-box-extended (GPLv3), AmneziaWG kernel module (GPLv2), templ (MIT), golang.org/x/crypto/ssh (BSD-3), HTMX/TailwindCSS/DaisyUI (MIT/BSD).

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
