// Command multibird runs multiple NetBird VPN instances simultaneously.
// This package is cobra wiring only — orchestration lives in internal/ops.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/ops"
	"github.com/magnetrong/multibird/internal/persist"
	"github.com/magnetrong/multibird/internal/selfupdate"
	"github.com/magnetrong/multibird/internal/tui"
	"github.com/magnetrong/multibird/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func env() (*ops.Env, error) { return ops.NewEnv() }

// forEach resolves name-or---all into the target instances.
func targets(e *ops.Env, args []string, all bool) ([]*instance.Instance, error) {
	if all {
		list, err := e.List()
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, errors.New("no instances configured — `multibird add <name> --management-url ... --setup-key ...` first")
		}
		return list, nil
	}
	if len(args) != 1 {
		return nil, errors.New("specify an instance name or --all")
	}
	i, err := e.Load(args[0])
	if err != nil {
		return nil, err
	}
	return []*instance.Instance{i}, nil
}

// instancesFor resolves the optional name argument of a read-only command
// into its targets: the named instance, or all of them. Unlike targets() it
// needs no --all flag and tolerates having none configured.
func instancesFor(e *ops.Env, args []string) ([]*instance.Instance, error) {
	if len(args) == 1 {
		i, err := e.Load(args[0])
		if err != nil {
			return nil, err
		}
		return []*instance.Instance{i}, nil
	}
	return e.List()
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "multibird",
		Short:         "Run multiple NetBird VPN instances simultaneously",
		Long:          "multibird runs multiple isolated NetBird daemons side by side —\none per mesh — until NetBird ships native simultaneous profiles\n(netbirdio/netbird#446), at which point you should migrate back and\ndelete multibird.",
		Version:       version.Full(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(addCmd(), upCmd(), downCmd(), statusCmd(), peersCmd(), listCmd(),
		removeCmd(), doctorCmd(), nukeCmd(), setCmd(), logsCmd(),
		installCmd(), uninstallCmd(), upgradeCmd(), dnsCmd(), tuiCmd())
	return root
}

// completeInstanceNames offers configured instance names for shell completion.
func completeInstanceNames(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	e, err := env()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	insts, err := e.List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, i := range insts {
		names = append(names, i.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func addCmd() *cobra.Command {
	var mgmtURL, setupKey, nbBin, dnsMode string
	var sso, disableDNS bool
	var wgPort int
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a new instance (does not start it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if (setupKey == "") == !sso {
				return errors.New("exactly one of --setup-key or --sso is required")
			}
			e, err := env()
			if err != nil {
				return err
			}
			inst := &instance.Instance{
				Name: args[0], ManagementURL: mgmtURL, SetupKey: setupKey,
				SSO: sso, NetbirdBin: nbBin, WireguardPort: wgPort,
			}
			if disableDNS {
				dnsMode = string(instance.DNSDisabled)
			}
			if dnsMode != "" {
				m, err := instance.ParseDNSMode(dnsMode)
				if err != nil {
					return err
				}
				inst.DNSMode = m
			}
			if err := e.Add(inst); err != nil {
				return err
			}
			fmt.Printf("added %s — bring it up with: sudo multibird up %s\n", inst, inst.Name)
			return nil
		},
	}
	c.Flags().StringVar(&mgmtURL, "management-url", "", "NetBird management server URL (required)")
	c.Flags().StringVar(&setupKey, "setup-key", "", "setup key for this mesh (stored 0600, never logged)")
	c.Flags().BoolVar(&sso, "sso", false, "use SSO login instead of a setup key (browser flow runs on first `up`)")
	c.Flags().StringVar(&nbBin, "netbird-bin", "", "pin this instance to a specific netbird binary")
	c.Flags().IntVar(&wgPort, "wireguard-port", 0, "override the derived WireGuard listen port")
	c.Flags().StringVar(&dnsMode, "dns-mode", "", "who manages host DNS: native, multibird or disabled (default: per-OS, see docs/dns.md)")
	c.Flags().BoolVar(&disableDNS, "disable-dns", false, "deprecated alias for --dns-mode disabled")
	_ = c.Flags().MarkDeprecated("disable-dns", "use --dns-mode disabled")
	_ = c.MarkFlagRequired("management-url")
	return c
}

func upCmd() *cobra.Command {
	var all, strict bool
	c := &cobra.Command{
		Use:   "up [name]",
		Short: "Bring instance(s) up (spawns the daemon; needs root)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			insts, err := targets(e, args, all)
			if err != nil {
				return err
			}
			// With --all, one failing instance (e.g. pending SSO) must not
			// block the others.
			var failed []string
			for _, i := range insts {
				if err := e.Up(cmd.Context(), i, strict); err != nil {
					if !all {
						return err
					}
					failed = append(failed, i.Name)
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					continue
				}
				fmt.Printf("%s: up\n", i.Name)
			}
			if len(failed) > 0 {
				return fmt.Errorf("%d instance(s) failed to come up: %s (others were brought up)", len(failed), strings.Join(failed, ", "))
			}
			return nil
		},
		ValidArgsFunction: completeInstanceNames,
	}
	c.Flags().BoolVar(&all, "all", false, "bring all instances up")
	c.Flags().BoolVar(&strict, "strict", false, "treat preflight conflicts as fatal (instance is brought back down)")
	return c
}

func downCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:               "down [name]",
		Short:             "Bring instance(s) down and stop their daemons",
		ValidArgsFunction: completeInstanceNames,
		Args:              cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			insts, err := targets(e, args, all)
			if err != nil {
				return err
			}
			for _, i := range insts {
				if err := e.Down(cmd.Context(), i); err != nil {
					return err
				}
				fmt.Printf("%s: down\n", i.Name)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "bring all instances down")
	return c
}

func statusCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:               "status [name]",
		Short:             "Show aggregated status across instances",
		ValidArgsFunction: completeInstanceNames,
		Args:              cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			insts, err := instancesFor(e, args)
			if err != nil {
				return err
			}
			sts := e.Status(cmd.Context(), insts)
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(sts)
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATE\tNETBIRD IP\tPEERS\tIFACE\tMGMT\tVERSION")
			for _, s := range sts {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
					s.Name, s.State, s.NetbirdIP, s.Peers, s.Interface, s.ManagementURL, s.Version)
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return c
}

func peersCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:               "peers [name]",
		Short:             "List each mesh's peers and their mesh addresses",
		Long:              "List the peers of one instance, or of every instance when no name is given,\nso you don't have to open a management dashboard to find a peer's address.",
		ValidArgsFunction: completeInstanceNames,
		Args:              cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			insts, err := instancesFor(e, args)
			if err != nil {
				return err
			}
			if len(insts) == 0 {
				fmt.Println("no instances — `multibird add <name> --management-url ... --setup-key ...`")
				return nil
			}
			groups := e.Peers(cmd.Context(), insts)
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(groups)
			}
			var total int
			for _, g := range groups {
				total += len(g.Peers)
			}
			if total > 0 {
				w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
				fmt.Fprintln(w, "INSTANCE\tPEER\tMESH IP\tSTATUS\tLAST HANDSHAKE")
				for _, g := range groups {
					for _, p := range g.Peers {
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
							g.Name, p.Name, p.IP, p.Status, handshakeAgo(p.LastHandshake))
					}
				}
				if err := w.Flush(); err != nil {
					return err
				}
			}
			// Say why an instance contributed nothing, rather than leaving a
			// silent gap in the table.
			for _, g := range groups {
				if len(g.Peers) == 0 {
					hint := ""
					if g.State == "stopped" {
						hint = fmt.Sprintf(" — `sudo multibird up %s`", g.Name)
					}
					fmt.Printf("%s: no peers (%s)%s\n", g.Name, g.State, hint)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return c
}

// handshakeAgo renders a peer's last WireGuard handshake as an age.
func handshakeAgo(t *time.Time) string {
	if t == nil {
		return "-"
	}
	d := time.Since(*t).Round(time.Second)
	if d < 0 {
		d = 0 // clock skew between us and the daemon
	}
	return d.String() + " ago"
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured instances",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			insts, err := e.List()
			if err != nil {
				return err
			}
			if len(insts) == 0 {
				fmt.Println("no instances — `multibird add <name> --management-url ... --setup-key ...`")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tMGMT\tINDEX\tWG PORT\tBIN\tDNS")
			for _, i := range insts {
				p := i.DeriveParams(e.Store.Root, e.Store.RunDir)
				bin := i.NetbirdBin
				if bin == "" {
					bin = "(PATH)"
				}
				dns := string(i.DNSMode)
				fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\t%s\n", i.Name, i.ManagementURL, i.Index, p.WGPort, bin, dns)
			}
			return w.Flush()
		},
	}
}

func removeCmd() *cobra.Command {
	var purge bool
	c := &cobra.Command{
		Use:               "remove <name>",
		Short:             "Tear down an instance and delete multibird's state for it",
		ValidArgsFunction: completeInstanceNames,
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			i, err := e.Load(args[0])
			if err != nil {
				return err
			}
			if err := e.Remove(cmd.Context(), i, purge); err != nil {
				return err
			}
			if purge {
				fmt.Printf("%s: removed (netbird config purged)\n", i.Name)
			} else {
				fmt.Printf("%s: removed (netbird config.json kept; --purge deletes it)\n", i.Name)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&purge, "purge", false, "also delete the instance's netbird config dir")
	return c
}

func doctorCmd() *cobra.Command {
	var strict, jsonOut bool
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Check the environment: netbird binaries, tested versions, leftover state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			checks := e.Doctor(cmd.Context())
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(checks); err != nil {
					return err
				}
			}
			fails, warns := 0, 0
			for _, ch := range checks {
				mark := "ok  "
				switch {
				case !ch.OK:
					mark = "FAIL"
					fails++
				case ch.Warn:
					mark = "warn"
					warns++
				}
				if !jsonOut {
					fmt.Printf("[%s] %s: %s\n", mark, ch.Name, ch.Detail)
				}
			}
			if fails > 0 {
				return fmt.Errorf("%d check(s) failed", fails)
			}
			if strict && warns > 0 {
				return fmt.Errorf("%d warning(s) treated as fatal (--strict)", warns)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&strict, "strict", false, "warnings are fatal")
	c.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return c
}

func nukeCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "nuke <name>",
		Short:             "Forceful cleanup of a crashed/half-up instance (idempotent)",
		ValidArgsFunction: completeInstanceNames,
		Args:              cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			i, err := e.Load(args[0])
			if err != nil {
				return err
			}
			errs := e.Nuke(i)
			for _, ne := range errs {
				fmt.Fprintln(os.Stderr, "nuke:", ne)
			}
			if len(errs) > 0 {
				return fmt.Errorf("%d cleanup step(s) failed — fix the above (often: re-run with sudo) and run nuke again; it is safe to repeat", len(errs))
			}
			fmt.Printf("%s: nuked (safe to run again)\n", i.Name)
			return nil
		},
	}
}

func installCmd() *cobra.Command {
	var dnsWatch bool
	c := &cobra.Command{
		Use:               "install <name>",
		Short:             "Install a boot unit (systemd/launchd, system-level) for an instance",
		Long:              "Generates and enables a root-level boot unit that runs `multibird up <name>`\nat boot — via multibird, never netbird directly, so preflight always runs.\nRequires sudo.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			i, err := e.Load(args[0])
			if err != nil {
				return err
			}
			bin, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locating the multibird binary: %w", err)
			}
			path, err := persist.Install(i.Name, bin, e.Store.Root)
			if err != nil {
				return err
			}
			fmt.Printf("%s: boot unit installed (%s) — instance comes up at boot; `multibird uninstall %s` reverses this\n", i.Name, path, i.Name)
			if dnsWatch {
				if i.DNSMode != instance.DNSMultibird {
					return fmt.Errorf("--dns-watch only makes sense for dns_mode multibird — instance %q is %q (change it with `multibird set %s --dns-mode multibird`)", i.Name, i.DNSMode, i.Name)
				}
				wpath, err := persist.InstallDNSWatch(i.Name, bin, e.Store.Root)
				if err != nil {
					return err
				}
				fmt.Printf("%s: dns-watch unit installed (%s) — host DNS stays in sync with daemon events\n", i.Name, wpath)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&dnsWatch, "dns-watch", false, "also install a KeepAlive unit running `multibird dns sync <name> --watch` (multibird DNS mode)")
	return c
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "uninstall <name>",
		Short:             "Remove an instance's boot unit",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := persist.UninstallDNSWatch(args[0]); err != nil {
				return err
			}
			if err := persist.Uninstall(args[0]); err != nil {
				return err
			}
			fmt.Printf("%s: boot unit removed (dns-watch unit too, if present)\n", args[0])
			return nil
		},
	}
}

func setCmd() *cobra.Command {
	c := &cobra.Command{
		Use:               "set <name>",
		Short:             "Change instance settings (applied without re-consuming the setup key)",
		Long:              "Changes per-instance settings. DNS and port changes are pushed to the\ninstance's daemon via SetConfig (starting it briefly if needed — sudo) and\ntake effect on the next down/up.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			i, err := e.Load(args[0])
			if err != nil {
				return err
			}
			var ch ops.SetChanges
			if cmd.Flags().Changed("dns-mode") {
				v, _ := cmd.Flags().GetString("dns-mode")
				m, err := instance.ParseDNSMode(v)
				if err != nil {
					return err
				}
				ch.DNSMode = &m
			}
			if cmd.Flags().Changed("disable-dns") {
				if ch.DNSMode != nil {
					return errors.New("pass either --dns-mode or the deprecated --disable-dns, not both")
				}
				m := instance.DNSNative
				if v, _ := cmd.Flags().GetBool("disable-dns"); v {
					m = instance.DNSDisabled
				}
				ch.DNSMode = &m
			}
			if cmd.Flags().Changed("wireguard-port") {
				v, _ := cmd.Flags().GetInt("wireguard-port")
				ch.WireguardPort = &v
			}
			if cmd.Flags().Changed("netbird-bin") {
				v, _ := cmd.Flags().GetString("netbird-bin")
				ch.NetbirdBin = &v
			}
			if ch.DNSMode == nil && ch.WireguardPort == nil && ch.NetbirdBin == nil {
				return errors.New("nothing to change — pass --dns-mode, --wireguard-port and/or --netbird-bin")
			}
			if err := e.Set(cmd.Context(), i, ch); err != nil {
				return err
			}
			fmt.Printf("%s: settings updated\n", i.Name)
			return nil
		},
	}
	c.Flags().String("dns-mode", "", "who manages host DNS: native, multibird or disabled (see docs/dns.md)")
	c.Flags().Bool("disable-dns", false, "deprecated alias for --dns-mode disabled/native")
	_ = c.Flags().MarkDeprecated("disable-dns", "use --dns-mode")
	c.Flags().Int("wireguard-port", 0, "WireGuard listen port (0 restores the derived default)")
	c.Flags().String("netbird-bin", "", "pinned netbird binary path (empty restores PATH lookup)")
	return c
}

