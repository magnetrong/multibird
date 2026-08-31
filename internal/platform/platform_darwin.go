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
)

type darwinPlatform struct{}

func newPlatform() Platform { return darwinPlatform{} }

func (darwinPlatform) ConfigRoot() (string, error) {
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
