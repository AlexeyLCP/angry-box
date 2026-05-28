**Languages:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

# Angry-BOX

Лёгкий оркестратор для управления **sing-box** (по умолчанию) и **xray** на удалённых машинах и роутерах **только через SSH**.

Никаких агентов на нодах. На удалённую сторону ставится только сам прокси + минимальный конфиг.

## Архитектура

- Оркестратор — только «голова». Сам трафик не проксирует.
- Всё управление — исключительно по SSH.
- На VPS/Keenetic/роутеры ставится **только** sing-box или xray + крошечный конфиг + init-скрипт.
- Сам angry-box можно поставить на Keenetic — он будет чисто управляющей головой.

### Два типа подключений

- **Транспортные** — для связывания хопов внутри цепочки (лучше всего XHTTP).
- **Пользовательские** — entry-point'ы для реальных клиентов (TUIC v5 или AmneziaWG).

## Пресеты 2026 года (главная фишка)

Взяли всё лучшее из публичных исследований на середину 2026 (pumbaX/awg-multi-script, Xray XHTTP #4113, Hysteria Gecko, NaiveProxy, Hiddify/3x-ui) и сделали модульные профили с жёсткой политикой **Security > Compatibility**.

В комплекте:
- `russia_2026`, `iran_2026`, `china_2026`, `maximum_stealth_2026`
- `pro_2026` — насильно включает CPS level 3 + QUIC Initial 1200 байт (отпечаток Chrome)
- `xhttp_max_stealth_2026` — экстремальный XHTTP + полный CPS3 + QUIC

Для AWG теперь генерируются полноценные I1–I5 (QUIC 1200B, реалистичные SIP REGISTER, DNS+EDNS0 и т.д.) при использовании pro/stealth-профилей.

XHTTP-транспорт получает продвинутые заголовки и padding на обоих бэкендах.

**Благодарности (Credits):**

- pumbaX / awg-multi-script — весь подход с CPS-генераторами и QUIC/SIP/DNS пакетами («бери все»)
- RPRX + сообщество Xray (XHTTP padding, XMUX, extra — PR #4113)
- Hysteria2 (Gecko-обфускация)
- NaiveProxy (реалистичные заголовки)
- Hiddify, 3x-ui, Telemt и всё русскоязычное/иранское/китайское коммьюнити исследований 2025-2026

## Установка

### Linux (systemd)

```bash
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.2.0
```

### Keenetic / OpenWRT через .ipk (новое в 0.2.0)

```bash
# Keenetic (mipsel)
opkg install angry-box_0.2.0_mipsel_24kc.ipk

# OpenWRT aarch64
opkg install angry-box_0.2.0_aarch64_cortex-a53.ipk
```

## Быстрый старт

```bash
angry-box host add node1 --addr 10.0.0.1:22 --user root --key ~/.ssh/id_ed25519
angry-box deploy ...
angry-box chain create mychain --nodes node1,node2 --profile pro_2026 --transport xhttp --user-protocol awg
angry-box apply-chain mychain
```

## Поддержка

GitHub Issues: https://github.com/alexeylcp/angry-box/issues

## Лицензия

MIT
