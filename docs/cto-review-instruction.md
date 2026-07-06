# CTO Review — `angry-box`

> Инструкция для будущей сессии, которая ничего не знает о проекте.
> Цель: запустить ревью этого репозитория через параллельных суб-агентов
> и синтезировать финальный отчёт. Только этот репозиторий.

---

## Шаг 0 — Что это за проект

**Путь:** `C:\Users\dante\OneDrive\projects\angry-box`

**Что внутри:** Go 1.26 monorepo — **приложение-оркестратор** Angry-box. Это НЕ
библиотека и НЕ单纯 daemon: это централизованная панель, которая генерирует
конфиги sing-box локально и пушит их на удалённые ноды по SSH. Содержит CLI
(`cmd/angry-box`), встроенный HTTP/HTMX UI (`internal/web`), слой оркестрации
цепочек (`internal/chain`), генераторы конфигов (`internal/backend/singbox`),
SSH-клиент с TOFU (`internal/ssh`), takeover существующих VPN (`internal/takeover`),
i18n (en/ru) и vendored пропатченный sing-box-extended + модуль AmneziaWG.

**Один Go-модуль** `github.com/alexeylcp/angry-box`. Крейтов нет — всё в
`internal/` + `cmd/`. `web/templates/*.templ` — исходники UI (компилируются в
`*_templ.go` через `templ generate`). `examples/`/`fuzz/` как отдельные
cargo-проекты — НЕТ (это не Rust).

### Технологический стек
- Go 1.26 (`go.mod`, edition неактуально — это не Rust)
- **sing-box-extended** `1.13.14-extended-2.5.0-patched` (НЕ официальный sing-box) —
  vendored бинарник в `deps/sing-box-1.13.14-extended-2.5.0-patched-linux-amd64.tar.gz`,
  патчи в `patches/` (wireguard-go chacha20poly1305 overlap fix + fallback round-robin).
  Деплоится на ноды через `angry-box deploy` (VPS не компилирует Go, только скачивает).
- **AmneziaWG kernel module** — собран из `deps/amneziawg-src.tar.gz`, ставится на
  user-facing AWG серверы (chain entry / standalone / exit) через `awg-quick@awg0` +
  sing-box TUN-overlay (`include_interface:["awg0"]`). НЕ userspace endpoint
  (тот нестабилен под amnezia).
- UI: **Go Templ** (`web/templates/*.templ`) + **HTMX** (`hx-*`) + **TailwindCSS/DaisyUI**.
  Все user-facing строки через `i18n.T(ctx, "key")` (handlers: `i18n.T(r.Context(), ...)`)
  + JS-side `window.AB_I18N` + `abt("key")`.
- SSH: `golang.org/x/crypto/ssh`, TOFU через `CheckHostKey` (Trust On First Use),
  fingerprint change → `HostKeyWarning` modal, НЕ обходить.
- Deps: `BurntSushi/toml` (config), `a-h/templ v0.3.1020` (UI), `skip2/go-qrcode` (QR),
  `golang.org/x/crypto v0.52.0` (SSH + крипто). Косвенно: `google/go-cmp` (тесты).
- Сборка: `make build` (`templ generate` → `go build` с ldflags version/commit/date).
  Текущая версия `v0.2.0`.
- Конфиг оркестратора: `angry-box.toml` (TOML, `internal/config`), store: `chains.json`
  или `store.json` (JSON, `internal/chain/store.go` с `sync.Mutex`).

### Структура пакетов (`internal/`)
| Пакет | Роль | Точка входа |
|---|---|---|
| `cmd/angry-box` | CLI: `serve` (HTTP API + UI), `deploy/status/config/apply/remove/reload`, `host add/list/delete`, `chain create/list/show/delete`, `apply-chain`, `version` | `cmd/angry-box/main.go` |
| `internal/chain` | Ядро оркестрации: `store.go` (JSON persistence, single source of truth), `applier.go` (render + SSH deploy + rollback), `merged_config.go` (merged node config), `presets.go`/`protocolpresets.go`/`routingpresets.go`, `awgpresets_gen.go`/`awg_cps.go`/`awgcapture.go`/`awgimport.go`/`awg_deploy.go`/`awg_push.go`/`awg_server.go`/`awg_tun_overlay.go`, `cryptogen.go` (Reality/WG/Trojan/SS/Hysteria2/TUIC/MTProxy keygen), `clientconfig.go`, `mtproxy.go`, `audit.go`/`deploystatus.go`/`autoapply.go`, `frozen.go` (TUIC/Hysteria2 FROZEN enforcement), `hostlock.go` (per-host mutex), `takeover_helpers.go`, `xhttp_cps.go` | `internal/chain/store.go` |
| `internal/backend/singbox` | Генерация конфига sing-box: `config.go` (base + standalone), `roles.go` (`RenderProxyNode`/`RenderAWGBalancer`/`RenderAWGHop`), `singbox.go`. XHTTP headers как `map[string]string`, NO amnezia/ECH/curve_preferences на REALITY inbound | `internal/backend/singbox/config.go` |
| `internal/backend/xray` | Xray backend (dual-core) — **ПУСТО**, placeholder | — |
| `internal/backend/factory` | `factory.go` — выбор бэкенда по типу | `internal/backend/factory/factory.go` |
| `internal/singbox/config` | `types.go` — типы конфига sing-box (напр. `RouteRuleEntry`, НЕ `RoutingRule`) | `internal/singbox/config/types.go` |
| `internal/takeover` | VPN takeover: detect (AWG/sing-box/Xray/MTProxy) → convert → cutover, rollback-to-old-VPN. `takeover.go`/`detect.go`/`convert.go`/`awg_takeover.go` | `internal/takeover/takeover.go` |
| `internal/domain/model` | Core data: `Chain`/`ChainNode`/`AWGExitLink`, `NodeInfo`/`NodeInbound`/`TakeoverState`, `User`/`MtproxyUser`/`PanelSettings`/`SSHKeyEntry`/`NodeMetrics`, `Profile`/`ClientAssignment`/`RouteRule`/`ConnectionLink`, `AuditLog`, `Host`/`KnownHost`/`Config`/`DeployResult`/`Status`/`ConfigType` | `internal/domain/model/*.go` |
| `internal/domain/ports` | Интерфейсы: `Backend`, `SSHClient` | `internal/domain/ports/{backend,ssh}.go` |
| `internal/ssh` | SSH-клиент (conn, file push, service control) — используется chain-пакетом | `internal/ssh/client.go` |
| `internal/config` | Загрузка `angry-box.toml`, `DefaultConfigPath()` | `internal/config/config.go` |
| `internal/i18n` | `i18n.go` — `locales` map, `T(ctx, key)`/`Lang(ctx)`. en (262 ключа) + ru (482 ключа). Ключ = английская строка | `internal/i18n/i18n.go` |
| `internal/web` | HTTP/HTMX handlers + routes (всё в `server.go` ~80 маршрутов): `chains.go`/`nodes.go`/`users.go`/`settings.go`/`spider.go`/`profiles.go`/`takeover.go`/`dashboard.go`/`auth.go`/`authlimiter.go`/`csrf.go`/`port.go`/`qr.go`/`htmlx.go`/`misc.go` | `internal/web/server.go` |
| `internal/wslsmoke` | WSL smoke tests (build tag `wsl_smoke`) | — |

