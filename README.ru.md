**Языки:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

# Angry-BOX

**Лёгкий SSH-оркестратор** для **sing-box** (основной) и **xray** (вторичный).

Без агентов на нодах. Всё управление происходит по SSH. Разворачивайте минимальные прокси-конфиги на удалённых машинах и роутерах (включая Keenetic).

## Возможности

- Чистое SSH-управление без постоянных агентов на целях
- Мощные пресеты обфускации 2026 года (Россия / Иран / Китай / Maximum Stealth)
- Продвинутый AWG с генераторами CPS + реалистичные QUIC/SIP/DNS
- Высококачественный XHTTP (padding, XMUX, реалистичные заголовки) на обоих бэкендах
- Стабильные пользовательские креды (ключи AWG + CPS генерируются один раз)
- Отличная поддержка роутеров (Keenetic .ipk + OpenWRT)
- Нативная сборка под Windows
- Веб-интерфейс + полный CLI

## Быстрый старт

```bash
# 1. Установка
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# 2. Добавляем хост
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# 3. Создаём цепочку с сильным пресетом 2026
angry-box chain create mychain --nodes node1 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# 4. Разворачиваем
angry-box apply-chain mychain
```

Веб-интерфейс будет доступен по адресу `http://localhost:8090`.

## Установка

### Установочный скрипт (рекомендуется)

```bash
# Последняя версия
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# Конкретная версия
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.2.1
```

### Готовые сборки

Скачивайте со [страницы Releases](https://github.com/alexeylcp/angry-box/releases).

**Linux**
```bash
tar -xzf angry-box-0.2.1-linux-amd64.tar.gz
cd angry-box-0.2.1-linux-amd64
./angry-box --help
```

**Windows**
- Скачайте `angry-box-0.2.1-windows-amd64.zip` или `.exe`
- Распакуйте и запустите `angry-box.exe`
- Веб-интерфейс: `http://localhost:8090`

### Роутеры (Keenetic и OpenWRT)

Подробная инструкция ниже.

## Архитектура

Angry-BOX — это только **управляющая часть**.

- Сам оркестратор никогда не проксирует трафик.
- Всё управление идёт по SSH.
- На удалённых нодах ставится только лёгкий прокси (sing-box или xray) + минимальный конфиг.

**Два типа подключений:**
- **Transport** — технические хопы для связывания цепочки (рекомендуется XHTTP)
- **User** — реальные точки входа для клиентов (TUIC v5 или AmneziaWG)

## Пресеты 2026 года

Проект поставляется с современными пресетами, заточенными под актуальные системы DPI:

| Пресет                    | Направление            | Основные техники                     |
|---------------------------|------------------------|--------------------------------------|
| `russia_2026`             | Россия                 | Сбалансированный XHTTP + AWG         |
| `iran_2026`               | Иран                   | Агрессивный XHTTP + Reality          |
| `china_2026`              | Китай                  | Сильная обфускация + фрагментация    |
| `maximum_stealth_2026`    | Максимальная скрытность| Полный XHTTP + AWG CPS               |
| `pro_2026`                | Профессиональное использование | Принудительный CPS level 3 + QUIC 1200B |
| `xhttp_max_stealth_2026`  | Экстремальный XHTTP    | Максимальный padding + XMUX          |

## Поддержка роутеров

Angry-BOX выпускает нативные `.ipk` пакеты.

| Платформа         | Архитектура            | Пример пакета                              | Куда ставится |
|-------------------|------------------------|--------------------------------------------|---------------|
| Keenetic (Entware)| `aarch64-3.10`         | `angry-box_0.2.1_aarch64-3.10.ipk`         | `/opt/bin`    |
| Keenetic          | `mipsel_24kc`          | `angry-box_0.2.1_mipsel_24kc.ipk`          | `/opt/bin`    |
| OpenWRT           | `aarch64_cortex-a53`   | `angry-box_0.2.1_aarch64_cortex-a53.ipk`   | `/usr/bin`    |
| OpenWRT           | `mips_24kc`            | `angry-box_0.2.1_mips_24kc.ipk`            | `/usr/bin`    |

Все роутерные пакеты используют формат **outer-tar** и полностью статические бинари.

## Сборка из исходников

```bash
git clone https://github.com/alexeylcp/angry-box.git
cd angry-box

CGO_ENABLED=0 go build -o angry-box ./cmd/angry-box
make package-all
```

## Благодарности и источники

Angry-BOX построен на исследованиях антицензурного сообщества.

**Ключевые источники:**
- pumbaX / awg-multi-script — генераторы CPS, QUIC, SIP, DNS
- Xray (RPRX) — транспорт XHTTP и продвинутые методы обфускации
- Hysteria2, NaiveProxy, Telemt и многие исследователи из русскоязычного, иранского и китайского сообществ

## Лицензия

PolyForm Noncommercial License 1.0.0

## Поддержка

- Ошибки и предложения → [GitHub Issues](https://github.com/alexeylcp/angry-box/issues)
- Общие обсуждения → GitHub Discussions
- Реальные результаты работы против DPI (Россия, Иран, Китай) очень ценны.

---

**Текущая версия:** 0.2.1 — исправленная упаковка для роутеров, поддержка Windows и улучшенная документация.