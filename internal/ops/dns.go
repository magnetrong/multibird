package ops

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/magnetrong/multibird/internal/daemon"
	"github.com/magnetrong/multibird/internal/hostdns"
	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbgrpc"

	"github.com/netbirdio/netbird/client/proto"
)

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
	spec, err := hostdns.Derive(st)
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

// DNSCleanupStrays removes host DNS registrations owned by instance names
// that no longer exist (crashed teardown, removed instances). Returns the
// names cleaned.
func (e *Env) DNSCleanupStrays() ([]string, error) {
	owners, err := e.Platform.ListHostDNSOwners()
	if err != nil {
		return nil, fmt.Errorf("listing host DNS registrations: %w", err)
	}
	insts, err := e.List()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, i := range insts {
		known[i.Name] = true
	}
	var cleaned []string
	for _, o := range owners {
		if known[o] {
			continue
		}
		if err := e.Platform.RemoveHostDNS(o); err != nil {
			return cleaned, fmt.Errorf("removing stray host DNS of %q: %w", o, err)
		}
		cleaned = append(cleaned, o)
	}
	return cleaned, nil
}

// removeHostDNSQuietly is the best-effort teardown used by down/remove/nuke.
func (e *Env) removeHostDNSQuietly(name string) {
	if err := e.Platform.RemoveHostDNS(name); err != nil {
		e.Warnf("instance %q: could not remove host DNS registration: %v — run `sudo multibird dns sync %s`", name, err, name)
	}
}

// DNSWatch keeps the host DNS registrations of the given instances in sync
// until ctx is canceled: it subscribes to each multibird-mode daemon's event
// stream and re-applies on NETWORK/DNS events (debounced), polls status every
// 60s as a fallback, survives daemon restarts (reconnect loop), and removes
// an instance's keys while its daemon is down.
func (e *Env) DNSWatch(ctx context.Context, insts []*instance.Instance) {
	var wg sync.WaitGroup
	for _, inst := range insts {
		if inst.DNSMode != instance.DNSMultibird {
			continue
		}
		wg.Add(1)
		go func(inst *instance.Instance) {
			defer wg.Done()
			e.watchOne(ctx, inst)
		}(inst)
	}
	wg.Wait()
}

const (
	watchDebounce  = 2 * time.Second
	watchPoll      = 60 * time.Second
	watchReconnect = 10 * time.Second
)

func (e *Env) watchOne(ctx context.Context, inst *instance.Instance) {
	p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
	downCleaned := false
	for ctx.Err() == nil {
		if !daemon.Running(p) {
			if !downCleaned {
				e.removeHostDNSQuietly(inst.Name)
				downCleaned = true
			}
			sleepCtx(ctx, watchReconnect)
			continue
		}
		downCleaned = false
		if err := e.watchConnected(ctx, inst, p); err != nil && ctx.Err() == nil {
			e.Warnf("instance %q: dns watch: %v — reconnecting", inst.Name, err)
			sleepCtx(ctx, watchReconnect)
		}
	}
}

// watchConnected holds one connection to the daemon: initial sync, event
// stream with debounce, periodic fallback poll. Returns when the stream
// breaks (daemon restart) or ctx ends.
func (e *Env) watchConnected(ctx context.Context, inst *instance.Instance, p instance.Params) error {
	c, err := nbgrpc.Dial(p.SocketPath)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := e.DNSSync(ctx, inst); err != nil {
		e.Warnf("%v", err)
	}

	stream, err := c.Events(ctx)
	if err != nil {
		return err
	}
	events := make(chan proto.SystemEvent_Category, 8)
	errc := make(chan error, 1)
	go func() {
		for {
			ev, err := stream.Recv()
			if err != nil {
				errc <- err
				return
			}
			select {
			case events <- ev.GetCategory():
			default: // debounce pending anyway; drop bursts
			}
		}
	}()

	var debounce <-chan time.Time
	poll := time.NewTicker(watchPoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errc:
			return err
		case cat := <-events:
			if cat == proto.SystemEvent_NETWORK || cat == proto.SystemEvent_DNS {
				debounce = time.After(watchDebounce)
			}
		case <-debounce:
			debounce = nil
			if err := e.DNSSync(ctx, inst); err != nil {
				e.Warnf("%v", err)
			}
		case <-poll.C:
			if err := e.DNSSync(ctx, inst); err != nil {
				e.Warnf("%v", err)
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