**Что НЕ входит / сознательно вне scope** (NON-goals, см. AGENTS.md + spec):
- Не маршрутизирует трафик сам — только генерит конфиги и пушит по SSH
- TUIC и Hysteria2 **FROZEN** (см. Known Issues #6/#11): существующие store-записи
  можно редактировать, новые chain/inbound с этими протоколами отклоняются
  (`internal/chain/frozen.go`). НЕ предлагать «доделать TUIC/Hysteria2» — это
  сознательная пауза до стабилизации AWG/Reality+XHTTP/MTProxy.

### Документация
| Файл | Что внутри |
|---|---|
| `AGENTS.md` | **Закон для агентов** — workflow (READ→AUDIT→PLAN→CODE→TEMPL→BUILD→TEST→DOCS), 10 правил, структура проекта, debug-паттерны, E2E инфра, sing-box-extended заметки, Known Issues #1–14 (TUIC/Hysteria2 FROZEN, AWG kernel rework, per-client routing, frozen enforcement и т.д.), commit convention |
| `docs/PROGRESS.md` | Knowledge & Progress (486 строк, RU) — архитектурные решения, kernel-AWG rework, e2e инфра (GCloud VPSes), commit conventions, attribution MIT/GPL |
| `TESTING.md`, `TEST_REPORT.md` | Тест-план + отчёт |
| `README.md` / `.ru` / `.fa` / `.zh` | README на 4 языках |
| `PR_DESCRIPTION_v0.2.0.md` | Описание релиза v0.2.0 |
| `config.example.toml`, `angry-box.toml` | Пример + runtime конфиг оркестратора |
| `store.json`/`chains.json`/`fresh_store.json`/`e2e_full_store.json`/`multi_store.json`/`test_ui_store.json` | Sample stores |

### Внешние ссылки (canon — read-only / не ревьюить код)
- `deps/sing-box-*.tar.gz` + `deps/sing-box.exe` + `deps/amneziawg-src.tar.gz` —
  vendored бинарники/исходники. **Не ревьюить**, только убедиться что
  патчи в `patches/` на месте и деплой скачивает правильный tarball.
- `patches/wireguard-go-awg-overlap.patch` + `patches/fallback-round-robin.patch` —
  патчи к sing-box-extended. Проверить, что они документированы и применяются
  при сборке sing-box (см. `scripts/build-singbox.sh`).

### Текущий статус (важно для baseline)
- Версия `v0.2.0`, ветка `main`, рабочее дерево чистое (на старте сессии).
- Recent commits: refactor/feat по unified Clients UI (MTProxy section, single
  Clients nav), удаление старых Mtproxy store CRUD, redirect /ui/users → /ui/clients.
- Продуктивный фокус v0.2.x: **AWG** (kernel + balancer), **VLESS+Reality+XHTTP**
  (inter-node transport + standalone), **MTProxy/Telemt**. TUIC/Hysteria2 — PAUSED.
- `go build ./...` — должен быть зелёный.
- `go test ./...` — unit-тесты (без build tag).
- E2E: `go test -tags e2e ./internal/chain/ -run TestE2E -v -timeout 300s`
  (нужны GCloud VPSes, см. ниже). WSL smoke: `-tags wsl_smoke`.
- **CI workflows НЕТ** (`.github/workflows/` отсутствует) — это сам по себе finding.

**Перед запуском ревью пересчитай baseline:**
```
go build ./...
go test ./... 2>&1 | tail -20
go vet ./...
```
Зафиксируй точное число passed/failed/ignored перед стартом.

### E2E инфраструктура (из AGENTS.md)
- GCloud project: `project-d4c6c72c-4f10-4288-902`
- Test VPSes:
  - `vps-de-test-1` — 34.40.120.7 (Debian 12, key: `google_compute_engine`)
  - `vps-de-test-2` — 35.198.166.183 (Ubuntu 24.04, key: `id_ed25519`)
  - `vps-de-test-3` — 35.198.100.1 (Ubuntu 24.04, key: `id_ed25519`, свежий)
- Auth: `gcloud auth login lucipoher@gmail.com`
- **Hardware baseline для оценок**: AMD Ryzen 7 7800X3D / 31 GB RAM / NVMe
  (не путать с VPSes — те слабее).

---

## Фаза 1 — Один агент-исследователь

**Запусти одного агента.** Без оценок — только карта.

```
Ты исследуешь Go monorepo angry-box. Полная карта без оценок.

Путь: C:\Users\dante\OneDrive\projects\angry-box
Документация: AGENTS.md, docs/PROGRESS.md, TESTING.md, README*.md

Используй Glob, Grep, Read, Bash для исследования. Перед стартом
обязательно прочитай:
- AGENTS.md полностью (workflow, 10 правил, структура, Known Issues #1-14)
- docs/PROGRESS.md (архитектурные решения, kernel-AWG rework, e2e инфра)

Составь карту:

1. ТЕХНОЛОГИЧЕСКИЙ СТЕК
   - go.mod (go 1.26, deps + версии), Makefile targets, .golangci.yml
   - deps/ (sing-box-extended tarball, sing-box.exe, amneziawg-src) + patches/

2. СТРУКТУРА ПАКЕТОВ (internal/ + cmd/)
   - Для каждого пакета: назначение, точка входа, основные типы/хендлеры
   - cmd/angry-box/main.go — список CLI-подкоманд
   - internal/chain (самый большой пакет — перечисли все .go файлы с ролью)
   - internal/backend/singbox (config.go, roles.go, singbox.go)
   - internal/web (server.go + handler-группы + auth/csrf)
   - internal/domain/model (Chain, NodeInfo, User, PanelSettings, Profile, AuditLog)

3. ТОЧКИ ВХОДА (это ПРИЛОЖЕНИЕ, не библиотека)
   - CLI: cmd/angry-box/main.go — какие подкоманды, какие флаги
   - HTTP serve: internal/web/server.go — какие маршруты, auth middleware
   - Потребитель (админ) работает через браузерный HTMX UI + CLI

4. МЕЖПАКЕТНЫЕ ЗАВИСИМОСТИ
   - Граф импортов между internal/* (chain → backend/singbox → ssh → domain/model)
   - internal/chain/store.go — single source of truth, sync.Mutex
   - Какие типы пересекают границы (Chain, NodeInfo, User, NodeInbound, Config)

5. ПЛАНЫ И СПЕЦИФИКАЦИИ
   - AGENTS.md — 10 правил, Known Issues, frozen enforcement
   - docs/PROGRESS.md — kernel-AWG rework, e2e верификации
   - Перечисли ключевые архитектурные решения (orchestrator pattern, node-first,
     chains as overlay, declarative state, SSH TOFU, persistent transit keys,
     graceful rollback, port conflict prevention)

6. ТЕСТЫ
   - Список *_test.go (unit, без build tag) по пакетам
   - E2E: e2e_test.go / e2e_heavy_test.go / e2e_helpers_test.go (build tag `e2e`)
   - WSL smoke: internal/wslsmoke/* (build tag `wsl_smoke`)
   - go test ./... результат (текущий счёт)
   - Бенчей нет (это Go, не criterion) — но есть test-artifacts/ с логами прогонов

7. ПУБЛИЧНЫЙ API ПОТРЕБИТЕЛЯ (админа)
   - CLI-подкоманды (deploy, status, config, apply, chain create/list/show/delete)
   - HTTP маршруты internal/web/server.go (~80 маршрутов)
   - web/templates/*.templ — UI-компоненты (base, dashboard, chains, nodes,
     users, settings, spider, hosts, index)

Возвращай ТОЛЬКО факты, без оценок. Максимум 3000 слов.
```

**Дождись результата перед Фазой 2.**

---

## Фаза 2 — 9 агентов параллельно

В каждый промпт вставь карту из Фазы 1 + укажи путь
`C:\Users\dante\OneDrive\projects\angry-box`. Все 9 запусти
**одним вызовом** (single message с 9 tool uses).

---

### Агент 1 — Application Production Readiness

```
Production readiness аудит angry-box КАК ПРИЛОЖЕНИЯ-ОРКЕСТРАТОРА.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box

Используй Grep, Bash, Read. Критерии для приложения (НЕ библиотеки):
есть CLI, HTTP-сервер, install-скрипты, systemd, packaging. Критичны:
panic-free handlers, отсутствие fmt.Println в production path, чистая
ошибка-семантика, docs, конфиг-валидация.

1. PANIC-FREE RUNTIME
   - Поиск: panic!, log.Fatal в handler-путях, unrecovered goroutines
   - rg -n 'panic\(|log\.Fatal|log\.Fatalf' internal/ cmd/ --type go
     | grep -v '_test.go'
   - Для каждого: severity (CRITICAL если в HTTP handler/SSH deploy path;
     LOW если в init с явной валидацией CLI-флагов)
   - Особое внимание: internal/chain/applier.go (SSH deploy + rollback),
     internal/web/* (handlers), cmd/angry-box/main.go (CLI dispatch)

2. ОШИБОЧНАЯ СЕМАНТИКА
   - Каждый пакет имеет свой error type? Или используются sentinel errors?
   - Все handler-ы возвращают пользователю понятные сообщения (через
     templates.ApplyResult или HTMX alerts), а не голый 500?
   - errors.Is/errors.As используется корректно?
   - Нет ли проглатывания ошибок через `_ =` без обоснования (AGENTS.md #6)
   - rg -n '_ = ' internal/ --type go | grep -v '_test.go'

3. ЛОГИРОВАНИЕ
   - Используется log (стандартный) или custom logger?
   - Уровни: info для deploy lifecycle, warn для recoverable, error для abort
   - fmt.Println/fprintf в production path (НЕ в _test.go) — недопустимо
   - НЕ логируется ли секретный материал? (SSH private keys, PSK,
     Reality PrivateKey, ShortID, UUID, MTProxy Secret, AWG private keys,
     admin password hash)
   - rg -n 'log\.|fmt\.Print' internal/ cmd/ --type go | grep -v '_test.go'
   - rg -n 'PrivateKey|Secret|Password|PSK|ShortID|UUID' internal/chain/cryptogen.go
     internal/ssh/client.go internal/web/auth.go

4. ДОКУМЕНТАЦИЯ
   - godoc на экспортируемых функциях/типах?
   - go doc -all ./... — есть ли пустые doc-строки на pub items?
   - Package-level doc-комментарии на каждом package?
   - AGENTS.md / docs/PROGRESS.md актуальны (соответствуют коду)?

5. SEMVER-READINESS
   - Версия v0.2.0 — что в PR_DESCRIPTION_v0.2.0.md vs реальный код?
   - breaking changes между коммитами? (git log --oneline)
   - Public CLI-флаги стабилизованы? (cmd/angry-box/main.go usage string)
   - store.json schema миграции (store_migration_test.go)?

6. CI/CD
   - .github/workflows/ — ОТСУТСТВУЕТ (это finding сам по себе)
   - .golangci.yml — какие linters включены (gofmt/govet/staticcheck/unused/
     ineffassign/typecheck; errcheck ОТКЛЮЧЕН — TODO v0.3.0)
   - Makefile: make build/test/vet/test-coverage — что делает каждый?
   - Должен ли быть минимальный CI (build + vet + test + templ generate check)?
   - Cache для deps/sing-box tarball? (он 25 MB, скачивается каждый раз?)

7. INSTALL / PACKAGING / SYSTEMD
   - scripts/install.sh — что делает, безопасно ли (rm -rf, перезапись конфигов)?
   - scripts/angry-box.service — systemd unit корректен (User=, Restart=,
     After=network.target)?
   - scripts/S99angry-box — init.d/Entware start script
   - packaging/keenetic/ — opkg metadata (control, postinst)
   - scripts/build-opkg.sh / build-keenetic-opkg.sh — cross-build targets
   - config.example.toml vs angry-box.toml — какой canonical?

8. CONFIG VALIDATION
   - internal/config/config.go — TOML парсинг, defaults, validation
   - config.example.toml — все поля документированы?
   - Что если config отсутствует/битый? Graceful или panic?
   - AB_ROUTE_DNS=1 env gate (см. Known Issues #2/#7) — документирован?

Вывод: таблица "пакет → % app-ready → блокеры".
Итоговый % application-readiness. Максимум 1500 слов.
```

---

### Агент 2 — Внутренняя связность модулей

```
Аудит внутренней связности кода angry-box.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box

Используй Grep, Bash, Read.

1. МЁРТВЫЙ КОД
   - Экспортируемые функции/методы без вызовов в репозитории
   - internal/backend/xray — ПУСТОЙ пакет (placeholder). Реально нужен?
     Если dual-core заявлен но не реализован — это zombie-код или осознанный stub?
   - internal/node — ПУСТОЙ (если есть)
   - web/handlers/ — ПУСТОЙ каталог
   - frontend/ — только README (placeholder)
   - //go:build wsl_smoke — используется ли?
   - rg -n '// nolint:deadcode|// nolint:unused' internal/

2. НЕЗАВЕРШЁННЫЕ РЕАЛИЗАЦИИ
   - TODO/FIXME/XXX в коде: rg -n 'TODO|FIXME|XXX' internal/ cmd/ web/ --type go
   - Заглушки: функции возвращают nil/zero без реализации
   - internal/backend/xray — пустой, но заявлен в factory?
   - Пустые handler-функции (http.HandlerFunc возвращающие 200 без тела)
   - AGENTS.md "legacy follow-up" пункты (напр. CLI Backend.ApplyConfig
     standalone-AWG path всё ещё userspace — Known Issues #10/#11)

3. НЕСВЯЗАННЫЕ МОДУЛИ
   - Файлы объявленные но не подключённые
   - build tags без использования (wsl_smoke, e2e)
   - Лишние deps в go.mod (google/go-cmp только в тестах — indirect, ОК)
   - internal/wslsmoke — реально запускается? test-artifacts/ есть логи?

4. КОМПИЛИРУЕМОСТЬ
   - go build ./...
   - go vet ./...
   - go build -tags e2e ./... (e2e тесты компилируются?)
   - go build -tags wsl_smoke ./...
   - templ generate требуется? (web/templates/*_templ.go синхронны с .templ?)
   - Зафиксируй ВСЕ warnings (go vet, golangci-lint run если есть)

5. ТЕСТОВОЕ ПОКРЫТИЕ
   - Каждый пакет имеет тесты? (cmd/, internal/chain, internal/web, internal/ssh,
     internal/config, internal/takeover, internal/backend/singbox, internal/i18n?)
   - go test ./... результат (зафиксируй счёт)
   - internal/i18n — есть ли тест на наличие всех ключей в обоих локалях?
     (en 262 ключа, ru 482 — асимметрия, это finding?)
   - Какие public APIs не покрыты unit-тестами?

6. ОТНОШЕНИЕ К AGENTS.md / PROGRESS.md
   - Known Issues #1-14 — какие отмечены как "Fixed", какие "Open"?
   - docs/PROGRESS.md §11 (routing bugs fixed 2026-07-04) — реально в коде?
   - "legacy follow-up" пункты — определены чётко?

Вывод: список проблем с файл:строка и severity. Максимум 1200 слов.
```

---

### Агент 3 — Связность между пакетами и с canon (deps/sing-box-patched + patches)

```
Аудит связности между internal/* пакетами + потребления vendored sing-box-extended
и AmneziaWG.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box
Canon (read-only): deps/sing-box-*.tar.gz, deps/amneziawg-src.tar.gz, patches/

Используй Grep, Read, Bash.

1. РАЗДЕЛЕНИЕ СЛОЁВ (AGENTS.md #4)
   - internal/backend/singbox (config gen) НЕ должен содержать UI-логику
   - internal/chain/applier.go (deploy) НЕ должен содержать config-gen логику
   - internal/chain/merged_config.go — отдельная ответственность
   - internal/takeover/ — отдельная от chain
   - UI-логика (handlers) только в internal/web/
   - rg 'templ\.|http\.' internal/chain/ internal/backend/ --type go
     (если есть — нарушение слоёв)

2. КОНТРАКТЫ НА ГРАНИЦАХ
   - internal/domain/ports/Backend, SSHClient — реализуются кем?
   - internal/ssh/client.go реализует ports.SSHClient?
   - internal/backend/factory/factory.go → ports.Backend — корректно?
   - internal/chain → internal/backend/singbox: какие типы пересекают?
     (Config, ConfigParams, DeployResult, Status)
   - internal/web → internal/chain/store: только чтение/запись через Store API,
     не прямое обращение к store.json файлу?
   - Error-конверсия между пакетами без утечки внутренностей?

3. TYPE MISMATCHES
   - model.Chain vs model.NodeInfo — кто чем владеет (Chain генерит transport
     inbounds под капотом, Node — standalone). Конфликты (Known Issues #11:
     server-IP collision 10.8.0.1/32 если chain AWG entry + standalone AWG inbound)?
   - User.AWGAddress vs ChainNode.TransitAWGAddress (10.8.0.0/24 vs 10.9.0.0/24) —
     разделение соблюдено?
   - model.UserProtocol vs model.TransportType — enum-ы consistent между
     chain.go, frozen.go, web handlers?
   - ConfigType int enum (ConfigTransport/ConfigUser/ConfigStandaloneNode) —
     используется единообразно?

4. SING-BOX CONFIG TYPES
   - internal/singbox/config/types.go — canonical типы
   - internal/backend/singbox/config.go / roles.go используют types.go?
   - RouteRuleEntry (НЕ RoutingRule — Debug Pattern #3)
   - XHTTP headers как map[string]string (AGENTS.md #4)
   - NO amnezia/ECH/curve_preferences на REALITY inbound
   - WireGuardEndpoint vs WireGuardOutbound (Known Issues #9: 1.13 не имеет
     wireguard outbound, клиентская сторона = WireGuardEndpoint+peers[])
   - Расхождения между types.go и реальной схемой sing-box-extended 1.13.14?

5. ПОТРЕБЛЕНИЕ VENDORED SING-BOX
   - deps/sing-box-1.13.14-extended-2.5.0-patched-linux-amd64.tar.gz —
     какой путь установки на VPS? (scripts/install.sh)
   - deps/checksums.txt — проверяется ли checksum при деплое?
   - patches/wireguard-go-awg-overlap.patch + patches/fallback-round-robin.patch —
     применяются при сборке sing-box (scripts/build-singbox.sh)?
   - Версия в tarball (1.13.14-extended-2.5.0-patched) vs заявленная в AGENTS.md —
     совпадает?
   - deps/sing-box.exe (Windows) — для чего? Тесты на Windows?

6. AMNEZIAWG KERNEL MODULE
   - deps/amneziawg-src.tar.gz — собирается на VPS или скачивается готовый?
   - InstallAWGModuleWithOptions (AGENTS.md #8) — gating на
     UserProtocol == AWG || Transport == AWG
   - curve25519_x86_64, libcurve25519_generic, udp_tunnel, ip6_udp_tunnel —
     зависимости документированы?
   - patches/wireguard-go-awg-overlap.patch — про userspace (для transit,
     Known Issues #10), НЕ для kernel user-facing servers

7. STORE КОНСИСТЕНТНОСТЬ
   - internal/chain/store.go — sync.Mutex, JSON file
   - ResolveNodes не держит lock но вызывает GetNodeInfo который держит (AGENTS.md #2)
   - readStore (unlocked helper) используется внутри locked методов?
   - store_migration_test.go — миграции корректны?
   - Несколько store-файлов (chains.json, store.json, fresh_store.json,
     e2e_full_store.json, multi_store.json, test_ui_store.json) — какой canonical?

8. ПРОТОКОЛЬНАЯ СОГЛАСОВАННОСТЬ
   - cryptogen.go: Reality/WG/Trojan/SS/Hysteria2/TUIC/MTProxy keygen —
     client-side gen vs server-side parse consistent?
   - derive_auth_key / HKDF info/salt — совпадают между gen и использованием?
   - RenderClientAWGConf vs RenderServerAWGConf/RenderExitAWGConf —
     amnezia fields в [Interface] BEFORE [Peer] (Known Issues #10)?
   - buildChainRoleInOut (merged_config.go) ветвится на c.Transport:
     AWG/Reality/XHTTP — все три работают, Hysteria2 refuses loudly (Known Issues #8)?

Вывод: карта "пакет A → пакет B: совместимо/расхождение".
Особое внимание consumptions deps/sing-box + patches. Максимум 1200 слов.
```

---

### Агент 4 — Чистая архитектура и качество кода

```
Архитектурный аудит angry-box.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box

Используй Grep, Read.

1. НАРУШЕНИЯ СЛОЁВ (AGENTS.md #4)
   - I/O (SSH, HTTP) в pure-logic функциях chain/applier?
   - UI-логика в boilerplate (handlers содержат бизнес-логику вместо делегирования
     store)?
   - God objects: applier.go 72KB, merged_config.go 39KB, store.go 30KB,
     nodes.templ 33KB — оправдано или избыточно?
   - internal/chain/applier.go — один файл на 72KB, должен ли быть разбит?
   - internal/web/server.go — ~80 маршрутов в одном RegisterRoutes — split оправдан?

2. ДУБЛИРОВАНИЕ
   - client-side config render (clientconfig.go) vs server-side (roles.go) —
     общие части framing?
   - RenderProxyNode / RenderAWGBalancer / RenderAWGHop — общий код?
   - awg_deploy.go / awg_push.go / awg_server.go / awg_tun_overlay.go — границы
     чёткие или overlap?
   - mtproxy.go vs cryptogen.go MTProxy keygen — дублирование?
   - internal/ssh/client.go vs internal/chain/takeover_helpers.go — SSH-логика
     разнесена?

3. ZOMBIE-КОД
   - Закомментированные блоки: rg -n '//.*func |/\*' internal/ | head
   - TODO без owner/issue
   - Duplicate constants (RTP/header sizes? — нет, это не Rust; но размеры
     MTU 1280/1420, subnets 10.8.0.0/24 / 10.9.0.0/24 / 10.10.0.0/24 —
     в нескольких местах?)
   - "legacy follow-up" / "SUPERSEDED" блоки в AGENTS.md — есть ли мёртвый
     код оставшийся после rework?

4. СЛОЖНОСТЬ
   - Топ-10 функций по длине (>150 строк Go)
   - Вложенность >4 уровней
   - >6 параметров — Go не ругает, но стоит ли refaktorить в config-struct?
   - applier.go buildNodeConfig / ApplyChain — цикломатическая сложность?

5. GO-СПЕЦИФИЧНЫЕ АНТИПАТТЕРНЫ
   - Излишний string вместо typed enum (model.* имеет typed string enums —
     соблюдено везде?)
   - copy в hot path (per-deploy SSH push — не per-packet, менее критично)
   - sync.Mutex vs sync.RWMutex — store.go использует Mutex, оправдано?
     (AGENTS.md #2 — deadlock warnings)
   - interface{} / any без type-assertion safety (ConfigParams.Extra map[string]any)
   - error wrapping: %w vs %v — консистентно?
   - context.Context передаётся во все handler/store calls?

6. SSH / TOFU / UNSAFE
   - internal/ssh/client.go — CheckHostKey реализация (AGENTS.md #3)
   - HostKeyWarning modal — реально не обходит TOFU?
   - unsafe блоки (если есть — Go редко): rg -n 'unsafe ' internal/ cmd/
   - SSH key handling — приватные ключи не логируются, не утекают в error messages?
   - hostlock.go — per-host mutex, корректно предотвращает concurrent deploys
     на одну ноду?

7. TEMPLATE / HTMX КАЧЕСТВО
   - web/templates/*.templ — Templ best practices (templ.KV для conditional classes,
     компоненты разбиты, inline SVG Heroicons)
   - hx-target / hx-swap — консистентно?
   - Модалы через DaisyUI + HTMX #modal-container?
   - i18n.T на ВСЕХ user-facing строках (AGENTS.md #1)?

Вывод: список нарушений с файл:строка. Топ-5 самых критичных.
Максимум 1200 слов.
```

---

### Агент 5 — Протоколы, sing-box schema и сетевые стандарты

```
Аудит соответствия протокольным стандартам и sing-box-extended schema.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box

Сначала прочитай AGENTS.md "sing-box-extended" секцию + Known Issues #8/#9/#10/#11.

Проверь:

1. VLESS + REALITY (sing-box schema)
   - internal/backend/singbox/roles.go RenderProxyNode — Reality inbound корректен?
   - private_key / short_id / server_name (SNI) — поля правильные?
   - NO amnezia/ECH/curve_preferences на REALITY inbound (AGENTS.md #4)
   - XHTTP transport: headers как map[string]string (НЕ map[string][]string)
   - Reality client side (transit): buildChainRoleInOut генерит правильный outbound?

2. AWG (AmneziaWG) — kernel mode
   - RenderServerAWGConf / RenderExitAWGConf / RenderExitServerAWGConf —
     awg-quick .conf формат корректен?
   - amnezia fields (Jc/S1/S2/H1-H4/Jc/Junk... — см. Known Issues #10) в
     [Interface] BEFORE [Peer]?
   - Table = off в exit .conf (Known Issues #12 — без него lockout)?
   - PostUp MASQUERADE для 10.8.0.0/24 + 10.10.0.0/24 (Known Issues #13)?
   - PostUp/PostDown sysctl rp_filter=0 (Known Issues #13)?
   - MTU 1420 (не 1280 — Known Issues #10 fix)?
   - CPS I1-I5 / S3/S4 / ITime (I1Packet unused — Known Issues #10)?
   - H1-H4 quadrant ranges width >= 1000?

3. XHTTP (inter-node transport)
   - XHTTP headers map[string]string (AGENTS.md #4)
   - XHTTP CPS (xhttp_cps.go) — корректная обфускация?
   - max obfuscation (sing-box-extended supports)?

4. MTProxy / FakeTLS
   - internal/chain/mtproxy.go — FakeTLS корректен?
   - cryptogen.go MTProxy secret generation (dd-secret format)?
   - mtproxy user (model.MtproxyUser) — SecretHex, FakeTLSDomain, OrderIndex?
   - MTProxyNodes auto-apply (recent commits про unified Clients UI)?

5. TUIC / HYSTERIA2 — FROZEN (AGENTS.md #6/#11)
   - internal/chain/frozen.go — FrozenTransports / FrozenUserProtocols /
     FrozenStandaloneProtocols + Validate* guards
   - Везде ли wired: chain create/edit (web/chains.go), spider link (web/spider.go),
     standalone inbound add (web/nodes.go), default protocol (web/settings.go)?
   - UI dropdowns: frozen options как <option ... selected disabled>?
   - Edit-guard: handleUpdateChain валидирует только когда значение ИЗМЕНЯЕТСЯ
     (!= c.Transport) — TestHandler_UpdateChain_PreservedFrozenProtocol /
     TestHandler_UpdateChain_RejectsSwitchToFrozen покрывают?
   - Существующие store-записи с TUIC/Hysteria2 — остаются для display/edit?

6. SSH TOFU (AGENTS.md #3)
   - internal/ssh/client.go CheckHostKey — верификация host key
   - При смене ключа — deploy fails, user approves via HostKeyWarning modal
   - model.KnownHost — Addr/Fingerprint/FirstSeen/Trusted
   - НЕ обходить (нет skip-HostKey флагов в production path)

7. КРИПТОГРАФИЯ (cryptogen.go)
   - X25519 Reality keypair — корректная генерация?
   - HKDF (info = "wirev3"? — нет, это не Rust; но Reality HKDF параметры
     consistent между gen и использованием)?
   - WireGuard / AmneziaWG keypair — x25519-dalek эквивалент (Go crypto)?
   - Trojan / Shadowsocks password gen — random source (crypto/rand)?
   - MTProxy secret (dd / base64)?
   - UUID v4 gen (VLESSUUID, TUICUUID)?

8. ANTI-REPLAY / PORT CONFLICTS
   - AGENTS.md #8: chain inbounds read-only, ports 443/8443 не переопределяются
     standalone inbounds
   - Проверка port conflicts перед save/apply — где, корректна?
   - port.go helper (internal/web)

9. ROUTING / DNS (gated AB_ROUTE_DNS=1)
   - Known Issues #2/#7: DNS/Route disabled в merged config (sing-box 1.13
     detour bugs) — minimal config works
   - buildMergedRoute / buildMergedDNS — удалены (CTO-review M10) или
     есть? Если есть — соответствуют live sing-box?
   - Per-client routing: AWG source_ip_cidr (primary, Known Issues #7) vs
     auth_user (legacy secondary)

10. РАНДОМИЗАЦИЯ
   - crypto/rand используется (НЕ math/rand) для всех крипто-материалов?
   - UUID v4, Reality ShortID, MTProxy secret, AWG keypair — OsRng эквивалент?

Вывод: таблица "стандарт/протокол → файл:строка → соответствует/нарушает".
Максимум 1200 слов.
```

---

### Агент 6 — Баги, уязвимости, утечки

```
Security и bug аудит angry-box.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box

1. КРИПТОГРАФИЯ
   - Constant-time compare для PSK / secret / password hash?
     (subtle.ConstantTimeCompare, НЕ просто == на байтах/strings)
   - crypto/rand для всех nonce/key/UUID/secret gen?
   - Key material zeroize: в Go нет Zeroizing, но есть ли явная очистка
     приватных ключей из памяти? (менее критично чем Rust, но для SSH privkeys — да)
   - Логирование: PrivateKey/Secret/Password/PSK/ShortID/UUID НЕ попадают в log?
     rg -n 'log\.|fmt\.Print' internal/chain/cryptogen.go internal/ssh/client.go
     rg -n 'PrivateKey|Secret|Password' internal/web/auth.go

2. SSH (AGENTS.md #3)
   - internal/ssh/client.go — TOFU CheckHostKey реализация
   - При смене host key — НЕ自动 accept, требует UI approval
   - Приватные SSH-ключи (model.SSHKeyEntry.KeyData) — хранятся в store.json
     В ОТКРЫТОМ ВИДЕ? (это finding — ключи в JSON без шифрования?)
   - hostlock.go — per-host mutex предотвращает race в concurrent deploy
   - SSH command injection: r.RemoteAddr / user input в SSH commands?
     rg -n 'exec\.Command|cmd\.' internal/ssh/ internal/chain/

3. AUTH / CSRF (internal/web)
   - auth.go — admin password auth, PanelSettings.AdminPasswordHash
   - Какой hash алгоритм? (bcrypt/argon2/scrypt — НЕ plain SHA256 без salt)
   - authlimiter.go — rate limiting на login (brute-force protection)
   - csrf.go — CSRF protection на всех state-changing POST handlers?
     (HTMX hx-post — CSRF token передаётся?)
   - Session/cookie: Secure flag, HttpOnly, SameSite?
   - Default password — есть ли? Forced change on first login?

4. CERT-FORGE / TLS (если есть)
   - rcgen-эквивалент в Go? (нет, это Rust; но есть ли cert generation в cryptogen?)
   - TUIC TLS cert (Known Issues #1: buildTUICTLSOptions, base64 heredoc) —
     но TUIC FROZEN, не трогать
   - Reality server cert — cover-cert, НЕ валидируется (AGENTS.md #5 spec)

5. INJECTION
   - TOML config parsing (internal/config): deny_unknown_fields?
   - store.json parsing: schema validation? (произвольный JSON → типы)
   - URI parser (clientconfig.go link gen): bounds, percent-encoded?
   - Path traversal: user input в путях на VPS (config path, backup path)?
   - HTMX hx-* attributes — не инъектируются из user input?
   - model.User.Name / Telegram / Email — санитизация в templates? (Templ
     auto-escapes HTML, но проверь)

6. MEMORY / RESOURCE SAFETY
   - Неограниченный рост: map/slice без eviction?
   - internal/chain/store.go — store растёт без bound (chains/nodes/users)?
   - audit.go — AuditLog растёт без bound? Есть ли rotation/retention?
   - hostlock.go — map locks растёт без cleanup?
   - Goroutine leaks: go func() без context cancellation?
     rg -n 'go func\(' internal/ cmd/ --type go | grep -v '_test.go'

7. CONCURRENCY
   - internal/chain/store.go sync.Mutex — deadlock risks (AGENTS.md #2:
     SaveChain → GetHost nested lock)
   - ResolveNodes не держит lock но вызывает GetNodeInfo (держит) — scope
   - internal/web — handler concurrency, shared state?
   - hostlock.go — global map of mutexes, своя race?

8. INTEGER SAFETY
   - as-касты: int → uint16/uint8 без bounds check (port, MTU, payload len)
   - Wire parsing: Content-Length, RTP-interleaved length (если есть — это не
     Rust, но sing-box config parse может быть)
   - u32 → int конверсия (на 32-bit платформах — keenetic mipsel!)

9. DEPENDENCY CVE AUDIT
   - govulncheck ./... (если установлен)
   - golang.org/x/crypto v0.52.0 — CVE?
   - BurntSushi/toml v1.6.0 — CVE?
   - a-h/templ v0.3.1020 — CVE?
   - skip2/go-qrcode — CVE?

10. ROLLBACK INTEGRITY (AGENTS.md #7)
   - ApplyChain / ApplyStandaloneNode: SSH connect → push config → restart
     sing-box → verify → rollback on failure
   - Цепочка rollback НЕ нарушена? (backup config, restore, restart, verify)
   - deploystatus.go — deploy-hash tracking, корректен?
   - Takeover rollback-to-old-VPN (internal/takeover) — откатывается cleanly?

11. SPECIFIC TO THIS PROJECT
   - SSH TOFU bypass: есть ли debug flag / env var для skip host key check
     в production path? (НЕ должно быть)
   - AWG kernel module install — rmmod/insmod безопасно? Lockout risk
     (Known Issues #12 — full-tunnel AWG client lockout, Table=off)
   - Takeover cutover — atomic? Если fails mid-cutover, old VPN restored?
   - MTProxy secret в клиентских ссылках — leak через referer/logs?

Вывод: CRITICAL/HIGH/MEDIUM/LOW с файл:строка.
CVE секция отдельно: пакет → CVE → severity → fix available.
Максимум 1800 слов.
```

---

### Агент 7 — Deploy pipeline и runtime data path + ресурсный бюджет

```
Анализ полного пути деплоя и runtime data path в angry-box.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box

Самостоятельно изучи путь UI → нода:
1. Admin открывает UI (internal/web handlers) → HTMX-запрос
2. Handler читает/пишет Store (internal/chain/store.go, sync.Mutex)
3. ApplyChain (internal/chain/applier.go): render config (backend/singbox)
4. SSH connect (internal/ssh/client.go) → push config на VPS
5. Restart sing-box (systemctl / service control)
6. Verify service running (Status)
7. On failure → rollback to previous config (AGENTS.md #7)

И takeover путь:
1. internal/takeover/detect.go — detect existing AWG/sing-box/Xray/MTProxy
2. convert.go — convert config to angry-box model
3. cutover — push new config, restart, verify
4. rollback-to-old-VPN on failure

Используй Read и Grep.

1. КОПИРОВАНИЕ ДАННЫХ
   - Config render: []byte → SSH write — сколько copies? Bytes vs []byte?
   - SSH push: scp / sftp / exec cat heredoc — какой метод, эффективный?
   - store.json read/write — atomicwrite.go (atomic file write)?
   - Large configs (multi-node chain) — streamed или buffered полностью?

2. БУФЕРИЗАЦИЯ И BACKPRESSURE
   - Concurrent deploys на разные ноды — hostlock.go per-host mutex
   - Concurrent deploys на одну ноду — serialize (hostlock)?
   - Store lock contention: UI writes + autoapply.go + applier reads
   - autoapply.go — background loop, как часто, contention с UI?

3. ТАЙМАУТЫ
   - SSH connect timeout — где настраивается?
   - SSH command exec timeout (restart sing-box, install module)?
   - HTTP handler timeout — ServeMux без timeout? (http.Server ReadTimeout/
     WriteTimeout настроены в cmd/angry-box/main.go serve?)
   - sing-box restart verify timeout (health check loop)?
   - Reality/TLS handshake — не тут (это на VPS, не в orchestrator)

4. RESILIENCE PATTERNS
   - Retry: где? SSH connect retry? Config push retry?
   - Rollback: AGENTS.md #7 — реализован полностью? Тесты покрывают rollback?
   - Drain/graceful shutdown: cmd/angry-box/shutdown.go — in-flight deploys?
   - autoapply — retry on transient SSH failure?

5. ПРОИЗВОДИТЕЛЬНОСТЬ
   - go func() в hot path (per-deploy)?
   - Blocking calls: SSH exec — синхронный, но в goroutine per request?
   - JSON marshal/unmarshal store на каждой операции? (store.go reloads?)
   - templ generate overhead — compile-time, ОК

6. РЕСУРСНЫЙ БЮДЖЕТ (оркестратор, НЕ data plane)
   Цель: оркестрировать десятки нод, не перегружая панель.

   CPU:
   - busy-wait в autoapply loop?
   - JSON marshal/unmarshal на каждом store read/write — кэшируется?
   - Config render per-deploy — O(chain size), приемлемо?

   RAM:
   - Store in-memory: chains.json весь загружен? Размер на 100 chains × 10 nodes?
   - AuditLog — растёт без bound?
   - Per-request alloc в handlers?

   Сеть:
   - SSH connection pool (internal/chain/pool? — нет, это не Rust wirev3-io)
   - Reuse SSH connection для multi-command? Или connect-per-command?
   - TCP_NODELAY на SSH?

7. SPECIFIC TO PROJECT
   - Сколько параллельных нод оркестратор может деплоить одновременно?
     (hostlock per-host, но что если 50 нод сразу?)
   - autoapply на 100 нод — CPU/RAM?
   - Store file growth (chains.json) — 1 MB? 10 MB? Маршалится целиком?
   - HTTP server: один ServeMux, нет reverse proxy в production — ОК для small panel?

Вывод: описание deploy data path, bottlenecks.
Таблица: CPU% / RAM MB / SSH conn / deploy latency на 1 ноду и на 50 нод.
Максимум 1500 слов.
```

---

### Агент 8 — Архитектурный аудит и соответствие AGENTS.md / docs/PROGRESS.md

```
Высокоуровневый архитектурный аудит angry-box.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box

Прочитай:
- AGENTS.md (workflow, 10 правил, Known Issues #1-14, commit convention)
- docs/PROGRESS.md (§1.A kernel-AWG rework, §8 dns.idoctor.mom comparison,
  §11 routing bugs fixed 2026-07-04)
- README.md (product positioning)

Сравни с реальным кодом.

1. СООТВЕТСТВИЕ AGENTS.md (10 правил)
   - #1 HTMX+Templ only, i18n.T на всех строках — соблюдено?
   - #2 Store sync.Mutex, no nested locks — соблюдено (Debug Pattern #4)?
   - #3 SSH TOFU, no bypass — соблюдено?
   - #4 Config gen separation (config.go/roles.go/applier.go/merged_config.go/takeover) —
     слои не смешаны?
   - #5 Persistent transit keys (generateHopParams reuses, SaveChain after ApplyChain) —
     соблюдено?
   - #6 No silent failures (no _ = without docs) — соблюдено?
   - #7 Graceful rollback chain intact — соблюдено?
   - #8 Port conflict prevention (chain inbounds read-only, 443/8443) — соблюдено?
   - #9 Test before reporting (go build, templ generate THEN build) — Makefile
     покрывает?
   - #10 Documentation updates — task artifacts актуальны?

2. СООТВЕТСТВИЕ Known Issues
   - #6 TUIC FROZEN — frozen.go + UI guards на месте?
   - #7 Per-client routing TWO mechanisms (AWG source_ip_cidr primary, auth_user legacy) —
     код отражает?
   - #8 TransportAWG IMPLEMENTED, TransportHysteria2 FROZEN — buildChainRoleInOut
     ветвится корректно, Hysteria2 refuses loudly?
   - #9 sing-box 1.13 NO wireguard outbound — WireGuardEndpoint+peers[] используется
     (НЕ WireGuardOutbound в chain path)?
   - #10 AWG amnezia 4 fixes — CPS I1-I5 persisted on chain, client .conf uses
     chain preset, amnezia в [Interface] BEFORE [Peer], no TUN inbound for userspace?
     (SUPERSEDED для user-facing servers kernel rework — код отражает?)
   - #11 Hysteria2 FROZEN centralized in frozen.go, edit-guard nuance (handleUpdateChain
     validates only on change) — TestHandler_UpdateChain_PreservedFrozenProtocol /
     RejectsSwitchToFrozen покрывают?
   - #12 AWG multi-node = BALANCER (kernel awg-exit-nX + bind_interface, NOT linear chain
     userspace WG). RenderExitAWGConf Table=off. NEVER full-tunnel AWG client on
     SSH'd VPS without Table=off — код/доки отражают?
   - #13 Traffic status live VPS re-confirmed 2026-07-04 — MASQUERADE both subnets,
     tunIncludeInterfaces awg-exit-nX, rp_filter=0 PostUp — в коде?
   - #14 AWG CPS live capture = QUIC only (CaptureQUICSignature), NOT plain TCP TLS —
     awgcapture.go отражает?

3. РАЗДЕЛЕНИЕ ОТВЕТСТВЕННОСТИ
   - cmd/angry-box (CLI) — thin dispatch, без бизнес-логики?
   - internal/chain (orchestration) — ядро, оправданного размера? (applier 72KB)
   - internal/backend/singbox (config gen) — чистый, без I/O?
   - internal/web (handlers) — тонкий, делегирует store/chain?
   - internal/takeover — отдельная ответственность, не дублирует chain?
   - internal/backend/xray — пустой, нужен ли? (dual-core support заявлен)
   - Можно ли что-то слить без потери?

4. МАСШТАБИРУЕМОСТЬ
   - При каком кол-ве нод/цепочек архитектура сломается? (store.json целиком в RAM)
   - hostlock.go — global mutex map, contention при 100+ нод?
   - autoapply.go — polling loop, масштабируется?
   - HTTP server — один ServeMux, нет reverse proxy — ОК для target scale?

5. СЛОЖНОСТЬ
   - applier.go 72KB — оправдан или разбить? (buildNodeConfig / ApplyChain / rollback)
   - merged_config.go 39KB — buildChainRoleInOut + buildAWGTransportInbound/Outbound
   - store.go 30KB — JSON persistence + migration
   - 8+ AWG файлов (awg_deploy/push/server/tun_overlay/cps/capture/import) —
     split оправдан или избыточный?

6. ТЕХНИЧЕСКИЙ ДОЛГ
   - AGENTS.md "legacy follow-up" — CLI Backend.ApplyConfig standalone-AWG path
     ещё userspace (RenderAWGHop) — не конвертирован в pushConfigWithAWG?
   - Takeover'd AWG has no per-client routing (peers not materialized as users)
   - Server-IP collision 10.8.0.1/32 (chain entry + standalone AWG inbound)
   - I1Packet parsed but unused
   - DNS/Route disabled (sing-box 1.13 detour bugs) — re-implement when fixed
   - Multi-node chains need Route/DNS re-enabled
   - Какие из этих debt-пунктов реально open vs closed?

7. РИСКИ
   - deps/sing-box-patched vendored — что если GitHub deps репо удалено? Backup?
   - patches/ применяются к какой версии sing-box-extended? При обновлении upstream —
     ребейз документирован?
   - Chrome fingerprint drift — НЕ применимо (это не Rust wirev3), но Reality
     SNI/fingerprint drift при обновлении sing-box — план?
   - amneziawg-src.tar.gz версия — при обновлении kernel module API?
   - Keenetic mipsel cross-build — поддерживается, но тестируется?

8. PROTOCOL VERSIONING
   - store.json schema versioning (store_migration_test.go)?
   - Backward compat: старые store.json загружаются новым binary?
   - Chain/NodeInbound schema evolution — migration path?

Вывод: список архитектурных решений с оценкой
(Правильно / Сомнительно / Нужно пересмотреть) и обоснованием.
Топ-5 архитектурных рисков. Максимум 1800 слов.
```

---

### Агент 9 — TDD compliance и качество тестов

```
Проверка соблюдения TDD в angry-box.

[ВСТАВЬ КАРТУ ИЗ ФАЗЫ 1]

Путь: C:\Users\dante\OneDrive\projects\angry-box

Используй Bash (git log, go test), Grep, Read.

1. ПОКРЫТИЕ ПУБЛИЧНОГО API
   Для каждого пакета (cmd/angry-box, internal/chain, internal/backend/singbox,
   internal/web, internal/ssh, internal/config, internal/takeover, internal/i18n):
   - Список экспортируемых функций/типов (grep '^func [A-Z]\|^type [A-Z]')
   - Тест-файлы (in-source _test.go + crates/*/tests — нет, Go in-package)
   - Таблица: pub item → покрыт / частично / не покрыт
   Особое внимание:
   - internal/chain/applier.go (ApplyChain, ApplyStandaloneNode, rollback) — покрыт?
   - internal/chain/store.go (CRUD, migration, lock safety) — покрыт?
   - internal/chain/merged_config.go (buildChainRoleInOut all 3 transports) — покрыт?
   - internal/chain/frozen.go (Validate* guards) — TestHandler_UpdateChain_* покрывают?
   - internal/web handlers (chains, nodes, users, spider, settings) — handler tests?
   - internal/ssh/client.go (CheckHostKey TOFU) — покрыт?
   - internal/takeover (detect/convert/cutover/rollback) — покрыт?
   - internal/i18n (key completeness en/ru) — покрыт? (асимметрия 262 vs 482)

2. КАЧЕСТВО ТЕСТОВ
   - Тесты-пустышки (вызов без assert)?
   - assert только на nil/non-nil err без проверки content?
   - Тесты на реализацию (внутренности) vs поведение (контракт)?
   - t.Skip тесты — обоснованы? (TUIC FROZEN — Known Issues #6, должны skip)
   - Table-driven tests (Go idiomatic) — используются?
   - testify? (нет в go.mod — используется google/go-cmp + стандартная testing)

3. ГРАНИЧНЫЕ СЛУЧАИ
   - store.go: пустой store, битый JSON, миграция старой версии
   - frozen.go: switch TO frozen (reject 400), preserve frozen (selected disabled)
   - applier.go: SSH failure mid-deploy, rollback success, rollback failure
   - merged_config.go: все 3 transport (AWG/Reality/XHTTP), Hysteria2 refuses loudly
   - cryptogen.go: key gen determinism (random source), format correctness
   - port conflict: chain inbound 443 + standalone inbound 443 — отклоняется?
   - takeover: detect each VPN type, convert, cutover, rollback-to-old

4. E2E / WSL SMOKE ПОКРЫТИЕ
   - internal/chain/e2e_test.go, e2e_heavy_test.go, e2e_helpers_test.go (tag `e2e`)
   - TestE2E_Heavy_Protocol_AWG_Kernel, TestE2E_Heavy_Protocol_AWG_Kernel_2Hop —
     PASS на live VPS (Known Issues #13)?
   - TestE2E_Heavy_PerClientRouting — skip stub (AWG kernel module not staged)?
   - internal/wslsmoke/* (tag `wsl_smoke`) — запускаются?
   - test-artifacts/2026-07-02/ — логи прошлых прогонов, что покрыто?
   - test_e2e.ps1 — PowerShell runner, что запускает?

5. БЕНЧМАРКИ
   - Go testing.B benchmarks — есть? (rg -n 'func Benchmark' internal/)
   - Если нет — нужны ли? (store marshal/unmarshal, config render)

6. GIT ИСТОРИЯ
   - git log --oneline | head -50: conventional commits (feat/fix/test/docs/refactor)?
   - Co-Authored-By: Claude Opus 4.8 в commits (AGENTS.md convention)?
   - Commits соответствуют task tracking?

7. ИНТЕГРАЦИОННЫЕ
   - internal/web/handlers_*_test.go (18 файлов) — handler integration
   - internal/chain/*_test.go (44 файла) — unit + integration
   - internal/backend/singbox/*_test.go (8 файлов) — config gen
   - internal/takeover/*_test.go (7 файлов) — takeover
   - fakessh_test.go, backend_ssh_test.go — SSH mocking

8. ТЕСТОВАЯ ИНФРАСТРУКТУРА
   - test fixtures / helpers shared (servertest_test.go, fakessh_test.go)?
   - Mock SSH (fakessh) vs real VPS (e2e tag)?
   - Детерминированность: random в тестах с явным seed?
   - test-artifacts/ — fixtures или просто логи?
   - coverage.out (make test-coverage) — реальное покрытие chain/singbox/config?

Вывод:
- Таблица: пакет → % покрытия pub API → качество (A/B/C/D)
- Топ-15 непокрытых публичных функций
- Топ-5 плохих тестов с примерами
- Общая оценка: TDD соблюдён / частично / не соблюдён
Максимум 1500 слов.
```

---

## Фаза 3 — Синтез финального отчёта

Собери результаты 9 агентов в финальный отчёт:

```markdown
# `angry-box` CTO Review — [дата]

## Executive Summary
[5-7 предложений: общее состояние оркестратора, главные риски,
готовность к v0.2.0 публикации / v0.3.0]

## Application Production Readiness
| Пакет | % Ready | Критические блокеры |
|---|---|---|
| cmd/angry-box | ... | ... |
| internal/chain | ... | ... |
| internal/backend/singbox | ... | ... |
| internal/web | ... | ... |
| internal/ssh | ... | ... |
| internal/takeover | ... | ... |
| internal/config | ... | ... |
| internal/i18n | ... | ... |
| ИТОГО (app) | ... | |

## Top-10 блокеров перед v0.3.0
[Приоритизированный список]

## Критические находки по безопасности
[Только CRITICAL и HIGH из Агента 6 + CVE]

## Архитектурные риски
[Топ-5 из Агентов 4 и 8]

## Deploy pipeline и ресурсный бюджет
[Ключевые выводы из Агента 7]

### Ресурсный бюджет (цель: оркестрация десятков нод)
| Метрика | Измеренное | Бюджет | Статус |
|---|---|---|---|
| RAM при 100 chains × 10 nodes | ? MB | <200 MB | ✓/✗ |
| CPU idle (autoapply polling) | ? % | <5% | ✓/✗ |
| Deploy latency (1 нода) | ? s | <30 s | ✓/✗ |
| Concurrent deploys (50 нод) | ? | serialized per-host | ✓/✗ |
| Store.json marshal per op | ? ms | минимизировать | ✓/✗ |
| HTTP handler p99 | ? ms | <100 ms | ✓/✗ |

## Соответствие протоколам и sing-box-extended schema
[Из Агента 5: Reality/AWG/XHTTP/MTProxy + Агента 3: vendored sing-box + patches]

## FROZEN enforcement (TUIC / Hysteria2)
[Из Агента 5 + Агента 8: frozen.go wired везде, edit-guard nuance, тесты покрывают]

## TDD compliance
| Пакет | % покрытия API | Качество | Статус |
|---|---|---|---|
| cmd/angry-box | ... | A/B/C/D | ✓/~/✗ |
| internal/chain | ... | A/B/C/D | ✓/~/✗ |
| internal/backend/singbox | ... | A/B/C/D | ✓/~/✗ |
| internal/web | ... | A/B/C/D | ✓/~/✗ |
| internal/ssh | ... | A/B/C/D | ✓/~/✗ |
| internal/takeover | ... | A/B/C/D | ✓/~/✗ |

[Топ-10 непокрытых функций + вывод: TDD соблюдён / частично / не соблюдён]

## Соответствие AGENTS.md и docs/PROGRESS.md
[Из Агента 8: где код отклонился от доков, оправдано или нет;
какие Known Issues реально closed vs open]

## Технический долг (legacy follow-ups)
[Из AGENTS.md Known Issues + Агента 2: CLI standalone-AWG userspace path,
takeover'd AWG no per-client routing, server-IP collision, I1Packet unused,
DNS/Route disabled, multi-node Route/DNS re-enable]

## CI/CD статус
[Отсутствие .github/workflows — finding; .golangci.yml config; Makefile targets;
рекомендация минимального CI: build + vet + test + templ generate check + govulncheck]

## Рекомендуемый порядок действий
1. Немедленно (blockers v0.3.0): ...
2. До v0.3.0: ...
3. До v1.0 (production-grade): ...
4. Operational debt: [sing-box-extended pin updates, amneziawg-src version pin,
   patch rebases, CI setup, SSH key encryption at rest, audit log retention]
```

---

## Чеклист перед запуском ревью

- [ ] `git status` — рабочее дерево чистое (или известные WIP)
- [ ] `go build ./...` — зелёный
- [ ] `go test ./...` — все unit-тесты проходят (зафиксируй точное число
      passed/failed/ignored перед запуском)
- [ ] `go vet ./...` — чисто
- [ ] `make generate` (templ generate) — web/templates/*_templ.go синхронны с .templ
- [ ] `go build -tags e2e ./...` — e2e тесты компилируются (НЕ запускать без VPS)
- [ ] `go build -tags wsl_smoke ./...` — wsl_smoke компилируется
- [ ] `golangci-lint run` (если установлен) — зафиксируй warnings
- [ ] `govulncheck ./...` (если установлен) — зафиксируй CVE baseline
- [ ] AGENTS.md + docs/PROGRESS.md отражают актуальные статусы (Known Issues #1-14)
- [ ] Прочитать AGENTS.md полностью перед запуском ревью — это даст контекст
      что спроектировано и почему, чтобы агенты не путали NON-goals (TUIC/Hysteria2
      FROZEN, no CLI/TUN/SOCKS5 — не library) с пропущенной функциональностью

## Особенности именно этого проекта

- **Это приложение-оркестратор**: НЕ библиотека, НЕ单纯 daemon. Критерии
  production-readiness: CLI, HTTP/HTMX UI, install scripts, systemd, packaging,
  SSH deploy pipeline, rollback integrity. НЕ panic-free public API библиотеки.
- **sing-box-extended vendored критичен**: deps/sing-box-patched + patches/.
  Любой совет «убрать vendored бинарник» = поломка AWG/Reality/XHTTP/CPS.
- **TUIC и Hysteria2 FROZEN** (AGENTS.md #6/#11): НЕ предлагать доделать.
  Существующие store-записи можно редактировать, новые — reject (frozen.go).
- **AWG = primary protocol** (AGENTS.md #7/#10/#11/#12/#13): kernel awg-quick@awg0
  + sing-box TUN-overlay для user-facing серверов. Userspace endpoints только
  для inter-node AWG transit (linear multihop). НЕ предлагать откат на userspace
  для user-facing (он нестабилен под amnezia).
- **Per-client routing TWO mechanisms** (AGENTS.md #7): AWG source_ip_cidr
  (primary, работает через inter-node XHTTP туннель end-to-end) vs auth_user
  (legacy secondary, только entry hop pin). Новые per-client фичи → AWG модель.
- **E2E требует GCloud VPSes** (AGENTS.md "E2E Testing Infrastructure"):
  vps-de-test-1/2/3, gcloud auth login. НЕ запускать e2e без VPS доступа.
  UDP 443 firewall на exit VPS — известный infra blocker.
- **Hardware baseline**: AMD Ryzen 7 7800X3D / 31 GB RAM / NVMe (для dev).
  VPSes слабее — не путать при оценке perf.
- **i18n асимметрия**: en 262 ключа, ru 482. ru — canonical/primary (больше строк).
  README на 4 языках (en/ru/fa/zh), но UI i18n только en/ru.
- **CI отсутствует**: .github/workflows/ нет. Это сам по себе finding —
  рекомендуется минимальный CI (build + vet + test + templ check + govulncheck).
- **Кросс-сборки**: keenetic mipsel/armv7, opkg packaging — поддерживается в
  Makefile, но тестируется ли регулярно?
- **AGENTS.md = закон**: все агенты должны следовать workflow
  (READ→AUDIT→PLAN→CODE→TEMPL→BUILD→TEST→DOCS) и 10 правилам.