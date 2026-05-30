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
