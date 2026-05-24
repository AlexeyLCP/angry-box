package xray

import (
	"context"
	"encoding/json"

	"github.com/alexeylcp/lucx-core/internal/backend"
	xraycfg "github.com/alexeylcp/lucx-core/internal/backend/xray/config"
	"github.com/alexeylcp/lucx-core/internal/ssh"
)

// GetConfig reads the current config.json from the server.
func (x *XrayBackend) GetConfig(ctx context.Context, client backend.SSHClient) (*backend.RawConfig, error) {
	sshClient := client.(*ssh.Client)
	mgr := xraycfg.NewManager(sshClient)
	return mgr.Read(ctx)
}

// ApplyConfig applies a batch of LucX config changes to the server.
// Uses config.Manager: backup → merge → atomic write → test → restart → verify.
func (x *XrayBackend) ApplyConfig(
	ctx context.Context,
	client *ssh.Client,
	inbounds, outbounds []json.RawMessage,
	routing []backend.RoutingRule,
) error {
	tm := xraycfg.NewTagManager("") // chain ID not needed — tags already built
	mgr := xraycfg.NewManager(client)
	_, err := mgr.Apply(ctx, tm, inbounds, outbounds, routing, nil)
	return err
}
