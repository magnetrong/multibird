// Package ops orchestrates the v0.1 commands over the lower layers
// (instance store, daemon lifecycle, gRPC control, preflight). cmd/multibird
// stays cobra-only per CLAUDE.md.
package ops

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/OWNER/multibird/internal/daemon"
	"github.com/OWNER/multibird/internal/instance"
	"github.com/OWNER/multibird/internal/nbcli"
	"github.com/OWNER/multibird/internal/nbgrpc"
	"github.com/OWNER/multibird/internal/platform"
	"github.com/OWNER/multibird/internal/preflight"
)

// Env bundles the process-wide dependencies.
type Env struct {
	Store    *instance.Store
	Platform platform.Platform
	// Warnf receives user-facing warnings (default: stderr).
	Warnf func(format string, args ...any)
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

// Up brings one instance fully up: spawn daemon, first-time Login (which
// plumbs the isolation params into netbird's config.json), engine Up,
// interface discovery, and the v0.1 preflight (netbird IP range overlap
// against other running instances). With strict=true an overlap brings the
// instance back down and errors; otherwise it warns.
func (e *Env) Up(ctx context.Context, inst *instance.Instance, strict bool) error {
	p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
	if err := daemon.Start(inst, p); err != nil {
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
		if inst.SSO {
			return fmt.Errorf("instance %q is configured for SSO, which multibird v0.1 does not support — recreate it with a setup key (`multibird remove %s && multibird add %s --management-url %s --setup-key ...`)",
				inst.Name, inst.Name, inst.Name, inst.ManagementURL)
		}
		err := c.Login(ctx, nbgrpc.LoginParams{
			SetupKey:      inst.SetupKey,
			ManagementURL: inst.ManagementURL,
			InterfaceName: e.Platform.InterfaceHint(inst.Index),
			WireguardPort: p.WGPort,
			DisableDNS:    inst.DisableDNS,
		})
		if err != nil {
			return fmt.Errorf("instance %q: %w", inst.Name, err)
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
		if n, err := preflight.ParseAddr(inst.Name, ipStr); err == nil {
			if iface, err := e.Platform.DiscoverInterface(n.Prefix.Addr()); err == nil && iface != inst.Interface {
				inst.Interface = iface
				if err := e.Store.Save(inst); err != nil {
					return err
				}
			}
		}
	}

	// v0.1 preflight: netbird IP range overlap across running instances.
	conflicts, err := e.ipRangeConflicts(ctx)
	if err != nil {
		e.Warnf("preflight check skipped: %v", err)
		return nil
	}
	for _, cf := range conflicts {
		if cf.A.Instance == inst.Name || cf.B.Instance == inst.Name {
			if strict {
				_ = c.Down(ctx)
				return fmt.Errorf("preflight (--strict): %s — instance %q was brought back down", cf, inst.Name)
			}
			e.Warnf("preflight: %s", cf)
		}
	}
	return nil
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
			c.Close()
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
				}
				cancel()
				c.Close()
			}
		}
		out = append(out, s)
	}
	return out
}

// ipRangeConflicts gathers netbird IP ranges from every RUNNING instance and
// reports overlaps.
func (e *Env) ipRangeConflicts(ctx context.Context) ([]preflight.Conflict, error) {
	insts, err := e.Store.List()
	if err != nil {
		return nil, err
	}
	var nets []preflight.Net
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
		cancel()
		c.Close()
		if err != nil {
			continue
		}
		ipStr := st.GetFullStatus().GetLocalPeerState().GetIP()
		if ipStr == "" {
			continue
		}
		n, err := preflight.ParseAddr(inst.Name, ipStr)
		if err != nil {
			e.Warnf("%v", err)
			continue
		}
		nets = append(nets, n)
	}
	return preflight.IPRangeConflicts(nets), nil
}
