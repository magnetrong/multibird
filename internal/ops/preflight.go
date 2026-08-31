package ops

import (
	"context"
	"net/netip"
	"time"

	"github.com/magnetrong/multibird/internal/daemon"
	"github.com/magnetrong/multibird/internal/nbgrpc"
	"github.com/magnetrong/multibird/internal/preflight"
)

// runningSnapshot is what preflight needs from one running instance.
type runningSnapshot struct {
	name      string
	iface     string // recorded actual interface
	net       *preflight.Net
	routes    []preflight.Route
	dnsIntent preflight.DNSIntent
}

// snapshotRunning gathers state from every RUNNING instance's daemon.
// Unreachable daemons are skipped (a daemon mid-crash shouldn't block `up`).
func (e *Env) snapshotRunning(ctx context.Context) ([]runningSnapshot, error) {
	insts, err := e.Store.List()
	if err != nil {
		return nil, err
	}
	var out []runningSnapshot
	for _, inst := range insts {
		p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
		if !daemon.Running(p) {
			continue
		}
		c, err := nbgrpc.Dial(p.SocketPath)
		if err != nil {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		st, err := c.Status(sctx)
		if err != nil {
			cancel()
			c.Close()
			continue
		}
		snap := runningSnapshot{
			name:  inst.Name,
			iface: inst.Interface,
			dnsIntent: preflight.DNSIntent{
				Instance:   inst.Name,
				ManagesDNS: !inst.DisableDNS,
				Domains:    nbgrpc.DNSDomains(st),
			},
		}
		if ip := st.GetFullStatus().GetLocalPeerState().GetIP(); ip != "" {
			if n, err := preflight.ParseAddr(inst.Name, ip); err == nil {
				snap.net = &n
			} else {
				e.Warnf("%v", err)
			}
		}
		if nets, err := c.Networks(sctx); err == nil {
			for _, nw := range nets {
				pfx, perr := netip.ParsePrefix(nw.Range)
				if perr != nil {
					continue // DNS-routed networks have no CIDR range
				}
				snap.routes = append(snap.routes, preflight.Route{
					Instance: inst.Name, Prefix: pfx.Masked(), Selected: nw.Selected,
				})
			}
		} else {
			e.Warnf("instance %q: %v (route-overlap preflight incomplete)", inst.Name, err)
		}
		cancel()
		c.Close()
		out = append(out, snap)
	}
	return out, nil
}

// conflictsInvolving runs all preflight checks across running instances and
// returns the human-readable conflicts that involve the named instance
// (empty name = all conflicts, used by doctor).
func (e *Env) conflictsInvolving(ctx context.Context, name string) ([]string, error) {
	snaps, err := e.snapshotRunning(ctx)
	if err != nil {
		return nil, err
	}
	involves := func(insts ...string) bool {
		if name == "" {
			return true
		}
		for _, i := range insts {
			if i == name {
				return true
			}
		}
		return false
	}

	var out []string

	var nets []preflight.Net
	var routes []preflight.Route
	var intents []preflight.DNSIntent
	ifaceOwner := map[string]string{} // interface -> instance
	for _, s := range snaps {
		if s.net != nil {
			nets = append(nets, *s.net)
		}
		routes = append(routes, s.routes...)
		intents = append(intents, s.dnsIntent)
		if s.iface != "" {
			ifaceOwner[s.iface] = s.name
		}
	}

	for _, c := range preflight.IPRangeConflicts(nets) {
		if involves(c.A.Instance, c.B.Instance) {
			out = append(out, c.String())
		}
	}

	for _, c := range preflight.RouteConflicts(routes) {
		if !involves(c.A.Instance, c.B.Instance) {
			continue
		}
		// Ask the OS routing table who actually wins for the narrower prefix.
		probe := c.A.Prefix
		if c.B.Prefix.Bits() > probe.Bits() {
			probe = c.B.Prefix
		}
		if iface, err := e.Platform.RouteInterface(probe.Addr()); err == nil {
			c.Winner = ifaceOwner[iface]
		}
		out = append(out, c.String())
	}

	for _, c := range preflight.DNSConflicts(intents) {
		if involves(c.Instances...) {
			out = append(out, c.String())
		}
	}
	return out, nil
}
