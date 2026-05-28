# Angry-BOX

Лёгкий оркестратор для управления sing-box (по умолчанию) и xray (опционально) на удалённых машинах через SSH.

## Архитектура (простая и честная)

- **Оркестратор** — это только "голова". Он не является нодой цепочки и не проксирует трафик сам по себе.
- Управление — **только через SSH**. Без постоянных агентов на нодах.
- На удалённые машины (VPS, Keenetic, другие роутеры) ставится **только** прокси (sing-box или xray) + минимальный конфиг + init-скрипт.
- Сам angry-box можно установить на Keenetic (для тех, у кого нет другого всегда-включённого устройства). В этом случае он выступает управляющей головой и может управлять другими серверами по SSH. Он **не участвует в цепочке как прокси** (пока что).

### Два типа подключений

- **Транспортные** — технические входящие для связывания хопов внутри цепочки.
- **Пользовательские** — entry points для конечных клиентов.

## Текущий статус

Проект перезапущен с чистого листа. Старая кодовая база LucX удалена.
Релизы v0.1.0 и v0.1.1 остаются доступны в GitHub Releases.

### Реализовано (backend)

- SSH-деплой sing-box (primary) и xray (secondary/best-effort) с systemd + checksum + rollback
- Полноценный интерфейс Backend (Deploy / ApplyConfig / GenerateConfig / GetStatus / Reload / Remove)
- Модульные обфускационные профили 2026 (russia_2026, iran_2026, china_2026, maximum_stealth_2026) — XHTTP + Reality + TUIC + AWG параметры
- Глобальный профиль из конфига + per-chain override + валидация
- Поддержка внешних пресетов (presets_file в конфиге — свои профили для лабораторий/кастомных стран)
- Transport между нодами: XHTTP (рекомендуется) или Reality+TCP
- User entry: TUIC v5 и AmneziaWG (полная генерация Curve25519 + amnezia параметры из профиля)
- apply-chain: генерация связанных конфигов (transport in/out + user entry на первой ноде), бэкапы, sing-box check, reload/restart с откатом, детальная диагностика по каждой ноде
- Удобная работа с AWG клиентскими ключами: --client-pubkey при генерации/apply, примеры клиентских конфигов в выводе `config -type user --protocol awg`
- Standalone генерация `angry-box config -type user/transport` полностью уравнена с апплаером (учитывает текущий профиль, поддерживает --profile, --client-pubkey, --protocol)
- Хранение хостов/цепочек в JSON, ResolveNodes, chain create с --transport/--user-protocol/--profile
- SSH клиент с таймаутами и хорошими ошибками
- Сборка под Linux (amd64/arm64) + Keenetic (mipsel) через Makefile + install-скрипты

### Backend готов

Backend (генерация конфигов, профили 2026, XHTTP/TUIC/AWG, apply-chain с диагностикой, AWG client keys, standalone config, деплой) завершён и готов к совместному тестированию.

UI (serve) и некоторые UX-мелочи можно дорабатывать по мере использования.

## Установка

### Linux (systemd)

```bash
# Из GitHub Releases (замените версию):
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.1.0

# Или из локального бинарника:
sh scripts/install.sh --local ./angry-box
```

После установки angry-box запущен как systemd-сервис на порту `:8090`.

### Keenetic (NDMS/Entware)

```bash
# Из GitHub Releases:
curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh -s -- --version 0.1.0

# Или из локального бинарника:
sh scripts/install.sh --local ./angry-box
```

Установщик автоматически определяет Keenetic и ставит бинарник в `/opt/bin/`, конфиги в `/opt/etc/angry-box/`, и S99-скрипт в `/opt/etc/init.d/`.

### Деинсталляция

```bash
sh scripts/install.sh --uninstall
```

### Обновление

```bash
# Просто запустите установщик с новой версией — он заменит бинарник и перезапустит сервис:
sh scripts/install.sh --version 0.2.0
```

## Быстрый старт

