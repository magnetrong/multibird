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
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/OWNER/multibird/internal/instance"
	"github.com/OWNER/multibird/internal/ops"
	"github.com/OWNER/multibird/internal/persist"
	"github.com/OWNER/multibird/internal/tui"
	"github.com/OWNER/multibird/internal/version"
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
		removeCmd(), doctorCmd(), nukeCmd(), installCmd(), uninstallCmd(), tuiCmd())
	return root
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
				return errors.New("exactly one of --setup-key or --sso is required (note: --sso is accepted but not yet supported at `up` time in v0.1)")
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
	c.Flags().BoolVar(&sso, "sso", false, "use SSO login instead of a setup key (not yet supported in v0.1)")
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
			for _, i := range insts {
				if err := e.Up(cmd.Context(), i, strict); err != nil {
					return err
				}
				fmt.Printf("%s: up\n", i.Name)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "bring all instances up")
	c.Flags().BoolVar(&strict, "strict", false, "treat preflight conflicts as fatal (instance is brought back down)")
	return c
}

func downCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "down [name]",
		Short: "Bring instance(s) down and stop their daemons",
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
		Use:   "status [name]",
		Short: "Show aggregated status across instances",
		Args:  cobra.MaximumNArgs(1),
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
		Use:   "remove <name>",
		Short: "Tear down an instance and delete multibird's state for it",
		Args:  cobra.ExactArgs(1),
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
		Use:   "nuke <name>",
		Short: "Forceful cleanup of a crashed/half-up instance (idempotent)",
		Args:  cobra.ExactArgs(1),
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
	var system bool
	c := &cobra.Command{
		Use:   "install <name>",
		Short: "Install a boot unit for an instance (v0.2)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return persist.Install(args[0], system)
		},
	}
	c.Flags().BoolVar(&system, "system", false, "install a system-level unit")
	return c
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Remove an instance's boot unit (v0.2)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return persist.Uninstall(args[0])
		},
	}
}

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Live view of all instances (v0.3)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return tui.Run() },
	}
}
