# Angry-BOX

**Языки:** [English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

Лёгкий оркестратор для управления цепочками прокси с мощной обфускацией на удалённых машинах **только через SSH**.

**sing-box** — основной бэкенд. **xray** — вторичный (best-effort).

## Принципы архитектуры

- Оркестратор — это только «голова». Он никогда не участвует в цепочке как прокси.
- Управление **только по SSH**. Постоянных агентов на нодах нет.
- На удалённые машины (VPS, Keenetic и другие роутеры) ставится **только** сам прокси (sing-box или xray) + минимальный конфиг + init-скрипт.
- Angry-BOX можно поставить на сам Keenetic. В этом случае он выступает только управляющей головой и **не становится** нодой цепочки.

### Два типа подключений

- **Транспортные** — для связывания хопов внутри цепочки (в 2026 рекомендуется XHTTP).
- **Пользовательские** — entry points для клиентов (TUIC v5, AmneziaWG с продвинутым CPS и т.д.).

## Профили обфускации 2026 (Security-First)

Глобальный профиль задаётся в конфиге или через `--profile`.

Доступные профили:
- `russia_2026`, `iran_2026`, `china_2026` — сбалансированные региональные
- `maximum_stealth_2026` — агрессивный
- `pro_2026` — полные диапазоны pumbaX Pro 2026 + полный CPS chain AWG (I1-I5, уровень 3, в основном QUIC)
- `xhttp_max_stealth_2026` — **экстремальный** профиль с акцентом на XHTTP (тяжёлый случайный padding, агрессивный XMUX, разделение upstream/downstream + pro AWG)

**Security > Compatibility** — явная политика для `pro_2026` и `xhttp_max_stealth_2026`. Они дают самую сильную известную на май 2026 защиту от DPI (РКН, GFW, Иран), ценой больших клиентских конфигов и возможных проблем на очень старых клиентах.

Ключи и CPS-пакеты AmneziaWG (I1-I5) для entry генерируются **один раз** при создании цепочки и не меняются при повторном apply.

### Продвинутая обфускация XHTTP

Мы реализовали множество техник 2025–2026 годов:
- Случайный padding заголовков в диапазонах
- Управление мультиплексированием в стиле XMUX
- Реалистичные заголовки браузера
- Подсказки для разделения upstream/downstream
- Выбор режима (packet-up / stream-up)

Поддерживается как в sing-box, так и в xray бэкенде.

## Установка

Рекомендуемый способ — официальный установочный скрипт:

```bash
# Последняя версия
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh

# Конкретная версия
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.2.0

# Локальный бинарник
sh scripts/install.sh --local ./angry-box
```

Скрипт автоматически определяет Linux (systemd) и Keenetic (Entware).

### Удаление и обновление

```bash
sh scripts/install.sh --uninstall
sh scripts/install.sh --version 0.3.0
```

## Быстрый старт

```bash
# 1. Добавить хосты
angry-box host add node1 --addr 203.0.113.10:22 --user root --key ~/.ssh/id_ed25519

# 2. Задеплоить sing-box
angry-box deploy --host node1

# 3. Создать цепочку с сильным профилем 2026
angry-box chain create mychain --nodes node1 --strategy urltest --profile pro_2026 --transport xhttp --user-protocol awg

# 4. Применить (получите богатый отчёт с ключами AWG + CPS)
angry-box apply-chain mychain

# 5. Проверить
angry-box chain show mychain
```

Генерация конфига без цепочки:

```bash
angry-box config -type user --protocol awg --profile xhttp_max_stealth_2026
```

## Возможности

- Чистое SSH-управление с откатом при ошибках
- Подробный ApplyReport (включая публичный ключ сервера AWG и стабильные CPS I1-I5)
- Стабильные entry-ключи AWG (не ротируются)
- Продвинутый XHTTP с параметрами из исследований сообщества
- Модульные пресеты 2026 + поддержка внешних JSON
- Полный паритет между apply-chain и standalone `config`

## Поддержка

- Сообщения об ошибках и предложения — через Issues на GitHub.
- Общие вопросы и помощь с настройками под цензуру — GitHub Discussions.
- Реальные отчёты из России, Ирана и Китая особенно ценны.

## Язык

[English](README.md) | [Русский](README.ru.md) | [中文](README.zh.md) | [فارسی](README.fa.md)

## Acknowledgments / Credits

Смотрите подробный раздел благодарностей в конце английской версии README.md. Проект сильно опирается на публичные исследования и инструменты сообщества борьбы с цензурой.

## Лицензия

PolyForm Noncommercial License 1.0.0