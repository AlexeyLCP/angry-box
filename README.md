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

### Реализовано

- SSH-деплой sing-box и xray на удалённые машины с systemd
- Генерация transport-конфигов (VLESS+Reality/TCP) и user-конфигов (VLESS/WS)
- ApplyConfig, Remove, GetStatus, Reload через SSH
- Управление цепочками нод (chain create/list/show/delete, apply-chain)
- apply-chain с автоматической генерацией конфигов для всех hop-ов
- JSON-хранилище хостов и цепочек
- CLI с подкомандами (deploy, status, config, apply, remove, reload, host, chain, apply-chain, serve)
- HTTP API в режиме демона (/health, /api/status)

### В разработке

- Web UI (HTMX)

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
