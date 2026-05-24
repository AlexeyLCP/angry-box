package config

import (
	"encoding/json"
	"strings"

	"github.com/alexeylcp/lucx-core/internal/backend"
)

// MergeResult holds the merged config sections.
type MergeResult struct {
	Inbounds  []json.RawMessage `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
	Routing   json.RawMessage   `json:"routing"`
}

// Merge combines existing config with new LucX entries.
// Existing non-LucX entries are preserved. Old LucX entries are replaced.
func Merge(existing *backend.RawConfig, newInbounds, newOutbounds []json.RawMessage, newRules []backend.RoutingRule) *MergeResult {
	return &MergeResult{
		Inbounds:  mergeInbounds(existing.Inbounds, newInbounds),
		Outbounds: mergeOutbounds(existing.Outbounds, newOutbounds),
		Routing:   mergeRouting(existing.Routing, newRules),
	}
}

// mergeInbounds keeps non-LucX inbounds, replaces LucX inbounds.
func mergeInbounds(existing, newLucX []json.RawMessage) []json.RawMessage {
	var result []json.RawMessage
	for _, raw := range existing {
		var item struct{ Tag string `json:"tag"` }
		if json.Unmarshal(raw, &item) == nil && IsLucX(item.Tag) {
			continue // remove old LucX
		}
		result = append(result, raw) // keep user's
	}
	result = append(result, newLucX...) // add new LucX
	return result
}

// mergeOutbounds keeps non-LucX outbounds, replaces LucX outbounds.
func mergeOutbounds(existing, newLucX []json.RawMessage) []json.RawMessage {
	var result []json.RawMessage
	for _, raw := range existing {
		var item struct{ Tag string `json:"tag"` }
		if json.Unmarshal(raw, &item) == nil && IsLucX(item.Tag) {
			continue
		}
		result = append(result, raw)
	}
	result = append(result, newLucX...)
	return result
}

// mergeRouting keeps non-LucX routing rules, replaces LucX rules.
func mergeRouting(existing json.RawMessage, lucxRules []backend.RoutingRule) json.RawMessage {
	var current struct {
		DomainStrategy string            `json:"domainStrategy"`
		Rules          []json.RawMessage `json:"rules"`
	}
	json.Unmarshal(existing, &current)
	if current.DomainStrategy == "" {
		current.DomainStrategy = "AsIs"
	}

	var resultRules []json.RawMessage
	for _, raw := range current.Rules {
		var rule struct {
			InboundTag  []string `json:"inboundTag"`
			OutboundTag string   `json:"outboundTag"`
		}
		if json.Unmarshal(raw, &rule) != nil {
			resultRules = append(resultRules, raw)
			continue
		}
		if isLucXRule(rule) {
			continue // remove old LucX rule
		}
		resultRules = append(resultRules, raw) // keep user's
	}

	for _, lr := range lucxRules {
		b, _ := json.Marshal(lr)
		resultRules = append(resultRules, b)
	}

	merged, _ := json.Marshal(map[string]interface{}{
		"domainStrategy": current.DomainStrategy,
		"rules":          resultRules,
	})
	return merged
}

// isLucXRule checks if a routing rule belongs to LucX.
func isLucXRule(rule struct {
	InboundTag  []string `json:"inboundTag"`
	OutboundTag string   `json:"outboundTag"`
}) bool {
	if strings.HasPrefix(rule.OutboundTag, lucxPrefix) {
		return true
	}
	for _, tag := range rule.InboundTag {
		if strings.HasPrefix(tag, lucxPrefix) {
			return true
		}
	}
	return false
}
