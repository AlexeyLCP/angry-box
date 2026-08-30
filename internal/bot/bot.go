package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/mymmrac/telego"
)

// Bot is the fleet-wide Telegram bot. There is exactly ONE per fleet and it
// runs in the orchestrator (long-polling — works behind NAT with no webhook /
// cert / public address). Nodes never run their own bots. All state comes from
// the store — the bot is a read-mostly projection + the user-binding writer.

type Bot struct {
	store  *chain.Store
	cfg    model.TelegramBotConfig
	tg     *telego.Bot
	cancel context.CancelFunc
	done   chan struct{}
}

// New validates the token, starts the long-polling command loop + the hourly
// notification loop, and returns the running bot. The caller owns Stop.
func New(store *chain.Store, cfg *model.TelegramBotConfig) (*Bot, error) {
	if cfg == nil || !cfg.Enabled || strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("bot: disabled or empty token")
	}
	tg, err := telego.NewBot(strings.TrimSpace(cfg.Token))
	if err != nil {
		return nil, fmt.Errorf("bot: init: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	me, err := tg.GetMe(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bot: token rejected: %w", err)
	}
	updates, err := tg.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout:        25,
		AllowedUpdates: []string{"message"},
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bot: long polling: %w", err)
	}
	b := &Bot{store: store, cfg: *cfg, tg: tg, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(b.done)
		b.loop(ctx, updates)
	}()
	go b.notifyLoop(ctx)
	slog.Info("telegram bot started", "username", me.Username)
	return b, nil
}

// Stop cancels the polling + notify loops and waits for them to finish.
func (b *Bot) Stop() {
	if b == nil {
		return
	}
	b.cancel()
	<-b.done
}

func (b *Bot) loop(ctx context.Context, updates <-chan telego.Update) {
	for {
		select {
		case <-ctx.Done():
			return
		case up, ok := <-updates:
			if !ok {
				return
			}
			if up.Message != nil {
				b.handleMessage(ctx, up.Message)
			}
		}
	}
}

func (b *Bot) send(ctx context.Context, chatID int64, text string) {
	if _, err := b.tg.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: chatID},
		Text:   text,
	}); err != nil {
		slog.Warn("bot: send failed", "chat", chatID, "err", err)
	}
}

// langCtx builds an i18n context in the panel language (the bot serves the
// operator's fleet language, not per-user locales).
func (b *Bot) langCtx() context.Context {
	lang := "en"
	if s, err := b.store.GetSettings(); err == nil && s != nil && s.Language != "" {
		lang = s.Language
	}
	return context.WithValue(context.Background(), i18n.LangKey, lang)
}

func (b *Bot) t(key string) string { return i18n.T(b.langCtx(), key) }

func (b *Bot) isAdmin(id int64) bool {
	for _, a := range b.cfg.AdminIDs {
		if a == id {
			return true
		}
	}
	return false
}

func (b *Bot) handleMessage(ctx context.Context, msg *telego.Message) {
	if msg.From == nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(msg.Text))
	if len(fields) == 0 {
		return
	}
	cmd := strings.ToLower(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}
	from := msg.From.ID
	switch cmd {
	case "/start":
		b.handleStart(ctx, msg.Chat.ID, from, arg)
	case "/status":
		b.handleStatus(ctx, msg.Chat.ID, from)
	case "/config":
		b.handleConfig(ctx, msg.Chat.ID, from)
	case "/online":
		if b.isAdmin(from) {
			b.send(ctx, msg.Chat.ID, b.onlineReport())
		}
	case "/nodes":
		if b.isAdmin(from) {
			b.send(ctx, msg.Chat.ID, b.nodesReport())
		}
	case "/users":
		if b.isAdmin(from) {
			b.send(ctx, msg.Chat.ID, b.usersReport())
		}
	case "/link":
		if b.isAdmin(from) {
			b.handleLink(ctx, msg.Chat.ID, arg)
		}
	default:
		b.send(ctx, msg.Chat.ID, b.t("Bot help"))
	}
}

