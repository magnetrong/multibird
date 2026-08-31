// Package ops orchestrates the v0.1 commands over the lower layers
// (instance store, daemon lifecycle, gRPC control, preflight). cmd/multibird
// stays cobra-only per CLAUDE.md.
package ops

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/magnetrong/multibird/internal/daemon"
	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbcli"
	"github.com/magnetrong/multibird/internal/nbgrpc"
	"github.com/magnetrong/multibird/internal/platform"
)

// Env bundles the process-wide dependencies.
type Env struct {
	Store    *instance.Store
	Platform platform.Platform
	// Warnf receives user-facing warnings (default: stderr).
	Warnf func(format string, args ...any)
	// Printf receives user-facing progress output (default: stdout) — e.g.
	// the SSO "open this URL" prompt.
	Printf func(format string, args ...any)
}

// NewEnv builds the default environment for the current OS/user.
func NewEnv() (*Env, error) {
	p := platform.New()
	root, err := p.ConfigRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("creating config root %s: %w", root, err)
	}
	return &Env{
		Store:    &instance.Store{Root: root, RunDir: p.RunDir()},
		Platform: p,
		Warnf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
		},
		Printf: func(format string, args ...any) {
			fmt.Fprintf(os.Stdout, format+"\n", args...)
		},
	}, nil
}

// Add registers a new instance (does not start it).
func (e *Env) Add(inst *instance.Instance) error {
	if err := instance.ValidateName(inst.Name); err != nil {
		return err
	}
	if _, err := e.Store.Load(inst.Name); err == nil {
		return fmt.Errorf("instance %q already exists — `multibird remove %s` first, or pick another name", inst.Name, inst.Name)
	}
	idx, err := e.Store.NextIndex()
	if err != nil {
		return err
	}
	inst.Index = idx
	return e.Store.Save(inst)
}

// Up brings one instance fully up: spawn daemon, first-time Login (setup key
// or SSO browser flow; this plumbs the isolation params into netbird's
// config.json), engine Up, interface discovery, and preflight (IP-range,
// routed-prefix and DNS-management conflicts against other running
// instances). With strict=true a conflict brings the instance back down and
// errors; otherwise it warns.
func (e *Env) Up(ctx context.Context, inst *instance.Instance, strict bool) error {
	p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
	if err := daemon.Start(inst, p, e.Platform.DaemonEnv()); err != nil {
		return err
	}
	c, err := nbgrpc.Dial(p.SocketPath)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.WaitReady(ctx, 15*time.Second); err != nil {
		return fmt.Errorf("instance %q: %w — check the daemon log: %s", inst.Name, err, p.LogFile)
	}

	if !inst.LoggedIn {
		// Isolation parameters go through SetConfig: as of netbird v0.77,
		// Login ignores every config field except managementUrl/PSK (see
		// nbgrpc.LoginParams). SetConfig must run BEFORE Login/Up so the
		// engine never starts on the defaults (utun100, port 51820 — both
		// owned by a stock netbird install).
		err := c.SetConfig(ctx, nbgrpc.SetConfigParams{
			ManagementURL: inst.ManagementURL,
			InterfaceName: e.Platform.InterfaceHint(inst.Index),
			WireguardPort: p.WGPort,
			DisableDNS:    inst.DisableDNS,
		})
		if err != nil {
			return fmt.Errorf("instance %q: %w", inst.Name, err)
		}
		hostname := instanceHostname(inst.Name)
		ch, err := c.Login(ctx, nbgrpc.LoginParams{
			SetupKey:      inst.SetupKey, // empty for SSO instances
			ManagementURL: inst.ManagementURL,
			Hostname:      hostname,
		})
		if err != nil {
			return fmt.Errorf("instance %q: %w", inst.Name, err)
		}
		if ch != nil {
			if !inst.SSO {
				e.Warnf("instance %q: management server requested SSO login despite the setup key", inst.Name)
			}
			e.Printf("instance %q: complete the login in your browser:\n\n    %s\n\nwaiting for you to finish (Ctrl-C aborts this instance only)...", inst.Name, ch.VerificationURI)
			if err := c.WaitSSOLogin(ctx, ch, hostname); err != nil {
				return fmt.Errorf("instance %q: %w", inst.Name, err)
			}
		}
		inst.LoggedIn = true
		if err := e.Store.Save(inst); err != nil {
			return err
		}
	}

	if err := c.Up(ctx); err != nil {
		return fmt.Errorf("instance %q: %w", inst.Name, err)
	}

	// Discover the ACTUAL interface (macOS assigns utunN itself) and record it.
	st, err := c.Status(ctx)
	if err != nil {
		return fmt.Errorf("instance %q came up but status failed: %w", inst.Name, err)
	}
	if ipStr := st.GetFullStatus().GetLocalPeerState().GetIP(); ipStr != "" {
		if addr, err := hostAddr(ipStr); err == nil {
			if iface, err := e.Platform.DiscoverInterface(addr); err == nil && iface != inst.Interface {
				inst.Interface = iface
				if err := e.Store.Save(inst); err != nil {
					return err
				}
			}
		}
	}

	// Preflight: IP-range, routed-prefix and DNS-management conflicts against
	// the other running instances.
	conflicts, err := e.conflictsInvolving(ctx, inst.Name)
	if err != nil {
		e.Warnf("preflight check skipped: %v", err)
		return nil
	}
	if len(conflicts) > 0 && strict {
		_ = c.Down(ctx)
		return fmt.Errorf("preflight (--strict): %s — instance %q was brought back down", conflicts[0], inst.Name)
	}
	for _, cf := range conflicts {
		e.Warnf("preflight: %s", cf)
	}
	return nil
}

