package ops

import (
	"context"
	"fmt"
	"time"

	"github.com/magnetrong/multibird/internal/daemon"
	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbgrpc"
)

// SetChanges are the mutable per-instance settings for `multibird set`.
// Nil pointer = leave unchanged.
type SetChanges struct {
	DisableDNS    *bool
	WireguardPort *int
	NetbirdBin    *string
}

// Set updates instance settings. Settings that live in netbird's config.json
// (DNS toggle, WireGuard port) are pushed via the SetConfig gRPC — which,
// unlike a re-Login, does NOT consume setup-key usage. That requires the
// daemon: if it isn't running, Set starts it just for the update and stops it
// again (root needed, same as up). Changes to a running engine apply on the
// next down/up.
func (e *Env) Set(ctx context.Context, inst *instance.Instance, ch SetChanges) error {
	if ch.NetbirdBin != nil {
		inst.NetbirdBin = *ch.NetbirdBin
	}
	needsDaemon := false
	if ch.DisableDNS != nil && *ch.DisableDNS != inst.DisableDNS {
		inst.DisableDNS = *ch.DisableDNS
		needsDaemon = true
	}
	if ch.WireguardPort != nil && *ch.WireguardPort != inst.WireguardPort {
		inst.WireguardPort = *ch.WireguardPort
		needsDaemon = true
	}

	// Only push to the daemon if it has a persisted login to update.
	if needsDaemon && inst.LoggedIn {
		p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
		wasRunning := daemon.Running(p)
		if !wasRunning {
			if err := daemon.Start(inst, p, e.Platform.DaemonEnv()); err != nil {
				return fmt.Errorf("applying settings needs the instance's daemon: %w", err)
			}
		}
		c, err := nbgrpc.Dial(p.SocketPath)
		if err != nil {
			return err
		}
		defer c.Close()
		if err := c.WaitReady(ctx, 15*time.Second); err != nil {
			return fmt.Errorf("instance %q: %w", inst.Name, err)
		}
		err = c.SetConfig(ctx, nbgrpc.SetConfigParams{
			ManagementURL: inst.ManagementURL,
			InterfaceName: e.Platform.InterfaceHint(inst.Index),
			WireguardPort: p.WGPort,
			DisableDNS:    inst.DisableDNS,
		})
		if err != nil {
			return fmt.Errorf("instance %q: %w", inst.Name, err)
		}
		if !wasRunning {
			if err := daemon.Stop(p); err != nil {
				e.Warnf("instance %q: stopping the temporary daemon failed: %v", inst.Name, err)
			}
		} else {
			e.Printf("instance %q: settings saved — they take effect on the next `multibird down %s && sudo multibird up %s`", inst.Name, inst.Name, inst.Name)
		}
	}
	return e.Store.Save(inst)
}
