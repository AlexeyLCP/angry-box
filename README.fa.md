# Angry-BOX

**زبان‌ها:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

ارکستر سبک برای مدیریت زنجیره‌های پروکسی با اختلال قوی، **فقط از طریق SSH** روی ماشین‌های راه دور.

**sing-box** بک‌اند اصلی است. **xray** به عنوان بک‌اند ثانویه (best-effort) پشتیبانی می‌شود.

## اصول معماری

- ارکستر فقط «سر» است و هرگز خودش در زنجیره ترافیک شرکت نمی‌کند.
- مدیریت **فقط از طریق SSH**. هیچ عامل دائمی روی نودها نصب نمی‌شود.
- روی ماشین‌های راه دور (VPS، روترهای Keenetic و غیره) فقط خود پروکسی (sing-box یا xray) + کانفیگ حداقلی + اسکریپت راه‌اندازی نصب می‌شود.
- می‌توانید Angry-BOX را روی خود Keenetic اجرا کنید. در این حالت فقط نقش سر مدیریت را دارد و **به عنوان نود پروکسی** در زنجیره شرکت نمی‌کند.

### دو نوع اتصال

- **اتصالات حمل‌ونقل (Transport)**: برای اتصال هاپ‌های داخل زنجیره (در ۲۰۲۶ XHTTP توصیه می‌شود).
- **اتصالات کاربری (User)**: نقاط ورود برای کلاینت‌های نهایی (TUIC v5، AmneziaWG با CPS پیشرفته و غیره).

## پروفایل‌های اختلال ۲۰۲۶ (اولویت امنیت)

پروفایل جهانی را می‌توان در کانفیگ یا با `--profile` تنظیم کرد.

پروفایل‌های فعلی:
- `russia_2026`، `iran_2026`، `china_2026` — متعادل منطقه‌ای
- `maximum_stealth_2026` — تهاجمی
- `pro_2026` — محدوده‌های کامل pumbaX Pro ۲۰۲۶ + زنجیره کامل CPS AWG (سطح ۳، عمدتاً QUIC)
- `xhttp_max_stealth_2026` — پروفایل **افراطی** متمرکز بر XHTTP (پدینگ سنگین تصادفی، XMUX تهاجمی، جداسازی upstream/downstream + AWG سطح pro)

**Security > Compatibility** سیاست صریح `pro_2026` و `xhttp_max_stealth_2026` است. این پروفایل‌ها قوی‌ترین مقاومت شناخته‌شده در سال ۲۰۲۶ در برابر DPI (RKN، GFW و سیستم‌های ایرانی) را ارائه می‌دهند، به قیمت کانفیگ‌های بزرگ‌تر کلاینت و احتمال مشکلات با کلاینت‌های بسیار قدیمی.

کلیدها و پکت‌های CPS آمنیزیا (I1-I5) برای نقطه ورود **یک بار** هنگام ایجاد زنجیره تولید می‌شوند و با apply مجدد چرخش نمی‌کنند.

### اختلال پیشرفته XHTTP

ما بسیاری از تکنیک‌های پیشرفته ۲۰۲۵–۲۰۲۶ را پیاده‌سازی کرده‌ایم:
- پدینگ هدر با محدوده تصادفی
- کنترل مالتی‌پلکسینگ به سبک XMUX
- هدرهای واقع‌گرایانه مرورگر (الهام‌گرفته از استک واقعی Chromium)
- راهنمایی جداسازی upstream/downstream
- انتخاب حالت (packet-up / stream-up)

هم در بک‌اند sing-box و هم xray پشتیبانی می‌شود.

## نصب

روش توصیه‌شده، اسکریپت نصب رسمی است:

```bash
# آخرین نسخه
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# نسخه خاص
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.2.0

# باینری محلی
sh scripts/install.sh --local ./angry-box
```

اسکریپت به صورت خودکار محیط Linux (systemd) و Keenetic (Entware) را تشخیص می‌دهد.

### حذف و به‌روزرسانی

```bash
sh scripts/install.sh --uninstall
sh scripts/install.sh --version 0.3.0
```

## شروع سریع

```bash
# ۱. افزودن هاست
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# ۲. دیپلوی sing-box
angry-box deploy --host node1

# ۳. ایجاد زنجیره با پروفایل قوی ۲۰۲۶
angry-box chain create mychain --nodes node1 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# ۴. اعمال (گزارش غنی شامل کلیدهای AWG + CPS دریافت کنید)
angry-box apply-chain mychain

# ۵. بررسی وضعیت
angry-box chain show mychain
```

تولید کانفیگ مستقل (بدون نیاز به زنجیره):

```bash
angry-box config -type user --protocol awg --profile xhttp_max_stealth_2026
```

## ویژگی‌ها

- مدیریت خالص SSH + بازگشت خودکار در صورت خطا
- گزارش ApplyReport دقیق (شامل کلید عمومی سرور AWG و CPS پایدار I1-I5)
- اعتبارنامه‌های ورود AWG پایدار (یک بار تولید می‌شوند)
- XHTTP پیشرفته با پارامترهای تحقیقاتی جامعه
- پروفایل‌های ماژولار ۲۰۲۶ + پشتیبانی از JSON خارجی
- برابری کامل بین apply-chain و دستور مستقل `config`

## پشتیبانی

- گزارش باگ و درخواست ویژگی از طریق GitHub Issues.
- بحث عمومی و کمک برای تنظیمات در شبکه‌های سانسور شده از طریق GitHub Discussions.
- بازخورد واقعی از روسیه، ایران و چین برای بهبود پروفایل‌ها بسیار ارزشمند است.

## زبان

[English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

## قدردانی / Credits

جزئیات قدردانی را در بخش پایانی نسخه انگلیسی README ببینید. این پروژه heavily بر پایه تحقیقات و ابزارهای عمومی جامعه ضدسانسور ساخته شده است.

## مجوز

PolyForm Noncommercial License 1.0.0