// handleStart binds a Telegram account to a user via the one-time code an
// admin issued with /link. Without a code it explains the flow + reveals the
// sender's numeric ID (needed to become an admin).
func (b *Bot) handleStart(ctx context.Context, chatID, from int64, code string) {
	if code == "" {
		b.send(ctx, chatID, fmt.Sprintf(b.t("Bot start hint"), from))
		return
	}
	if !bindAllowed(from) {
		b.send(ctx, chatID, b.t("Bot code not found"))
		return
	}
	users, _ := b.store.ListUsers()
	for _, u := range users {
		if u.TelegramBindCode == "" || !strings.EqualFold(u.TelegramBindCode, code) {
			continue
		}
		if u.TelegramBindCodeAt.IsZero() || time.Since(u.TelegramBindCodeAt) > bindCodeTTL {
			continue
		}
		u.TelegramID = from
		u.TelegramBindCode = ""
		u.TelegramBindCodeAt = time.Time{}
		if err := b.store.SaveUser(u); err != nil {
			b.send(ctx, chatID, "error: "+err.Error())
			return
		}
		chain.WriteAudit(b.store, "telegram-bind", "user", u.ID, chain.AuditPayload{
			"telegram_id": from,
		}, "bot")
		b.send(ctx, chatID, fmt.Sprintf(b.t("Bot bound"), u.Name))
		return
	}
	bindRecordFail(from)
	b.send(ctx, chatID, b.t("Bot code not found"))
}

// handleStatus: admins get the fleet summary; bound users get their own
// lifecycle + usage; strangers get the start hint.
func (b *Bot) handleStatus(ctx context.Context, chatID, from int64) {
	if b.isAdmin(from) {
		b.send(ctx, chatID, b.usersReport())
		return
	}
	u := b.userByTelegram(from)
	if u == nil {
		b.send(ctx, chatID, b.t("Bot not bound"))
		return
	}
	b.send(ctx, chatID, userStatusText(u, b.t))
}

// handleConfig sends a bound user their subscription URLs — one per node that
// serves subscriptions (the spinal-cord entry points), plus the token.
func (b *Bot) handleConfig(ctx context.Context, chatID, from int64) {
	u := b.userByTelegram(from)
	if u == nil {
		b.send(ctx, chatID, b.t("Bot not bound"))
		return
	}
	if u.SubscriptionToken == "" {
		if tok, err := chain.GenerateSubscriptionToken(); err == nil {
			u.SubscriptionToken = tok
			_ = b.store.SaveUser(u)
		}
	}
	var lines []string
	infos, _ := b.store.ListNodeInfos()
	for _, info := range infos {
		if info.TLSDomain != "" && info.UtilityInstalled(model.UtilitySub) {
			lines = append(lines, fmt.Sprintf("https://%s/sub/%s", info.TLSDomain, u.SubscriptionToken))
		}
	}
	if len(lines) == 0 {
		b.send(ctx, chatID, b.t("Bot config no nodes")+"\ntoken: "+u.SubscriptionToken)
		return
	}
	b.send(ctx, chatID, b.t("Bot config links")+"\n"+strings.Join(lines, "\n"))
}

// handleLink issues a one-time bind code for a user (by name or ID) so the user
// can run /start <code> from their own account.
func (b *Bot) handleLink(ctx context.Context, chatID int64, arg string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		b.send(ctx, chatID, b.t("Bot link usage"))
		return
	}
	users, _ := b.store.ListUsers()
	var target *model.User
	for _, u := range users {
		if strings.EqualFold(u.ID, arg) || strings.EqualFold(u.Name, arg) {
			target = u
			break
		}
	}
	if target == nil {
		b.send(ctx, chatID, fmt.Sprintf(b.t("Bot user not found"), arg))
		return
	}
	code := bindCode()
	if code == "" {
		b.send(ctx, chatID, "error: bind code")
		return
	}
	target.TelegramBindCode = code
	target.TelegramBindCodeAt = time.Now()
	if err := b.store.SaveUser(target); err != nil {
		b.send(ctx, chatID, "error: "+err.Error())
		return
	}
	b.send(ctx, chatID, fmt.Sprintf(b.t("Bot link issued"), target.Name, code))
}