```bash
# 1. Зарегистрировать хосты
angry-box host add node1 --addr 10.0.0.1:22 --user root --key ~/.ssh/id_ed25519
angry-box host add node2 --addr 10.0.0.2:22 --user root --key ~/.ssh/id_ed25519
angry-box host add node3 --addr 10.0.0.3:22 --user root --key ~/.ssh/id_ed25519

# 2. Задеплоить sing-box на все хосты
angry-box deploy -addr 10.0.0.1 -user root -key ~/.ssh/id_ed25519
angry-box deploy -addr 10.0.0.2 -user root -key ~/.ssh/id_ed25519
angry-box deploy -addr 10.0.0.3 -user root -key ~/.ssh/id_ed25519

# 3. Создать и применить цепочку из трёх нод
angry-box chain create mychain --nodes node1,node2,node3 --strategy urltest
angry-box apply-chain mychain

# 4. Проверить статус
angry-box status -addr 10.0.0.1 -user root -key ~/.ssh/id_ed25519
angry-box chain show mychain
```

## Профили обфускации (2026)

Глобальный профиль задаётся в конфиге (`default_obfuscation_profile`) или через `--profile` при создании цепочки / генерации конфига.

Доступные: `russia_2026`, `iran_2026`, `china_2026`, `maximum_stealth_2026`, `pro_2026` (pumbaX Pro 2026 ranges + QUIC CPS I1-I5).

`pro_2026` и обновлённый `maximum_stealth_2026` используют лучшие практики 2026 (диапазоны Jc 4-16 / Jmin 50-256 / S 15-150 / H квадранты + генераторы QUIC Initial 1200B Chrome, SIP REGISTER, DNS+EDNS0 из pumbaX/awg-multi-script). Для стабильной работы AWG entry-ключи + CPS (I1-I5) генерируются **один раз** при создании цепочки и сохраняются — re-apply их не ротирует.

Профиль влияет на:
- XHTTP (методы/пути/заголовки/hosts) — для транспорта между нодами
- Reality SNI + fingerprint
- TUIC congestion_control + auth_timeout
- AWG (jc, jmin, jmax, h1-h4, s1/s2)

## AmneziaWG (AWG) — клиентские ключи

При использовании `--user-protocol awg`:

1. Сгенерируйте клиентскую пару на своей машине:
   ```bash
   wg genkey | tee client.priv | wg pubkey > client.pub
   ```

2. Передайте публичный ключ при apply/generation:
   ```bash
   angry-box apply-chain mychain   # если профиль уже содержит AWG
   # или для standalone:
   angry-box config -type user --protocol awg --client-pubkey "$(cat client.pub)" --profile russia_2026
   ```

3. apply-chain / config выведет подсказки с SERVER_PUBLIC_KEY (нужен клиентам в [Peer] PublicKey).

Серверная приватная часть остаётся только на entry-ноде (никогда не покидает её).
```

## Сборка из исходников

### Быстрая сборка

```bash
go build ./cmd/angry-box/
```

### Установка через make

```bash
# Сборка и установка бинарника
sudo make install

# Установка systemd-сервиса
sudo make install-systemd

# Всё вместе
sudo make install-all

# Деинсталляция
sudo make uninstall-systemd
sudo make uninstall
```

Переменные:
- `PREFIX` — путь установки (по умолчанию `/usr/local`)
- `DESTDIR` — для пакетирования (staging directory)
- `BINDIR`, `CONFDIR`, `DATADIR`, `SYSTEMD_DIR` — можно переопределить

```bash
make install PREFIX=/opt/angry-box
make install DESTDIR=/tmp/staging
```

## Архитектура проекта

```
cmd/angry-box/          — CLI (node, host, chain, apply-chain, serve)
internal/
  domain/model/         — Host, Chain, ChainNode, Strategy, Config...
  domain/ports/         — Backend interface, Factory interface
  backend/factory/      — Factory implementation
  backend/singbox/      — sing-box adapter (deploy, config, ssh ops)
  backend/xray/         — xray adapter
  ssh/                  — SSH client (key auth, Run)
  chain/                — Store (JSON), Applier (chain config gen + push)
scripts/
  install.sh            — Установщик для Linux и Keenetic
  angry-box.service     — systemd unit
  S99angry-box          — Keenetic init-скрипт
```

## Лицензия

PolyForm Noncommercial License 1.0.0
