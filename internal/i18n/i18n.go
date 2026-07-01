package i18n

import "context"

type ctxKey string

const LangKey ctxKey = "lang"

var locales = map[string]map[string]string{
	"en": {
		"Dashboard": "Dashboard",
		"Nodes": "Nodes",
		"Spider Web": "Spider Web",
		"Chains": "Chains",
		"Users": "Users",
		"Status": "Status",
		"Settings": "Settings",
		"Orchestrator": "Orchestrator",
		
		// Dashboard
		"System Status": "System Status",
		"Hosts": "Hosts",
		"Proxy Chains": "Proxy Chains",
		"Clients": "Clients",
		"Map View": "Map View",
		
		// Nodes
		"Add Node": "Add Node",
		"Address": "Address",
		"Country": "Country",
		"Bandwidth": "Bandwidth",
		"Action": "Action",
		"Online": "Online",
		"Offline": "Offline",
		"Unknown": "Unknown",
		"Edit": "Edit",
		"Delete": "Delete",
		
		// Settings
		"Panel Settings": "Panel Settings",
		"General": "General",
		"Web UI Listen Port": "Web UI Listen Port",
		"Language": "Language",
		"Web UI Username": "Web UI Username",
		"Old Password": "Old Password",
		"New Password": "New Password",
		"Panel Country": "Panel Country",
		"Metrics Interval": "Metrics Interval",
		"Default Protocol": "Default Protocol",
		"SSH Keys": "SSH Keys",
		"Save Settings": "Save Settings",
		"Enable Web UI Authentication": "Enable Web UI Authentication",
		
		// Spider
		"Routing Graph": "Routing Graph",
		
		// General actions
		"Cancel": "Cancel",
		"Save": "Save",
		"Create": "Create",
		"Apply": "Apply",
		"Del": "Del",
		
		"Port changed!": "Port changed!",
		"Please restart the angry-box service manually to apply the new port.": "Please restart the angry-box service manually to apply the new port.",
		"Currently active on: ": "Currently active on: ",

		// Apply results
		"OK": "OK",
		"FAIL": "FAIL",
		"AWG Client Keys": "AWG Client Keys",
		"Server Public:": "Server Public:",
		"Client Public:": "Client Public:",
		"Client Private:": "Client Private:",
		"(44 chars, base64)": "(44 chars, base64)",

		// Host key warning
		"Error": "Error",
		"Host not found": "Host not found",
		"WARNING: Host Key Changed!": "WARNING: Host Key Changed!",
		"The SSH key for": "The SSH key for",
		"has changed since the last connection.": "has changed since the last connection.",
		"This could be due to a server reinstall, or it could be a Man-in-the-Middle attack. Do not proceed unless you know the key was changed intentionally.": "This could be due to a server reinstall, or it could be a Man-in-the-Middle attack. Do not proceed unless you know the key was changed intentionally.",
		"Untrusted Host Key": "Untrusted Host Key",
		"is currently marked as untrusted.": "is currently marked as untrusted.",
		"New Fingerprint:": "New Fingerprint:",
		"Trust and Continue": "Trust and Continue",

		// License
		"By using Angry-BOX, you acknowledge that:": "By using Angry-BOX, you acknowledge that:",
		"Circumvention of network censorship may be illegal in your jurisdiction": "Circumvention of network censorship may be illegal in your jurisdiction",
		"You are solely responsible for complying with applicable laws": "You are solely responsible for complying with applicable laws",
		"The software must not be used for any commercial purpose": "The software must not be used for any commercial purpose",

		// Nav / pages not yet translated
		"Audit": "Audit",
		"Profiles": "Profiles",
		"Deploy Status": "Deploy Status",
		"Takeover": "Takeover",
		"Health": "Health",
		"Connections": "Connections",
		"Deploy Role": "Deploy Role",
		"Deploy Standalone Config": "Deploy Standalone Config",
		"Default Obfuscation": "Default Obfuscation",
		"Build and manage multi-hop proxy chains.": "Build and manage multi-hop proxy chains.",
		"Manage remote nodes (servers and routers).": "Manage remote nodes (servers and routers).",
		"View overall system and node health.": "View overall system and node health.",
		"No nodes registered.": "No nodes registered.",
		"Drag nodes to rearrange (positions are saved). Scroll to zoom, drag background to pan. Green = online.": "Drag nodes to rearrange (positions are saved). Scroll to zoom, drag background to pan. Green = online.",
		"Delete connection ": "Delete connection ",

		// Hardcoded strings now wrapped
		"ID": "ID",
		"License & Disclaimer": "License & Disclaimer",
		"PolyForm Noncommercial License 1.0.0": "PolyForm Noncommercial License 1.0.0",
		"This software is licensed for personal, non-commercial, educational, and scientific use only. Any commercial use is strictly prohibited.": "This software is licensed for personal, non-commercial, educational, and scientific use only. Any commercial use is strictly prohibited.",
		"THE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND. The author assumes no responsibility for any damage, data loss, or legal consequences resulting from the use of this software.": "THE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND. The author assumes no responsibility for any damage, data loss, or legal consequences resulting from the use of this software.",
		"Toggle theme": "Toggle theme",
		"Click to expand": "Click to expand",
		"Close": "Close",
		"reset": "reset",
		"Personal/Educational use only": "Personal/Educational use only",
		"This software is licensed for": "This software is licensed for",
		"personal, non-commercial, educational, and scientific use only": "personal, non-commercial, educational, and scientific use only",
		"Any commercial use is strictly prohibited.": "Any commercial use is strictly prohibited.",
	},
	"ru": {
		"Dashboard": "Дашборд",
		"Nodes": "Ноды",
		"Spider Web": "Паутина",
		"Chains": "Цепочки",
		"Users": "Пользователи",
		"Status": "Статус",
		"Settings": "Настройки",
		"Orchestrator": "Оркестратор",
		
		// Dashboard
		"System Status": "Статус системы",
		"Hosts": "Серверы",
		"Proxy Chains": "Прокси-цепочки",
		"Clients": "Клиенты",
		"Map View": "Карта маршрутов",
		
		// Nodes
		"Add Node": "Добавить ноду",
		"Address": "Адрес",
		"Country": "Страна",
		"Bandwidth": "Канал",
		"Action": "Действие",
		"Online": "В сети",
		"Offline": "Офлайн",
		"Unknown": "Неизвестно",
		"Edit": "Изменить",
		"Delete": "Удалить",
		
		// Settings
		"Panel Settings": "Настройки панели",
		"General": "Основные настройки",
		"Web UI Listen Port": "Порт веб-интерфейса",
		"Language": "Язык интерфейса",
		"Web UI Username": "Логин администратора",
		"Old Password (required to change)": "Старый пароль (для изменения)",
		"New Password": "Новый пароль",
		"Enter current password": "Введите текущий пароль",
		"Leave empty to keep current": "Оставьте пустым, чтобы не менять",
		"Panel Country": "Локация панели",
		"Metrics Refresh Interval (minutes)": "Интервал опроса метрик (в минутах)",
		"How often to poll hosts when UI is closed (default: 240)": "Как часто опрашивать сервера в фоне (по умолчанию: 240)",
		"Default Protocol": "Протокол по умолчанию",
		"SSH Keys": "SSH-ключи",
		"Save Settings": "Сохранить настройки",
		"Enable Web UI Authentication": "Включить авторизацию",
		"If disabled, anyone can access the orchestrator without a password.": "Если отключено, любой сможет получить доступ к оркестратору без пароля.",
		"For Basic Authentication": "Для базовой HTTP авторизации",
		"e.g., :8090 or 127.0.0.1:8090": "например, :8090 или 127.0.0.1:8090",
		"Auto-detect": "Определять автоматически",
		"Russia (RU)": "Россия (RU)",
		"Iran (IR)": "Иран (IR)",
		"China (CN)": "Китай (CN)",
		"Other": "Другое",
		"Affects recommended obfuscation presets": "Влияет на рекомендуемые настройки маскировки",
		"AWG (AmneziaWG)": "AWG (AmneziaWG)",
		"TUIC v5": "TUIC v5",
		"VLESS Reality": "VLESS Reality",
		"Manage SSH keys for node capture. System keys auto-detected from ~/.ssh/.": "Управление SSH ключами. Системные ключи загружаются автоматически из ~/.ssh/.",
		"System Info": "Информация о системе",
		"Global default": "Глобально по умолчанию",
		"Stored": "Сохранен",
		"System": "Системный",
		"Add New SSH Key": "Добавить SSH ключ",
		"Key name (e.g. Home Server)": "Имя ключа (напр. Домашний сервер)",
		"Save Key": "Сохранить ключ",
		
		// Spider
		"Routing Graph": "Граф маршрутизации",
		
		// Dashboard / General
		"Manage Nodes": "Управление нодами",
		"Name": "Название",
		"Host": "Сервер",
		"Version": "Версия",
		"Latency": "Задержка",
		"Last Checked": "Последняя проверка",
		"Check": "Проверить",
		"Capture": "Захват",
		"Inbounds": "Входящие",
		"Delete node ": "Удалить ноду ",
		"No nodes registered yet.": "Ноды еще не добавлены.",
		"No nodes yet. Add your first remote node.": "Пока нет нод. Добавьте свою первую удаленную ноду.",
		"Add your first node": "Добавьте первую ноду",
		"No chains configured. Create one via the Spider Web or Chains page.": "Цепочки не настроены. Создайте их в Паутине или в разделе Цепочек.",
		"Manage Chains": "Управление цепочками",
		"hops": "узлов",
		"Never": "Никогда",
		"ago": "назад",
		"Tip: Use the Nodes page for full management. Auto-refreshes every 60 seconds.": "Совет: Используйте страницу Нод для полного управления. Автообновление каждые 60 секунд.",
		
		// Hosts
		"+ Add Host": "+ Добавить ноду",
		"ID": "ID",
		"User": "Пользователь",
		"Key": "Ключ",
		"No hosts yet. Add your first remote node.": "Пока нет нод. Добавьте свою первую удаленную ноду.",
		"Delete host ": "Удалить ноду ",
		"Add New Host": "Добавить новую ноду",
		"SSH Address": "Адрес SSH",
		"SSH User": "Пользователь SSH",
		"Path to SSH Private Key": "Путь к приватному ключу SSH",
		"Cancel": "Отмена",
		"Add Host": "Добавить ноду",
		"Running": "Работает",
		"Stopped": "Остановлен",

		// Spider
		"Refresh": "Обновить",
		"Visual map of all nodes and connections. Drag nodes to rearrange. Green = online.": "Визуальная карта нод и соединений. Перетаскивайте ноды. Зеленый = онлайн.",
		"Add nodes first": "Сначала добавьте ноду",
		"Create New Connection": "Создать новое соединение",
		"From Node": "От ноды",
		"To Node": "К ноде",
		"Select...": "Выберите...",
		"Transport": "Транспорт",
		"max obfuscation": "макс. маскировка",
		"XHTTP (max obfuscation, recommended)": "XHTTP (макс. маскировка, рекомендуется)",
		"Reality + XHTTP (max obfuscation)": "Reality + XHTTP (макс. маскировка)",
		"AWG / AmneziaWG (encrypted tunnel)": "AWG / AmneziaWG (зашифрованный туннель)",
		"Hysteria2 (max obfuscation, QUIC)": "Hysteria2 (макс. маскировка, QUIC)",
		"Chain Name": "Имя цепочки",
		"Create Link": "Создать связь",

		// Chains
		"Delete chain ": "Удалить цепочку ",
		"+ Create Chain": "+ Создать цепочку",
		"No hosts registered yet. Add hosts first on the Hosts page before creating chains.": "Пока нет серверов. Сначала добавьте серверы на странице Нод перед созданием цепочек.",
		"Strategy": "Стратегия",
		"No chains yet. Create your first multi-hop proxy chain.": "Цепочек еще нет. Создайте свою первую многоузловую прокси-цепочку.",
		"Create New Chain": "Создать новую цепочку",
		"Chain Name (unique)": "Имя цепочки (уникальное)",
		"User Protocol (entry)": "Протокол пользователя (вход)",
		"Telemt (MTProto)": "Telemt (MTProto)",
		"VLESS + Reality": "VLESS + Reality",
		"Obfuscation Profile": "Профиль маскировки",
		"Use global default": "Глобально по умолчанию",
		"Leave empty to use the global profile from config": "Оставьте пустым, чтобы использовать глобальный профиль из конфига",
		"Routing Strategy": "Стратегия маршрутизации",
		"urltest (best latency)": "urltest (лучшая задержка)",
		"failover": "failover (переключение при сбое)",
		"selector (manual)": "selector (ручной выбор)",
		"bond (load balance)": "bond (балансировка нагрузки)",
		"Nodes (in order — first is entry)": "Ноды (по порядку — первая на входе)",
		"Select nodes in order: first = entry (user connects here), last = exit (traffic leaves here), middle = hops": "Выберите ноды по порядку: первая = вход (подключение пользователя), последняя = выход (трафик выходит здесь), посередине = транзитные",
		"Create Chain": "Создать цепочку",
		"Edit Chain: ": "Изменить цепочку: ",
		"Current order: ": "Текущий порядок: ",
		"Save Changes": "Сохранить изменения",

		// Users
		"+ Add User": "+ Добавить пользователя",
		"Protocols": "Протоколы",
		"Expires": "Истекает",
		"No users yet. Add your first proxy user.": "Пока нет пользователей. Добавьте первого прокси-пользователя.",
		"None": "Нет",
		"Expired": "Истёк",
		"Active": "Активен",
		"Inactive": "Неактивен",
		"Config": "Конфиг",
		"QR": "QR код",
		"Delete user ": "Удалить пользователя ",
		"Edit User: ": "Изменить пользователя: ",
		"Add New User": "Добавить нового пользователя",
		"ID (unique)": "ID (уникальный)",
		"Create User": "Создать пользователя",
		"Expires At": "Действует до",
		"Telegram (optional)": "Telegram (необязательно)",
		"Email (optional)": "Email (необязательно)",
		"Import Secret (optional)": "Импортировать секрет (необязательно)",
		"Migrate an existing key from Telemt (MTProto), AWG Toolza, or another system. Paste the key below — it will be used instead of generating a new one.": "Мигрировать существующий ключ. Вставьте ключ ниже — он будет использован вместо создания нового.",
		"Telemt (MTProto) — Secret": "Telemt (MTProto) — Secret",
		"AWG — Private Key (base64, 44 chars)": "AWG — Приватный ключ (base64, 44 симв)",
		"TUIC v5 — UUID": "TUIC v5 — UUID",
		"VLESS Reality — Private Key": "VLESS Reality — Приватный ключ",
		"Shadowsocks — Password/Key": "Shadowsocks — Пароль/Ключ",
		"Trojan — Password": "Trojan — Пароль",
		"VMess — UUID": "VMess — UUID",
		"Hysteria2 — Password/Key": "Hysteria2 — Пароль/Ключ",
		"Paste your existing key here...": "Вставьте ваш ключ сюда...",
		"Telemt (MTProto):": "Telemt (MTProto):",
		"paste the secret/hex key": "вставьте секрет/hex ключ",
		"AWG:": "AWG:",
		"paste the WireGuard private key": "вставьте приватный ключ WireGuard",
		"TUIC:": "TUIC:",
		"paste the UUID": "вставьте UUID",
		"User protocols are determined by the chains and node inbounds they are assigned to.": "Протоколы пользователей определяются цепочками и входящими подключениями нод, к которым они привязаны.",
		"Assigned Chains": "Привязанные цепочки",
		"No chains available. Create chains first.": "Нет доступных цепочек. Сначала создайте цепочку.",
		"Configs for ": "Конфиги для ",
		"No configs available. Assign chains to this user first.": "Конфигов пока нет. Сначала привяжите цепочки к пользователю.",
		"Copy": "Копировать",
		"Close": "Закрыть",
		"QR Codes for ": "QR-коды для ",
		"No connection links available.": "Нет доступных ссылок для подключения.",
		"QR unavailable": "QR недоступен",
		"Open Link": "Открыть ссылку",

		"Port changed!": "Порт изменен!",
		"Please restart the angry-box service manually to apply the new port.": "Пожалуйста, перезапустите сервис angry-box вручную для применения нового порта.",
		"Currently active on: ": "Текущий активный порт: ",

		// Apply results
		"OK": "ОК",
		"FAIL": "ОШИБКА",
		"AWG Client Keys": "Ключи клиента AWG",
		"Server Public:": "Публ. ключ сервера:",
		"Client Public:": "Публ. ключ клиента:",
		"Client Private:": "Прив. ключ клиента:",
		"(44 chars, base64)": "(44 симв, base64)",

		// Host key warning
		"Error": "Ошибка",
		"Host not found": "Хост не найден",
		"WARNING: Host Key Changed!": "ВНИМАНИЕ: Ключ хоста изменился!",
		"The SSH key for": "SSH-ключ для",
		"has changed since the last connection.": "изменился с момента последнего подключения.",
		"This could be due to a server reinstall, or it could be a Man-in-the-Middle attack. Do not proceed unless you know the key was changed intentionally.": "Это может быть связано с переустановкой сервера или атакой Man-in-the-Middle. Не продолжайте, если вы не уверены, что ключ был изменён намеренно.",
		"Untrusted Host Key": "Недоверенный ключ хоста",
		"is currently marked as untrusted.": "помечен как недоверенный.",
		"New Fingerprint:": "Новый отпечаток:",
		"Trust and Continue": "Доверять и продолжить",

		// License
		"By using Angry-BOX, you acknowledge that:": "Используя Angry-BOX, вы подтверждаете, что:",
		"Circumvention of network censorship may be illegal in your jurisdiction": "Обход сетевой цензуры может быть незаконным в вашей юрисдикции",
		"You are solely responsible for complying with applicable laws": "Вы несёте полную ответственность за соблюдение применимых законов",
		"The software must not be used for any commercial purpose": "Программное обеспечение не должно использоваться в коммерческих целях",

		// Chains list
		"Del": "Удл",

		// Dashboard (index)
		"Welcome to Angry-BOX": "Angry-BOX",
		"Lightweight SSH orchestrator for sing-box and xray nodes.": "Легковесный SSH-оркестратор для нод sing-box и xray.",
		"Go to Hosts": "К нодам",
		"Go to Chains": "К цепочкам",
		"View Status": "Статус системы",

		// Base layout
		"Angry-BOX • Orchestrator": "Angry-BOX • Оркестратор",
		"Profile:": "Профиль:",
		"Connected": "Подключен",

		// Missing keys
		"Old Password": "Старый пароль",
		"Metrics Interval": "Интервал метрик",
		"Save": "Сохранить",
		"Create": "Создать",
		"Apply": "Применить",

		// Node page
		"In Chain": "В цепочке",
		"Standalone": "Автономная",
		"Apply (Chain)": "Применить (Цепочка)",
		"Managed by chain: ": "Управляется цепочкой: ",
		"Can be deployed as a standalone server": "Может быть развёрнута как автономный сервер",
		"Apply via Chain": "Применить через цепочку",
		"includes standalone inbounds": "включая автономные входящие",
		"inbounds": "входящих",
		"0 inbounds": "0 входящих",
		"SSH Key": "SSH-ключ",
		"IP:port": "IP:порт",
		"usually root": "обычно root",
		"Select key...": "Выберите ключ...",
		"== Enter key manually ==": "== Ввести ключ вручную ==",
		"Source": "Источник",
		"Password": "Пароль",
		"Captured": "Захвачен",
		"Edit Host: ": "Изменить ноду: ",

		// Inbounds form
		"Inbounds for ": "Входящие для ",
		"Protocol": "Протокол",
		"Port": "Порт",
		"For Users": "Для пользователей",
		"Obfuscation Preset": "Прессет маскировки",
		"No users yet. ": "Пока нет пользователей. ",
		"Create first user": "Создать пользователя",
		"+ Add Inbound": "+ Добавить входящее",
		"Save Inbounds": "Сохранить входящие",

		// Protocol names
		"Shadowsocks": "Shadowsocks",
		"Trojan": "Trojan",
		"VMess": "VMess",
		"Hysteria2": "Hysteria2",

		// Capture form
		"Capture Node: ": "Захват ноды: ",
		"Connect via SSH and bring node under management.": "Подключиться по SSH и взять ноду под управление.",
		"Login User": "Пользователь",
		"Enter SSH password": "Введите пароль SSH",
		"— or use login + password —": "— или используйте логин + пароль —",
		"Auto-install SSH key on server after successful login": "Автоустановка SSH-ключа после входа",
		"If checked, the SSH key selected above will be installed on the target server for future passwordless access.": "Если отмечено, выбранный SSH-ключ будет установлен на сервер для будущего беспарольного доступа.",

		// Settings
		"English": "English",
		"Русский": "Русский",
		"License & Disclaimer": "Лицензия и отказ от ответственности",

		// Error / result messages
		"Cannot save zero inbounds. Add at least one inbound or delete the node instead.": "Нельзя сохранить 0 входящих. Добавьте хотя бы одно входящее подключение или удалите ноду.",
		"Inbounds saved.": "Входящие сохранены.",
		"Failed to delete: %v": "Ошибка удаления: %v",
		"Capture failed: %v": "Ошибка захвата: %v",
		"Node %s captured!": "Нода %s захвачена!",
		"Running: %v, Version: %s.": "Запущено: %v, Версия: %s.",
		"Refresh Nodes": "Обновить ноды",
		"Note: SSH key auto-generation failed: %v": "Примечание: авто-генерация SSH-ключа не удалась: %v",
		"Note: SSH key installation failed: %v": "Примечание: установка SSH-ключа не удалась: %v",
		"Failed to change password: old password is incorrect.": "Не удалось изменить пароль: старый пароль неверен.",
		"Settings saved, but config write failed: %v": "Настройки сохранены, но запись конфига не удалась: %v",
		"Settings saved.": "Настройки сохранены.",
		"Name and key data are required.": "Имя и данные ключа обязательны.",
		"Invalid key format. Expected a private key (BEGIN ... PRIVATE KEY).": "Неверный формат ключа. Ожидается приватный ключ (BEGIN ... PRIVATE KEY).",
		"No hosts registered yet. Add nodes first.": "Нет зарегистрированных нод. Сначала добавьте ноды.",
		"[Chain] ": "[Цепочка] ",

		// User/chain description strings
		"chain — ": "цепочка — ",
		"hops, strategy: ": "узлов, стратегия: ",
		"Standalone inbound on ": "Автономное входящее на ",
		"# Assign chains or node inbounds to this user to generate configs.": "# Назначьте цепочки или входящие пользователю для генерации конфигов.",
		"User has no chains assigned.": "У пользователя нет назначенных цепочек.",
		"chain(s) available — edit user to assign.": "цепочек доступно — измените пользователя для назначения.",
		"# Create a chain or node inbound first, then assign it to this user.": "# Сначала создайте цепочку или входящее, затем назначьте пользователю.",
		"No chains or standalone inbounds exist yet.": "Цепочек или автономных входящих ещё нет.",

		// License
		"PolyForm Noncommercial License 1.0.0": "PolyForm Noncommercial License 1.0.0",
		"This software is licensed for personal, non-commercial, educational, and scientific use only. Any commercial use is strictly prohibited.": "ПО лицензировано только для личного, некоммерческого, образовательного и научного использования. Коммерческое использование строго запрещено.",
		"THE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND. The author assumes no responsibility for any damage, data loss, or legal consequences resulting from the use of this software.": "ПО ПРЕДОСТАВЛЯЕТСЯ «КАК ЕСТЬ», БЕЗ КАКИХ-ЛИБО ГАРАНТИЙ. Автор не несёт ответственности за любой ущерб, потерю данных или правовые последствия, возникшие в результате использования данного ПО.",

		// Nav / pages previously untranslated
		"Audit": "Аудит",
		"Profiles": "Профили",
		"Deploy Status": "Статус деплоя",
		"Takeover": "Перехват",
		"Health": "Здоровье",
		"Connections": "Соединения",
		"Deploy Role": "Роль деплоя",
		"Deploy Standalone Config": "Деплой автономного конфига",
		"Default Obfuscation": "Маскировка по умолчанию",
		"Build and manage multi-hop proxy chains.": "Создание и управление многоузловыми прокси-цепочками.",
		"Manage remote nodes (servers and routers).": "Управление удалёнными нодами (серверами и роутерами).",
		"View overall system and node health.": "Общий статус системы и нод.",
		"No nodes registered.": "Ноды не зарегистрированы.",
		"Drag nodes to rearrange (positions are saved). Scroll to zoom, drag background to pan. Green = online.": "Перетаскивайте ноды для перестановки (позиции сохраняются). Колесо — масштаб, перетаскивание фона — панорама. Зелёный = онлайн.",
		"Delete connection ": "Удалить соединение ",

		// Previously hardcoded strings (now wrapped in i18n.T)
		"Toggle theme": "Переключить тему",
		"Click to expand": "Развернуть",
		"reset": "сброс",
		"Personal/Educational use only": "Только личное/образовательное использование",
		"This software is licensed for": "ПО лицензировано для",
		"personal, non-commercial, educational, and scientific use only": "личного, некоммерческого, образовательного и научного использования",
		"Any commercial use is strictly prohibited.": "Коммерческое использование строго запрещено.",
	},
}

// T returns the translated string for the given key based on the language in context.
func T(ctx context.Context, key string) string {
	lang, ok := ctx.Value(LangKey).(string)
	if !ok || lang == "" {
		lang = "en" // Default
	}
	
	if dict, found := locales[lang]; found {
		if val, exists := dict[key]; exists {
			return val
		}
	}
	return key
}
