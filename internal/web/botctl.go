package web

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/alexeylcp/angry-box/internal/bot"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// ─── Fleet Telegram bot lifecycle ────────────────────────────────────────────
// One bot per fleet, runs HERE in the orchestrator (long-polling — no webhook,
// no public address, works behind NAT). The settings form restarts it on save;
// serve starts it at boot; Server.Stop tears it down.

var botMu sync.Mutex
var fleetBot *bot.Bot

// StartBotIfConfigured boots the fleet bot from the stored settings (no-op
// when disabled/unconfigured). Called once at serve startup.
func (s *Server) StartBotIfConfigured() {
	if err := s.RestartBot(); err != nil {
		log.Printf("telegram bot not started: %v", err)
	}
}

// RestartBot stops the running fleet bot (if any) and starts it from the
// current store settings. Called after the settings form saves bot fields.
func (s *Server) RestartBot() error {
	settings, err := s.store().GetSettings()
	var cfg *model.TelegramBotConfig
	if err == nil && settings != nil {
		cfg = settings.TelegramBot
	}
	botMu.Lock()
	defer botMu.Unlock()
	if fleetBot != nil {
		fleetBot.Stop()
		fleetBot = nil
	}
	lockoutNotifyMu.Lock()
	authLockoutNotify = nil
	lockoutNotifyMu.Unlock()
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	b, err := bot.New(s.store(), cfg)
	if err != nil {
		return err
	}
	fleetBot = b
	lockoutNotifyMu.Lock()
	authLockoutNotify = b.NotifyAuthLockout
	lockoutNotifyMu.Unlock()
	return nil
}

// StopBot tears down the fleet bot (graceful shutdown path).
func (s *Server) StopBot() {
	botMu.Lock()
	defer botMu.Unlock()
	if fleetBot != nil {
		fleetBot.Stop()
		fleetBot = nil
	}
	lockoutNotifyMu.Lock()
	authLockoutNotify = nil
	lockoutNotifyMu.Unlock()
}

// telegramBotFromForm parses the bot section of the settings form. Returns
// nil when the bot is disabled with no token (keeps the settings clean).
func telegramBotFromForm(r *http.Request, prev *model.TelegramBotConfig) *model.TelegramBotConfig {
	enabled := r.FormValue("bot_enabled") == "on"
	token := strings.TrimSpace(r.FormValue("bot_token"))
	// An empty token keeps the previous one so re-saving unrelated settings
	// does not wipe the secret (the form renders it masked-but-present).
	if token == "" && prev != nil {
		token = prev.Token
	}
	cfg := &model.TelegramBotConfig{
		Enabled: enabled,
		Token:   token,
	}
	for _, part := range strings.Split(r.FormValue("bot_admin_ids"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && id != 0 {
			cfg.AdminIDs = append(cfg.AdminIDs, id)
		}
	}
	if v := strings.TrimSpace(r.FormValue("bot_notify_chat")); v != "" {
		cfg.NotifyChatID, _ = strconv.ParseInt(v, 10, 64)
	}
	if !enabled && token == "" {
		return nil
	}
	return cfg
}
