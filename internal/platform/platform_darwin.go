//go:build darwin

package platform

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/magnetrong/multibird/internal/hostdns"
	"github.com/magnetrong/multibird/internal/instance"
)

type darwinPlatform struct{}

// scutilRunner runs one scutil stdin script; swapped out in tests so unit
// tests never exec scutil.
type scutilRunner func(script string) (string, error)

func execScutil(script string) (string, error) {
	cmd := exec.Command("/usr/sbin/scutil")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("scutil: %w: %s (host DNS changes need root — try sudo)", err, out)
	}
	// scutil exits 0 even when a set/remove fails (e.g. permission denied as
	// non-root) and prints the error to stdout instead — verified in the
	// field 2026-09-03. Treat error-looking output as failure.
	if serr := scutilOutputError(string(out)); serr != nil {
		return "", fmt.Errorf("scutil: %w (host DNS changes need root — try sudo)", serr)
	}
	return string(out), nil
}

var runScutil scutilRunner = execScutil

// listMultibirdKeys returns every multibird-owned dynamic-store DNS key.
func listMultibirdKeys() ([]string, error) {
	out, err := runScutil(listScript())
	if err != nil {
		return nil, err
	}
	return parseListedKeys(out), nil
}

// flushDNSCache mirrors upstream host_darwin.go flushDNSCache.
func flushDNSCache() error {
	if out, err := exec.Command("/usr/bin/dscacheutil", "-flushcache").CombinedOutput(); err != nil {
		return fmt.Errorf("dscacheutil -flushcache: %w: %s", err, out)
	}
	if out, err := exec.Command("killall", "-HUP", "mDNSResponder").CombinedOutput(); err != nil {
		return fmt.Errorf("killall -HUP mDNSResponder: %w: %s", err, out)
	}
	return nil
}

// ApplyHostDNS writes the instance's scoped resolvers: remove its stale keys,
// set the new ones, flush the resolver cache. Idempotent. Needs root — the
// dynamic store rejects unprivileged writes (and scutil hides the failure in
// exit-0 output).
func (darwinPlatform) ApplyHostDNS(instanceName string, spec hostdns.Spec) error {
	if err := requireRoot(); err != nil {
		return err
	}
	existing, err := listMultibirdKeys()
	if err != nil {
		return fmt.Errorf("listing dynamic-store keys: %w", err)
	}
	stale := keysOfInstance(existing, instanceName)
	keys := hostDNSKeys(instanceName, spec)
	if len(stale) == 0 && len(keys) == 0 {
		return nil
	}
	if _, err := runScutil(renderApplyScript(stale, keys, spec.Listen)); err != nil {
		return fmt.Errorf("writing DNS keys for %q: %w", instanceName, err)
	}
	if err := flushDNSCache(); err != nil {
		return fmt.Errorf("DNS keys for %q written but cache flush failed: %w", instanceName, err)
	}
	return nil
}

// RemoveHostDNS removes every key the instance owns. Idempotent; needs root
// only when there is something to remove.
func (darwinPlatform) RemoveHostDNS(instanceName string) error {
	existing, err := listMultibirdKeys()
	if err != nil {
		return fmt.Errorf("listing dynamic-store keys: %w", err)
	}
	keys := keysOfInstance(existing, instanceName)
	if len(keys) == 0 {
		return nil
	}
	if err := requireRoot(); err != nil {
		return err
	}
	if _, err := runScutil(renderRemoveScript(keys)); err != nil {
		return fmt.Errorf("removing DNS keys for %q: %w", instanceName, err)
	}
	if err := flushDNSCache(); err != nil {
		return fmt.Errorf("DNS keys for %q removed but cache flush failed: %w", instanceName, err)
	}
	return nil
}

// ListHostDNSOwners lists instance names with registered keys.
func (darwinPlatform) ListHostDNSOwners() ([]string, error) {
	keys, err := listMultibirdKeys()
	if err != nil {
		return nil, err
	}
	return ownersFromKeys(keys), nil
}

// StockNetbirdDNSPresent checks for stock netbird's own DNS keys
// (State:/Network/Service/NetBird-*), the ones subject to the upstream
// fixed-name collision.
func (darwinPlatform) StockNetbirdDNSPresent() (bool, error) {
	out, err := runScutil("list State:/Network/Service/NetBird-.*/DNS\nquit\n")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "State:/Network/Service/NetBird-"), nil
}

func newPlatform() Platform { return darwinPlatform{} }

func (darwinPlatform) ConfigRoot() (string, error) {
	// Boot units run as root with root's HOME; they pin the installing
	// user's config root via this env var (set in the generated units).
	if r := os.Getenv("MULTIBIRD_CONFIG_ROOT"); r != "" {
		return r, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "multibird"), nil
}

func (darwinPlatform) RunDir() string { return "/var/run/multibird" }

// InterfaceHint: netbird's darwin code configures the interface by the
// EXACT name in its config (ifconfig/route by name; it never reads back a
// kernel-assigned name), so the bare auto-assign name "utun" is not usable
// and the hint must be a specific free unit. Stock netbird's own default is
// utun100 and its daemon can hold that unit even when ifconfig doesn't show
// it, so we start well away at 210+index and skip any unit that is visibly
// in use. The ACTUAL interface is still discovered post-up via
// DiscoverInterface — the hint is a request, not a promise.
func (darwinPlatform) InterfaceHint(i int) string {
	used := map[int]bool{}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, f := range ifaces {
			var n int
			if _, err := fmt.Sscanf(f.Name, "utun%d", &n); err == nil {
				used[n] = true
			}
		}
	}
	for n := 210 + i; ; n++ {
		if !used[n] {
			return fmt.Sprintf("utun%d", n)
		}
	}
}

// DefaultDNSMode: macOS dynamic-store DNS keys collide between netbird
// daemons (fixed-name NetBird-Match-0 keys — docs/dns.md), so new instances
// default to multibird-arbitrated DNS.
func (darwinPlatform) DefaultDNSMode() instance.DNSMode { return instance.DNSMultibird }

func (darwinPlatform) DiscoverInterface(addr netip.Addr) (string, error) {
	return discoverByAddr(addr)
}

// DaemonEnv: netbird's darwin "advanced routing" installs a scoped default
// route and, on every engine start AND stop, flushes ALL netbird-tagged
// scoped defaults — including a sibling daemon's (verified in v0.77.1
// systemops_darwin.go flushScopedDefaults). Two daemons therefore kick each
// other into restart loops. NB_USE_LEGACY_ROUTING selects the ref-counted
// per-prefix exclusion routes instead, which coexist cleanly.
func (darwinPlatform) DaemonEnv() []string {
	return []string{"NB_USE_LEGACY_ROUTING=true"}
}

// RouteInterface uses `route -n get`, whose output contains a line like:
//
//	interface: utun4
func (darwinPlatform) RouteInterface(addr netip.Addr) (string, error) {
	out, err := exec.Command("route", "-n", "get", addr.String()).Output()
	if err != nil {
		return "", fmt.Errorf("`route -n get %s`: %w", addr, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if name, ok := strings.CutPrefix(line, "interface:"); ok {
			return strings.TrimSpace(name), nil
		}
	}
	return "", fmt.Errorf("no interface in `route -n get %s` output", addr)
}
