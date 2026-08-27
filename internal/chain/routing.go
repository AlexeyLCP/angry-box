package chain

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// routing.go — the manual (operator) route-rule engine (LucX-port slice 1,
// 2026-08-27). RouteRule (per-node, model/panel.go) is expanded here into
// sing-box 1.14 route rules + LOCAL rule_set assets.
//
// Constraints inherited from the amnezia-box fork (sing-box 1.14):
//   - the legacy geosite/geoip matchers are REMOVED upstream (a config that
//     uses them fails startup) — geo matching goes through rule_set (.srs);
//   - there is no "block" action — blocking is action:"reject";
//   - rule_set sources are pushed to the node over SSH at deploy time
//     (type:"local", NodeRuleSetDir) because RU nodes often cannot reach
//     GitHub raw at sing-box startup (deploy would roll back in a loop).

// NodeRuleSetDir is where the deploy pushes .srs rule-set assets on the node.
const NodeRuleSetDir = "/etc/sing-box/rules"

const (
	ruleSetKindGeoSite = "geosite"
	ruleSetKindGeoIP   = "geoip"
)

// RuleSetTag returns the sing-box rule_set tag for a geo asset: geosite
// categories keep their name ("telegram"), geoip countries get the geoip-
// prefix ("geoip-ru") — the same convention BuildRoutingSection uses.
func RuleSetTag(kind, name string) string {
	if kind == ruleSetKindGeoIP {
		return "geoip-" + strings.ToLower(strings.TrimSpace(name))
	}
	return strings.TrimSpace(name)
}

// RuleSetAssetURL returns the upstream URL (SagerNet sing-geosite / sing-geoip
// rule-set branches) the orchestrator downloads the .srs from. File naming:
// geosite-<category>.srs / geoip-<cc>.srs (verified 2026-08-27).
func RuleSetAssetURL(kind, name string) string {
	if kind == ruleSetKindGeoIP {
		return ruleSetBaseURL + "/" + RuleSetTag(kind, name) + ".srs"
	}
	return ruleSetGeoSiteURL + "/geosite-" + RuleSetTag(kind, name) + ".srs"
}

// RuleSetAsset is one .srs asset the deploy must fetch locally and push to the
// node (referenced by a local rule_set entry in the generated config).
type RuleSetAsset struct {
	Tag  string
	Kind string // geosite|geoip
	Name string
}

// ManualRouteExpansion is the result of expanding a node's manual RouteRules.
type ManualRouteExpansion struct {
	Rules    []config.RouteRuleEntry
	RuleSets []config.RuleSetEntry // type:"local", Path on the node
	Assets   []RuleSetAsset        // deduped, same order as RuleSets
	Warnings []string
}