func (b *Bot) userByTelegram(id int64) *model.User {
	if id == 0 {
		return nil
	}
	users, _ := b.store.ListUsers()
	for _, u := range users {
		if u.TelegramID == id {
			return u
		}
	}
	return nil
}

// onlineWindow is how recent the last AWG traffic observation must be for a
// user to count as online. Bounded below by the metrics interval granularity.
const onlineWindow = 6 * time.Minute

func (b *Bot) onlineReport() string {
	users, _ := b.store.ListUsers()
	var names []string
	total := 0
	for _, u := range users {
		if !u.Active {
			continue
		}
		total++
		if userOnline(u) {
			names = append(names, u.Name)
		}
	}
	if len(names) == 0 {
		return fmt.Sprintf(b.t("Bot online none"), total)
	}
	return fmt.Sprintf(b.t("Bot online list"), len(names), total, strings.Join(names, ", "))
}

func userOnline(u *model.User) bool {
	return !u.AWGTrafficAt.IsZero() && time.Since(u.AWGTrafficAt) <= onlineWindow
}

func (b *Bot) nodesReport() string {
	infos, _ := b.store.ListNodeInfos()
	if len(infos) == 0 {
		return b.t("Bot nodes none")
	}
	var lines []string
	for _, info := range infos {
		mark := ""
		if info.TLSDomain != "" {
			mark = " [" + info.TLSDomain + "]"
		}
		lines = append(lines, fmt.Sprintf("• %s %s%s — %d inbounds", info.ID, info.Addr, mark, len(info.Inbounds)))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) usersReport() string {
	users, _ := b.store.ListUsers()
	active, expired, onHold := 0, 0, 0
	for _, u := range users {
		switch u.ComputeStatus() {
		case "active":
			active++
		case "expired":
			expired++
		case "on_hold":
			onHold++
		}
	}
	return fmt.Sprintf(b.t("Bot users summary"), len(users), active, onHold, expired)
}

// userStatusText renders one user's lifecycle + usage for the /status reply.
func userStatusText(u *model.User, t func(string) string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", t("Status"), u.ComputeStatus())
	if !u.ExpiresAt.IsZero() {
		fmt.Fprintf(&b, "%s: %s\n", t("Expires"), u.ExpiresAt.Format("2006-01-02 15:04"))
	}
	if u.DataLimit > 0 {
		fmt.Fprintf(&b, "%s: %s / %s\n", t("Traffic"), humanBytes(u.UsedTraffic), humanBytes(u.DataLimit))
	}
	if u.AWGRxBytes+u.AWGTxBytes > 0 {
		fmt.Fprintf(&b, "AWG: ↓%s ↑%s\n", humanBytes(u.AWGRxBytes), humanBytes(u.AWGTxBytes))
	}
	return strings.TrimRight(b.String(), "\n")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

const bindCodeTTL = 10 * time.Minute

var (
	bindFailMu sync.Mutex
	bindFails  = map[int64][]time.Time{}
)

func bindAllowed(from int64) bool {
	bindFailMu.Lock()
	defer bindFailMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-15 * time.Minute)
	keep := bindFails[from][:0]
	for _, t := range bindFails[from] {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	bindFails[from] = keep
	return len(keep) < 5
}

func bindRecordFail(from int64) {
	bindFailMu.Lock()
	defer bindFailMu.Unlock()
	bindFails[from] = append(bindFails[from], time.Now())
}

func bindCode() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// NotifyAuthLockout alerts the operator's notify chat that an IP was locked
// out by the brute-force limiter (panel exposed via a relay = public attack
// surface; visibility matters). No-op without a notify chat.
func (b *Bot) NotifyAuthLockout(ip string) {
	if b == nil || b.cfg.NotifyChatID == 0 {
		return
	}
	b.send(context.Background(), b.cfg.NotifyChatID, fmt.Sprintf(b.t("Bot auth lockout"), ip))
}