// hostAddr parses the daemon-reported local address ("100.79.230.225/16" or
// bare IP) into the HOST address — unlike preflight.ParseAddr, which masks
// to the network address for overlap checks and is useless for interface
// discovery.
func hostAddr(s string) (netip.Addr, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr(), nil
	}
	return netip.ParseAddr(s)
}

// instanceHostname derives a per-instance peer hostname so two multibird
// instances on the same box never register as the same peer name.
func instanceHostname(name string) string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "multibird-" + name
	}
	return h + "-" + name
}

// Down stops the engine and the daemon for one instance.
func (e *Env) Down(ctx context.Context, inst *instance.Instance) error {
	p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
	if daemon.Running(p) {
		if c, err := nbgrpc.Dial(p.SocketPath); err == nil {
			dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := c.Down(dctx); err != nil {
				e.Warnf("instance %q: graceful engine down failed (%v); stopping daemon anyway", inst.Name, err)
			}
			cancel()
			c.Close() //nolint:gosec // best-effort close of a status probe
		}
	}
	return daemon.Stop(p)
}

// Remove tears the instance down and deletes multibird state (and, with
// purge, netbird's config dir too).
func (e *Env) Remove(ctx context.Context, inst *instance.Instance, purge bool) error {
	if err := e.Down(ctx, inst); err != nil {
		e.Warnf("instance %q: teardown before removal was incomplete: %v — `multibird nuke %s` cleans up leftovers", inst.Name, err, inst.Name)
	}
	p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
	_ = os.Remove(p.SocketPath)
	return e.Store.Remove(inst, purge)
}

// Nuke forcefully cleans a crashed/half-up instance. Idempotent.
func (e *Env) Nuke(inst *instance.Instance) []error {
	return daemon.Nuke(inst.DeriveParams(e.Store.Root, e.Store.RunDir))
}

// InstanceStatus is the per-instance view for `multibird status` (and its
// --json form). The setup key never appears here by construction.
type InstanceStatus struct {
	Name          string `json:"name"`
	ManagementURL string `json:"management_url"`
	State         string `json:"state"` // stopped | daemon-only | Connected | Connecting | ...
	NetbirdIP     string `json:"netbird_ip,omitempty"`
	Peers         int    `json:"peers"`
	Interface     string `json:"interface,omitempty"`
	Version       string `json:"netbird_version,omitempty"`
	WGPort        int    `json:"wireguard_port"`
	LogFile       string `json:"log_file"`
}

// Status collects the aggregated view for the given instances.
func (e *Env) Status(ctx context.Context, insts []*instance.Instance) []InstanceStatus {
	out := make([]InstanceStatus, 0, len(insts))
	for _, inst := range insts {
		p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
		s := InstanceStatus{
			Name: inst.Name, ManagementURL: inst.ManagementURL,
			State: "stopped", Interface: inst.Interface, WGPort: p.WGPort,
			LogFile: p.LogFile,
		}
		if v, err := nbcli.New(inst.NetbirdBin).Version(ctx); err == nil {
			s.Version = v
		}
		if daemon.Running(p) {
			s.State = "daemon-only"
			if c, err := nbgrpc.Dial(p.SocketPath); err == nil {
				sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if st, err := c.Status(sctx); err == nil {
					s.State = st.GetStatus()
					if st.GetDaemonVersion() != "" {
						s.Version = st.GetDaemonVersion()
					}
					fs := st.GetFullStatus()
					s.NetbirdIP = fs.GetLocalPeerState().GetIP()
					s.Peers = len(fs.GetPeers())
					// Backfill interface discovery: `up` may have returned
					// while the engine was still connecting (no IP yet), so
					// the recorded interface can be empty or stale.
					if s.NetbirdIP != "" {
						if addr, err := hostAddr(s.NetbirdIP); err == nil {
							if iface, err := e.Platform.DiscoverInterface(addr); err == nil && iface != inst.Interface {
								inst.Interface = iface
								s.Interface = iface
								if err := e.Store.Save(inst); err != nil {
									e.Warnf("instance %q: recording discovered interface: %v", inst.Name, err)
								}
							}
						}
					}
				}
				cancel()
				c.Close() //nolint:gosec // best-effort close of a status probe
			}
		}
		out = append(out, s)
	}
	return out
}
