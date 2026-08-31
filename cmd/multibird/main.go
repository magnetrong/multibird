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
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/ops"
	"github.com/magnetrong/multibird/internal/persist"
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
		list, err := e.Store.List()
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
	i, err := e.Store.Load(args[0])
	if err != nil {
		return nil, err
	}
	return []*instance.Instance{i}, nil
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "multibird",
		Short:         "Run multiple NetBird VPN instances simultaneously",
		Long:          "multibird runs multiple isolated NetBird daemons side by side —\none per mesh — until NetBird ships native simultaneous profiles\n(netbirdio/netbird#446), at which point you should migrate back and\ndelete multibird.",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(addCmd(), upCmd(), downCmd(), statusCmd(), listCmd(),
		removeCmd(), doctorCmd(), nukeCmd(), setCmd(), logsCmd(),
		installCmd(), uninstallCmd(), tuiCmd())
	return root
}

// completeInstanceNames offers configured instance names for shell completion.
func completeInstanceNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	e, err := env()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	insts, err := e.Store.List()
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
	var mgmtURL, setupKey, nbBin string
	var sso, disableDNS bool
	var wgPort int
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a new instance (does not start it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (setupKey == "") == !sso {
				return errors.New("exactly one of --setup-key or --sso is required")
			}
			e, err := env()
			if err != nil {
				return err
			}
			inst := &instance.Instance{
				Name: args[0], ManagementURL: mgmtURL, SetupKey: setupKey,
				SSO: sso, NetbirdBin: nbBin, WireguardPort: wgPort, DisableDNS: disableDNS,
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
	c.Flags().BoolVar(&disableDNS, "disable-dns", false, "don't let this instance manage host DNS (see docs/dns.md)")
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
			var insts []*instance.Instance
			if len(args) == 1 {
				i, err := e.Store.Load(args[0])
				if err != nil {
					return err
				}
				insts = []*instance.Instance{i}
			} else if insts, err = e.Store.List(); err != nil {
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

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured instances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			insts, err := e.Store.List()
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
				dns := "managed"
				if i.DisableDNS {
					dns = "disabled"
				}
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
			i, err := e.Store.Load(args[0])
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
		RunE: func(cmd *cobra.Command, args []string) error {
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
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			i, err := e.Store.Load(args[0])
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
	return &cobra.Command{
		Use:               "install <name>",
		Short:             "Install a boot unit (systemd/launchd, system-level) for an instance",
		Long:              "Generates and enables a root-level boot unit that runs `multibird up <name>`\nat boot — via multibird, never netbird directly, so preflight always runs.\nRequires sudo.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			i, err := e.Store.Load(args[0])
			if err != nil {
				return err
			}
			bin, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locating the multibird binary: %w", err)
			}
			path, err := persist.Install(i.Name, bin)
			if err != nil {
				return err
			}
			fmt.Printf("%s: boot unit installed (%s) — instance comes up at boot; `multibird uninstall %s` reverses this\n", i.Name, path, i.Name)
			return nil
		},
	}
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "uninstall <name>",
		Short:             "Remove an instance's boot unit",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := persist.Uninstall(args[0]); err != nil {
				return err
			}
			fmt.Printf("%s: boot unit removed\n", args[0])
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
			i, err := e.Store.Load(args[0])
			if err != nil {
				return err
			}
			var ch ops.SetChanges
			if cmd.Flags().Changed("disable-dns") {
				v, _ := cmd.Flags().GetBool("disable-dns")
				ch.DisableDNS = &v
			}
			if cmd.Flags().Changed("wireguard-port") {
				v, _ := cmd.Flags().GetInt("wireguard-port")
				ch.WireguardPort = &v
			}
			if cmd.Flags().Changed("netbird-bin") {
				v, _ := cmd.Flags().GetString("netbird-bin")
				ch.NetbirdBin = &v
			}
			if ch.DisableDNS == nil && ch.WireguardPort == nil && ch.NetbirdBin == nil {
				return errors.New("nothing to change — pass --disable-dns, --wireguard-port and/or --netbird-bin")
			}
			if err := e.Set(cmd.Context(), i, ch); err != nil {
				return err
			}
			fmt.Printf("%s: settings updated\n", i.Name)
			return nil
		},
	}
	c.Flags().Bool("disable-dns", false, "true: this instance stops managing host DNS; false: it manages DNS (see docs/dns.md)")
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
			i, err := e.Store.Load(args[0])
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

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Live view of all instances (v0.3)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return tui.Run() },
	}
}