func logsCmd() *cobra.Command {
	var follow bool
	var tailKB int
	c := &cobra.Command{
		Use:               "logs <name>",
		Short:             "Show (or follow) an instance's daemon log",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			i, err := e.Load(args[0])
			if err != nil {
				return err
			}
			return e.Logs(cmd.Context(), i, os.Stdout, int64(tailKB)*1024, follow)
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new log lines")
	c.Flags().IntVar(&tailKB, "tail", 64, "how many KiB of history to print first")
	return c
}

func dnsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "dns",
		Short: "Host DNS arbitration (multibird dns_mode, macOS)",
	}
	c.AddCommand(dnsSyncCmd())
	return c
}

func dnsSyncCmd() *cobra.Command {
	var all, watch bool
	c := &cobra.Command{
		Use:               "sync [name]",
		Short:             "Re-apply host DNS registrations for multibird-mode instances",
		Long:              "Re-derives each instance's scoped resolvers from live daemon status and\nrewrites the host registration (idempotent; also cleans up when a daemon\nis down). --watch keeps doing it on daemon NETWORK/DNS events until\ninterrupted — suitable as a launchd KeepAlive job.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			insts, err := targets(e, args, all || (len(args) == 0 && !all))
			if err != nil {
				return err
			}
			if watch {
				fmt.Println("watching daemon events; Ctrl-C to stop")
				e.DNSWatch(cmd.Context(), insts)
				return nil
			}
			var failed int
			for _, i := range insts {
				if err := e.DNSSync(cmd.Context(), i); err != nil {
					failed++
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					continue
				}
				fmt.Printf("%s: dns synced\n", i.Name)
			}
			if len(args) == 0 { // full sync also sweeps strays of removed instances
				cleaned, err := e.DNSCleanupStrays()
				if err != nil {
					failed++
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
				}
				for _, o := range cleaned {
					fmt.Printf("%s: stray dns registration removed\n", o)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d sync step(s) failed", failed)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "sync every instance (default when no name is given)")
	c.Flags().BoolVar(&watch, "watch", false, "keep syncing on daemon events until interrupted")
	return c
}

func upgradeCmd() *cobra.Command {
	var check, force bool
	c := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade multibird to the latest GitHub release",
		Long:  "Downloads the latest release from github.com/" + selfupdate.Repo + ",\nverifies its checksum, and replaces this binary in place. Running\ninstances are untouched — the netbird daemons keep their connections.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rel, err := selfupdate.LatestRelease(cmd.Context())
			if err != nil {
				return err
			}
			cur := version.Version
			if cur == "dev" {
				if !force {
					return fmt.Errorf("this multibird was built from source (version %s) — upgrade with `git pull && go build ./cmd/multibird`, or pass --force to replace it with release %s", version.Full(), rel.Tag)
				}
			} else if cmpRes, err := version.Compare(cur, rel.Version()); err == nil && cmpRes >= 0 && !force {
				fmt.Printf("already up to date (%s; latest release is %s)\n", cur, rel.Tag)
				return nil
			}
			if check {
				fmt.Printf("upgrade available: %s -> %s (run `multibird upgrade` to install)\n", version.Full(), rel.Tag)
				return nil
			}
			bin, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locating the running binary: %w", err)
			}
			if err := selfupdate.Apply(cmd.Context(), rel, runtime.GOOS, runtime.GOARCH, bin); err != nil {
				return err
			}
			fmt.Printf("upgraded %s -> %s (%s)\nrunning instances are unaffected; daemon-spawn changes apply on the next `down` + `up`\n", version.Full(), rel.Tag, bin)
			return nil
		},
	}
	c.Flags().BoolVar(&check, "check", false, "only report whether an upgrade is available")
	c.Flags().BoolVar(&force, "force", false, "install even if this build is newer, equal, or built from source")
	return c
}

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Live view of all instances (v0.3)",
		Args:  cobra.NoArgs,
		RunE:  func(_ *cobra.Command, _ []string) error { return tui.Run() },
	}
}
