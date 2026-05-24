package xray

import (
	"context"
	"encoding/json"

	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/ssh"
)

type XrayBackend struct{}

func init() {
	backend.Register(backend.BackendXray, func() backend.ProxyBackend {
		return &XrayBackend{}
	})
}

func (x *XrayBackend) Type() backend.BackendType { return backend.BackendXray }

// Individual Add/Remove methods delegate to ApplyConfig with single entries.
// Chain Engine uses ApplyConfig directly for per-server batching.

func (x *XrayBackend) AddInbound(ctx context.Context, ssh backend.SSHClient, spec backend.InboundSpec) (backend.InboundResult, error) {
	inb, _ := json.Marshal(map[string]interface{}{
		"tag": spec.Tag, "protocol": spec.Protocol, "port": spec.Port,
		"listen": spec.Listen, "settings": json.RawMessage(spec.Settings),
		"streamSettings": json.RawMessage(spec.Stream),
	})
	cfg, _ := x.GetConfig(ctx, ssh)
	if err := x.ApplyConfig(ctx, toSSH(ssh),appendLucXInbounds(cfg.Inbounds, inb), cfg.Outbounds, nil); err != nil {
		return backend.InboundResult{}, err
	}
	return backend.InboundResult{Tag: spec.Tag, Port: spec.Port}, nil
}

func (x *XrayBackend) RemoveInbound(ctx context.Context, ssh backend.SSHClient, tag string) error {
	cfg, _ := x.GetConfig(ctx, ssh)
	var filtered []json.RawMessage
	for _, raw := range cfg.Inbounds {
		var item struct{ Tag string `json:"tag"` }
		if json.Unmarshal(raw, &item) == nil && item.Tag == tag {
			continue
		}
		filtered = append(filtered, raw)
	}
	return x.ApplyConfig(ctx, toSSH(ssh),filtered, cfg.Outbounds, nil)
}

func (x *XrayBackend) AddOutbound(ctx context.Context, ssh backend.SSHClient, spec backend.OutboundSpec) (backend.OutboundResult, error) {
	outb, _ := json.Marshal(map[string]interface{}{
		"tag": spec.Tag, "protocol": spec.Protocol,
		"settings": json.RawMessage(spec.Settings),
		"streamSettings": json.RawMessage(spec.Stream),
	})
	cfg, _ := x.GetConfig(ctx, ssh)
	newOutbounds := append(cfg.Outbounds, outb)
	if err := x.ApplyConfig(ctx, toSSH(ssh),cfg.Inbounds, newOutbounds, nil); err != nil {
		return backend.OutboundResult{}, err
	}
	return backend.OutboundResult{Tag: spec.Tag}, nil
}

func (x *XrayBackend) RemoveOutbound(ctx context.Context, ssh backend.SSHClient, tag string) error {
	cfg, _ := x.GetConfig(ctx, ssh)
	var filtered []json.RawMessage
	for _, raw := range cfg.Outbounds {
		var item struct{ Tag string `json:"tag"` }
		if json.Unmarshal(raw, &item) == nil && item.Tag == tag {
			continue
		}
		filtered = append(filtered, raw)
	}
	return x.ApplyConfig(ctx, toSSH(ssh),cfg.Inbounds, filtered, nil)
}

func (x *XrayBackend) SetRouting(ctx context.Context, ssh backend.SSHClient, rules []backend.RoutingRule) error {
	cfg, _ := x.GetConfig(ctx, ssh)
	return x.ApplyConfig(ctx, toSSH(ssh),cfg.Inbounds, cfg.Outbounds, rules)
}

func appendLucXInbounds(existing []json.RawMessage, newInbound json.RawMessage) []json.RawMessage {
	return append(existing, newInbound)
}

func toSSH(sshClient backend.SSHClient) *ssh.Client { return sshClient.(*ssh.Client) }
