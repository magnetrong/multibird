package ops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/magnetrong/multibird/internal/daemon"
	"github.com/magnetrong/multibird/internal/hostdns"
	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbgrpc"

	"github.com/netbirdio/netbird/client/proto"
)

// customDNSAddress is the customDNSAddress value for an instance's mode:
// the fixed listener in multibird mode, "" (→ "empty", clear) otherwise.
// Every SetConfig call MUST go through this — see nbgrpc.SetConfigParams.
func customDNSAddress(inst *instance.Instance, p instance.Params) string {
	if inst.DNSMode == instance.DNSMultibird {
		return p.DNSListen.String()
	}
	return ""
}

// applyHostDNSFromStatus registers/refreshes the instance's scoped resolvers
// from a live status. Skips silently when the engine has no IP yet (the next
// status/sync backfills, like interface discovery).
func (e *Env) applyHostDNSFromStatus(inst *instance.Instance, st *proto.StatusResponse) error {
	if inst.DNSMode != instance.DNSMultibird {
		return nil
	}
	if st.GetFullStatus().GetLocalPeerState().GetIP() == "" {
		return nil // engine still connecting
	}
	p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
	spec, err := hostdns.Derive(st, p.DNSListen)
	if err != nil {
		if errors.Is(err, hostdns.ErrPrimaryClaim) {
			return fmt.Errorf("instance %q: %w", inst.Name, err)
		}
		return fmt.Errorf("instance %q: deriving DNS spec: %w", inst.Name, err)
	}
	if err := e.Platform.ApplyHostDNS(inst.Name, spec); err != nil {
		return fmt.Errorf("instance %q: %w", inst.Name, err)
	}
	return nil
}

// DNSSync re-derives and re-applies one instance's host DNS registration.
// For non-multibird modes it removes any stale registration instead.
// Idempotent; safe to run any time.
func (e *Env) DNSSync(ctx context.Context, inst *instance.Instance) error {
	p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
	if inst.DNSMode != instance.DNSMultibird || !daemon.Running(p) {
		if err := e.Platform.RemoveHostDNS(inst.Name); err != nil {
			return fmt.Errorf("instance %q: removing host DNS: %w", inst.Name, err)
		}
		return nil
	}
	c, err := nbgrpc.Dial(p.SocketPath)
	if err != nil {
		return err
	}
	defer c.Close()
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := c.Status(sctx)
	if err != nil {
		return fmt.Errorf("instance %q: %w", inst.Name, err)
	}
	return e.applyHostDNSFromStatus(inst, st)
}

// removeHostDNSQuietly is the best-effort teardown used by down/remove/nuke.
func (e *Env) removeHostDNSQuietly(name string) {
	if err := e.Platform.RemoveHostDNS(name); err != nil {
		e.Warnf("instance %q: could not remove host DNS registration: %v — run `sudo multibird dns sync %s`", name, err, name)
	}
}