// splitMatchValues splits a MatchValues blob on newlines/commas, trims and
// drops empties.
func splitMatchValues(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ',' || r == ';'
	}) {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ruleSetTagValid gates operator-supplied geo names: the tag becomes part of a
// file path pushed to the node AND lands in a sudo shell command at deploy
// time — anything outside [A-Za-z0-9_.-] is rejected up front.
func ruleSetTagValid(tag string) bool {
	if tag == "" || len(tag) > 64 {
		return false
	}
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

// ExpandManualRouteRules turns a node's operator RouteRules (already sorted by
// priority — store.ListRouteRulesForNode) into sing-box route rules.
//
// inboundScope is the set of USER-FACING inbound tags on this node (standalone
// sa-* tags, chain user-in tags, tun-in): global rules are scoped to it so a
// domain/block rule never touches inter-node TRANSIT traffic (first-match-wins
// — matching transit would break the cascade). User-scoped rules additionally
// carry source_ip_cidr (AWG inner IP, works through chains) and/or auth_user
// (entry-only identity for non-AWG user protocols).
//
// users resolves UserIDs to identities; scoped rules naming unknown users are
// skipped with a warning.
func ExpandManualRouteRules(rules []*model.RouteRule, inboundScope []string, users []model.User) ManualRouteExpansion {
	var ex ManualRouteExpansion
	usersByID := make(map[string]*model.User, len(users))
	for i := range users {
		usersByID[users[i].ID] = &users[i]
	}
	wantAsset := map[string]bool{}

	addAsset := func(kind, name string) (string, error) {
		tag := RuleSetTag(kind, name)
		if !ruleSetTagValid(tag) {
			return "", fmt.Errorf("invalid geo name %q (allowed: letters, digits, _ - .)", name)
		}
		if !wantAsset[tag] {
			wantAsset[tag] = true
			ex.RuleSets = append(ex.RuleSets, config.RuleSetEntry{
				Tag:    tag,
				Type:   "local",
				Format: "binary",
				Path:   NodeRuleSetDir + "/" + tag + ".srs",
			})
			ex.Assets = append(ex.Assets, RuleSetAsset{Tag: tag, Kind: kind, Name: name})
		}
		return tag, nil
	}

	for _, r := range rules {
		if r == nil || !r.Enabled {
			continue
		}
		base := config.RouteRuleEntry{}
		var ruleSetTags []string
		if err := applyRuleMatcher(r, &base, &ruleSetTags, addAsset); err != nil {
			ex.Warnings = append(ex.Warnings, fmt.Sprintf("route rule %q: %v", r.ID, err))
			continue
		}
		// applyRuleMatcher may rewrite the action (the ads preset forces reject)
		// — read it AFTER the expansion.
		action := r.Action
		for _, tag := range ruleSetTags {
			base.RuleSet = append(base.RuleSet, tag)
		}
		if action == "" {
			action = "route"
		}
		if action == "block" {
			action = "reject" // sing-box 1.14 has no block action
		}
		switch action {
		case "direct":
			base.Action = "direct"
		case "reject":
			base.Action = "reject"
		case "sniff":
			base.Action = "sniff"
		case "hijack-dns":
			base.Action = "hijack-dns"
		default: // route
			tag := strings.TrimSpace(r.OutboundTag)
			if tag == "" {
				tag = "direct-out"
			}
			base.Outbound = tag
		}

		if len(r.UserIDs) == 0 {
			base.Inbound = append([]string{}, inboundScope...)
			ex.Rules = append(ex.Rules, base)
			continue
		}

		// User-scoped rule: AWG users match by their inner tunnel IP (preserved
		// end-to-end through chains — the primary per-client mechanism); the
		// auth_user entry covers non-AWG user-entry protocols on the entry node.
		var cidrs, authIDs []string
		unknown := 0
		for _, uid := range r.UserIDs {
			u := usersByID[uid]
			if u == nil {
				unknown++
				continue
			}
			if u.AWGAddress != "" {
				cidrs = append(cidrs, u.AWGAddress)
			}
			authIDs = append(authIDs, userIdentities(*u)...)
		}
		if unknown > 0 {
			ex.Warnings = append(ex.Warnings, fmt.Sprintf("route rule %q: %d unknown user ID(s) skipped", r.ID, unknown))
		}
		if len(cidrs) == 0 && len(authIDs) == 0 {
			ex.Warnings = append(ex.Warnings, fmt.Sprintf("route rule %q: scoped users have no AWG address or auth identity — rule skipped", r.ID))
			continue
		}
		if len(cidrs) > 0 {
			byIP := base
			byIP.Inbound = append([]string{}, inboundScope...)
			byIP.SourceIPCIDR = append([]string{}, cidrs...)
			ex.Rules = append(ex.Rules, byIP)
		}
		if len(authIDs) > 0 {
			byAuth := base
			byAuth.Inbound = append([]string{}, inboundScope...)
			byAuth.AuthUser = append([]string{}, authIDs...)
			ex.Rules = append(ex.Rules, byAuth)
		}
	}
	return ex
}

// applyRuleMatcher fills the matcher fields of entry for one RouteRule.
// geosite/geoip/preset-with-ads register a rule_set asset via addAsset and
// append its tag to ruleSetTags.
func applyRuleMatcher(r *model.RouteRule, entry *config.RouteRuleEntry, ruleSetTags *[]string, addAsset func(kind, name string) (string, error)) error {
	values := splitMatchValues(r.MatchValues)
	switch r.MatchType {
	case "domain":
		if len(values) == 0 {
			return fmt.Errorf("match_values required for domain")
		}
		entry.Domain = values
	case "domain_suffix":
		if len(values) == 0 {
			return fmt.Errorf("match_values required for domain_suffix")
		}
		entry.DomainSuffix = values
	case "domain_keyword":
		if len(values) == 0 {
			return fmt.Errorf("match_values required for domain_keyword")
		}
		entry.DomainKeyword = values
	case "ip_cidr":
		if len(values) == 0 {
			return fmt.Errorf("match_values required for ip_cidr")
		}
		entry.IPCidr = values
	case "protocol":
		if len(values) == 0 {
			return fmt.Errorf("match_values required for protocol")
		}
		entry.Protocol = values
	case "preset":
		if len(values) == 0 {
			return fmt.Errorf("preset id required")
		}
		p, ok := GetRoutingPreset(values[0])
		if !ok {
			return fmt.Errorf("unknown routing preset %q", values[0])
		}
		if p.Action == "reject" {
			// The ads preset ships no domain list — it maps to the geosite
			// category-ads-all rule set (the upstream ad-block list).
			tag, err := addAsset(ruleSetKindGeoSite, "category-ads-all")
			if err != nil {
				return err
			}
			*ruleSetTags = append(*ruleSetTags, tag)
			if r.Action == "" || r.Action == "route" {
				r.Action = "reject"
			}
			return nil
		}
		if len(p.Domains) == 0 {
			return fmt.Errorf("routing preset %q has no domains", p.ID)
		}
		entry.DomainSuffix = append([]string{}, p.Domains...)
	case "geosite":
		if len(values) == 0 {
			return fmt.Errorf("geosite category required")
		}
		for _, name := range values {
			tag, err := addAsset(ruleSetKindGeoSite, name)
			if err != nil {
				return err
			}
			*ruleSetTags = append(*ruleSetTags, tag)
		}
	case "geoip":
		if len(values) == 0 {
			return fmt.Errorf("geoip country code required")
		}
		for _, cc := range values {
			tag, err := addAsset(ruleSetKindGeoIP, cc)
			if err != nil {
				return err
			}
			*ruleSetTags = append(*ruleSetTags, tag)
		}
	default:
		return fmt.Errorf("unknown match_type %q", r.MatchType)
	}
	return nil
}

// userIdentities returns the sing-box auth_user identities a user presents on
// user-entry inbounds (one per protocol with generated creds). Empty for
// AWG-only users (they are identified by source_ip_cidr instead).
func userIdentities(u model.User) []string {
	var out []string
	if u.VLESSUUID != "" {
		out = append(out, u.VLESSUUID)
	}
	if u.TUICUUID != "" {
		out = append(out, u.TUICUUID)
	}
	if u.Hysteria2Password != "" {
		out = append(out, u.Hysteria2Password)
	}
	return out
}

// ValidateRouteRule dry-runs the expansion of a single rule and returns the
// reasons it would be dropped at deploy time (unknown preset, empty values,
// invalid geo name, scoped users without identity, …). Empty slice = the rule
// will generate traffic rules. Used by the UI for fail-fast input validation.
func ValidateRouteRule(r *model.RouteRule, users []model.User) []string {
	if r == nil {
		return []string{"nil rule"}
	}
	enabled := *r
	enabled.Enabled = true
	ex := ExpandManualRouteRules([]*model.RouteRule{&enabled}, []string{"scope"}, users)
	return ex.Warnings
}

// ruleSetCacheDir is the orchestrator-local cache for downloaded .srs assets
// (<user cache>/angry-box/rule-set). Downloads happen once per asset; the
// deploy pushes the cached file to the node.
func ruleSetCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "angry-box", "rule-set")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// FetchRuleSetAsset downloads (and caches) one .srs asset and returns the
// local path. Cached non-empty files are reused as-is (the orchestrator is
// short-lived per deploy; staleness is acceptable — geo lists drift slowly).
func FetchRuleSetAsset(kind, name string) (string, error) {
	dir, err := ruleSetCacheDir()
	if err != nil {
		return "", fmt.Errorf("rule-set cache dir: %w", err)
	}
	tag := RuleSetTag(kind, name)
	path := filepath.Join(dir, tag+".srs")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		return path, nil
	}
	url := RuleSetAssetURL(kind, name)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}
