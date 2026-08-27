package web

// subpush.go — pushes the rendered subscription statics for every active user
// to a node with the sub utility installed. Caddy serves /sub/<token> from
// these files (format negotiated by query/UA matchers — see RenderCaddyfile).
// "Last config wins": the full set is re-rendered from the store and pushed on
// every apply, and the store revision is stamped into the utility state so a
// node that missed a push shows up as stale.

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

// PushNodeSubscriptions renders + uploads the subscription files for all users
// to one node. No-op (nil, nil) when the node has no sub utility. Errors leave
// the utility state stamped with the failure (visible in the utilities panel).
func (s *Server) PushNodeSubscriptions(ctx context.Context, nodeID string) error {
	st := s.store()
	info, err := st.GetNodeInfo(nodeID)
	if err != nil {
		return err
	}
	if !info.UtilityInstalled(model.UtilitySub) {
		return nil
	}
	host, err := st.GetHost(nodeID)
	if err != nil {
		return err
	}
	resolved := chain.ResolveHostKey(st, host)
	client, err := s.SSHConnector().Connect(resolved.Addr, resolved.User, resolved.KeyPath)
	if err != nil {
		s.setUtility(nodeID, model.UtilitySub, true, "", "error: "+err.Error(), 0)
		return fmt.Errorf("sub push: connect: %w", err)
	}
	defer client.Close()

	useSudo := info.UseSudo
	// Clean slate: files for deleted users must disappear (revocation).
	if err := chain.ClearSubStatics(ctx, client, useSudo); err != nil {
		s.setUtility(nodeID, model.UtilitySub, true, "", "error: "+err.Error(), 0)
		return fmt.Errorf("sub push: clear: %w", err)
	}

	users, _ := st.ListUsers()
	settings, _ := st.GetSettings()
	lang := "en"
	if settings != nil && settings.Language != "" {
		lang = settings.Language
	}

	pushed := 0
	for _, u := range users {
		if u.SubscriptionToken == "" {
			continue
		}
		// Mirror the /sub endpoint's lifecycle gate: expired/disabled users
		// get NO files (their subscription stops working everywhere).
		switch u.ComputeStatus() {
		case "disabled", "expired", "limited":
			continue
		}
		links := s.collectUserLinks(u, st)
		if len(links) == 0 {
			continue
		}
		files := renderSubFiles(u, links, lang)
		for name, content := range files {
			if err := chain.PushSubStatic(ctx, client, useSudo, name, content); err != nil {
				s.setUtility(nodeID, model.UtilitySub, true, "", "error: "+err.Error(), 0)
				return fmt.Errorf("sub push: %s: %w", name, err)
			}
		}
		pushed++
	}

	rev := st.GetRevision()
	s.setUtility(nodeID, model.UtilitySub, true, fmt.Sprintf("%d users", pushed), "", rev)
	slog.Info("sub push done", "node", nodeID, "users", pushed, "rev", rev)
	return nil
}

// renderSubFiles renders the per-user subscription file set (name -> content).
// Same formats the /sub endpoint serves; the Caddyfile maps query/UA onto them.
func renderSubFiles(u *model.User, links []string, lang string) map[string]string {
	tok := u.SubscriptionToken
	files := map[string]string{
		tok + ".raw": strings.Join(links, "\n"),
		tok + ".b64": base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n"))),
	}
	if clash := buildClashYAML(u.Name, links); clash != "" {
		files[tok+".clash.yaml"] = clash
	}
	if vpn := vpnLinksFrom(links); len(vpn) > 0 {
		files[tok+".vpn"] = strings.Join(vpn, "\n")
	}
	var buf bytes.Buffer
	ctx := context.WithValue(context.Background(), i18n.LangKey, lang)
	if err := templates.SubPage(u.Name, links, vpnLinksFrom(links)).Render(ctx, &buf); err == nil {
		files[tok+".html"] = buf.String()
	}
	return files
}

// maybePushSubsAfterApply pushes subscriptions to a node after a successful
// apply when the sub utility is installed. Best-effort: a push failure is
// reported in the apply result message (rule #6) but never fails the apply.
// Returns "" when nothing was to push or the push succeeded.
func (s *Server) maybePushSubsAfterApply(nodeID string) string {
	st := s.store()
	info, err := st.GetNodeInfo(nodeID)
	if err != nil || !info.UtilityInstalled(model.UtilitySub) {
		return ""
	}
	if err := s.PushNodeSubscriptions(context.Background(), nodeID); err != nil {
		return "subscriptions push failed: " + err.Error()
	}
	return ""
}
