package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// ─── The cron framework (P3.5) ───────────────────────────────────────────────
// A single hourly loop owns every periodic job the orchestrator needs:
//  1. start_on_first_use deadline enforcement — a user whose activation
//     deadline passed without a first subscription fetch becomes expired
//     (Marzneshin semantics; ComputeStatus then reports "expired" and the
//     /sub endpoints + node subscription statics stop serving them).
//  2. Expiry warnings — one Telegram notification per user per expiry window
//     (72h ahead), to the operator's notify chat and, when bound, the user.
// Adding a new periodic job = one more function called from the tick.

const notifyEvery = time.Hour

func (b *Bot) notifyLoop(ctx context.Context) {
	ticker := time.NewTicker(notifyEvery)
	defer ticker.Stop()
	// Run once at start so a restart doesn't leave a >1h gap.
	b.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.tick(ctx)
		}
	}
}

func (b *Bot) tick(ctx context.Context) {
	users, err := b.store.ListUsers()
	if err != nil {
		slog.Warn("bot tick: list users", "err", err)
		return
	}
	b.enforceActivationDeadlines(users)
	b.warnExpiring(ctx, users)
}

// enforceActivationDeadlines expires start_on_first_use users whose outer
// activation deadline has passed without a first /sub fetch.
func (b *Bot) enforceActivationDeadlines(users []*model.User) {
	now := time.Now()
	for _, u := range users {
		if u.ExpireStrategy != "start_on_first_use" || !u.FirstUseAt.IsZero() {
			continue
		}
		if u.ActivationDeadline.IsZero() || now.Before(u.ActivationDeadline) {
			continue
		}
		if !u.ExpiresAt.IsZero() && !now.After(u.ExpiresAt) {
			continue // already expired-by-date
		}
		u.ExpiresAt = u.ActivationDeadline
		if err := b.store.SaveUser(u); err != nil {
			slog.Warn("bot tick: enforce deadline", "user", u.ID, "err", err)
			continue
		}
		chain.WriteAudit(b.store, "activation-deadline-expired", "user", u.ID, chain.AuditPayload{
			"deadline": u.ActivationDeadline.Format(time.RFC3339),
		}, "bot")
	}
}

// warnExpiring notifies once per user when their expiry lands within the next
// 72 hours. The message goes to the operator's notify chat and (when bound) to
// the user themselves.
func (b *Bot) warnExpiring(ctx context.Context, users []*model.User) {
	now := time.Now()
	window := 72 * time.Hour
	for _, u := range users {
		if u.ComputeStatus() != "active" || u.ExpiresAt.IsZero() {
			continue
		}
		left := time.Until(u.ExpiresAt)
		if left <= 0 || left > window {
			continue
		}
		// One warning per expiry instant: a user already warned for THIS
		// ExpiresAt is skipped (re-notifying after the operator extends the
		// expiry happens naturally — ExpiresAt moved, the comparison fails).
		if !u.ExpiryNotifiedAt.IsZero() && u.ExpiryNotifiedAt.After(u.ExpiresAt.Add(-window)) {
			continue
		}
		msg := fmt.Sprintf(b.t("Bot expiry warning"), u.Name, u.ExpiresAt.Format("2006-01-02 15:04"), hoursRound(left))
		if b.cfg.NotifyChatID != 0 {
			b.send(ctx, b.cfg.NotifyChatID, msg)
		}
		if u.TelegramID != 0 {
			b.send(ctx, u.TelegramID, fmt.Sprintf(b.t("Bot expiry warning user"), u.ExpiresAt.Format("2006-01-02 15:04")))
		}
		u.ExpiryNotifiedAt = now
		if err := b.store.SaveUser(u); err != nil {
			slog.Warn("bot tick: mark notified", "user", u.ID, "err", err)
		}
	}
}

func hoursRound(d time.Duration) int {
	return int(d.Hours() + 0.5)
}
