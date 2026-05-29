**Languages:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

# Angry-BOX

**ارکستر سبک SSH-only** برای **sing-box** (اصلی) و **xray** (ثانویه).

بدون نیاز به ایجنت روی نودها. همه مدیریت از طریق SSH انجام می‌شود. روی سرورهای راه‌دور و روترها (از جمله Keenetic) فقط پروکسی سبک نصب می‌شود.

## ویژگی‌های اصلی

- مدیریت کامل از طریق SSH بدون ایجنت پایدار
- پریست‌های قدرتمند ۲۰۲۶ (روسیه، ایران، چین، حداکثر پنهان‌کاری)
- AWG پیشرفته با تولیدکننده‌های CPS + QUIC/SIP/DNS واقعی
- XHTTP با کیفیت بالا (padding، XMUX، هدرهای واقعی) روی هر دو بک‌اند
- اعتبارنامه‌های پایدار کاربر (کلیدهای AWG و CPS فقط یک بار ساخته می‌شوند)
- پشتیبانی عالی از روترها (Keenetic .ipk + OpenWRT)
- نسخه بومی ویندوز
- رابط وب + CLI کامل

## شروع سریع

```bash
# ۱. نصب
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# ۲. اضافه کردن هاست
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# ۳. ساخت زنجیره با پریست قوی ۲۰۲۶
angry-box chain create mychain --nodes node1 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# ۴. اعمال
angry-box apply-chain mychain
```

رابط وب به صورت پیش‌فرض روی `http://localhost:8090` در دسترس است.

## نصب

### اسکریپت نصب یک‌خطی (توصیه‌شده)

```bash
# آخرین نسخه
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# نسخه خاص
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.5.2
```

### باینری‌های از پیش ساخته‌شده

از صفحه [Releases](https://github.com/alexeylcp/angry-box/releases) دانلود کنید.

**لینوکس**
```bash
tar -xzf angry-box-0.5.2-linux-amd64.tar.gz
cd angry-box-0.5.2-linux-amd64
./angry-box --help
```

**ویندوز**
- فایل `angry-box-0.5.2-windows-amd64.zip` یا `.exe` را دانلود کنید
- اجرا کنید: `angry-box.exe`
- رابط وب: `http://localhost:8090`

### روترها (Keenetic و OpenWRT)

جزئیات در بخش پایین.

## معماری

Angry-BOX فقط **صفحه کنترل** است.

- خود ارکستر ترافیک را فوروارد نمی‌کند.
- تمام عملیات از طریق SSH انجام می‌شود.
- روی نودهای راه‌دور فقط پروکسی سبک (sing-box یا xray) + کانفیگ کوچک نصب می‌شود.

**دو نوع اتصال:**
- **Transport**: هاب‌های داخلی زنجیره (XHTTP توصیه می‌شود)
- **User**: نقاط ورود واقعی کاربران (TUIC v5 یا AmneziaWG)

## پریست‌های پنهان‌کاری ۲۰۲۶

پروژه با پریست‌های حرفه‌ای بهینه‌شده برای DPIهای فعلی عرضه می‌شود.

| پریست                    | هدف                  | تکنیک‌های اصلی                    |
|--------------------------|----------------------|------------------------------------|
| `russia_2026`            | روسیه                | XHTTP متعادل + AWG                |
| `iran_2026`              | ایران                | XHTTP تهاجمی + Reality             |
| `china_2026`             | چین                  | پنهان‌کاری قوی + fragmentation     |
| `maximum_stealth_2026`   | حداکثر پنهان‌کاری    | XHTTP کامل + AWG CPS               |
| `pro_2026`               | استفاده حرفه‌ای      | CPS سطح ۳ اجباری + QUIC ۱۲۰۰ بایت |
| `xhttp_max_stealth_2026` | XHTTP افراطی         | حداکثر padding + XMUX             |

## پشتیبانی از روترها

Angry-BOX پکیج‌های بومی `.ipk` ارائه می‌دهد.

## ساخت از منبع

```bash
git clone https://github.com/alexeylcp/angry-box.git
cd angry-box
CGO_ENABLED=0 go build -o angry-box ./cmd/angry-box
make package-all
```

## قدردانی

این پروژه بر پایه تحقیقات عمومی جامعه ضدسانسور ساخته شده است.

منابع اصلی:
- pumbaX / awg-multi-script
- تیم Xray (RPRX)
- Hysteria2، NaiveProxy، Telemt و بسیاری از محققان روسی، ایرانی و چینی

## لایسنس

PolyForm Noncommercial License 1.0.0

## پشتیبانی

- گزارش باگ و درخواست ویژگی → [GitHub Issues](https://github.com/alexeylcp/angry-box/issues)
- بحث عمومی → GitHub Discussions

---

**نسخه فعلی:** 0.5.2 — بهبود بسته‌بندی روترها، پشتیبانی از ویندوز و مستندات.