//go:build linux

package platform

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/magnetrong/multibird/internal/hostdns"
	"github.com/magnetrong/multibird/internal/instance"
)

type linuxPlatform struct{}

func newPlatform() Platform { return linuxPlatform{} }

func (linuxPlatform) ConfigRoot() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "multibird"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, ".config", "multibird"), nil
}

func (linuxPlatform) RunDir() string { return "/var/run/multibird" }

// InterfaceHint: Linux interface names are ours to choose; wt-mb-<i> stays
// well under the 15-char IFNAMSIZ limit.
func (linuxPlatform) InterfaceHint(i int) string { return fmt.Sprintf("wt-mb-%d", i) }

// DefaultDNSMode: netbird's per-link systemd-resolved integration coexists
// fine on Linux, so native stays the default there.
func (linuxPlatform) DefaultDNSMode() instance.DNSMode { return instance.DNSNative }

func (linuxPlatform) DiscoverInterface(addr netip.Addr) (string, error) {
	return discoverByAddr(addr)
}

// DaemonEnv: no coexistence overrides needed on Linux so far. (Open
// question: whether two daemons' policy-routing tables collide — revisit
// when running multiple instances on Linux with routes.)
func (linuxPlatform) DaemonEnv() []string { return nil }

// RouteInterface uses `ip route get`, which consults all routing tables and
// rules (netbird installs routes in a custom table, so /proc/net/route alone
// would miss them). Output looks like:
//
//	10.1.2.3 dev wt-mb-0 table 7120 src 100.92.14.7 uid 0
func (linuxPlatform) RouteInterface(addr netip.Addr) (string, error) {
	out, err := exec.Command("ip", "route", "get", addr.String()).Output()
	if err != nil {
		return "", fmt.Errorf("`ip route get %s`: %w", addr, err)
	}
	return parseRouteDev(string(out))
}

// parseRouteDev extracts the token after "dev" (shared shape on both
// platforms' route-lookup outputs handled per-OS).
func parseRouteDev(out string) (string, error) {
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("no device in route lookup output %q", strings.TrimSpace(out))
}

// ApplyHostDNS: the multibird DNS mode is darwin-only; Linux keeps netbird's
// native per-link DNS management.
func (linuxPlatform) ApplyHostDNS(instanceName string, _ hostdns.Spec) error {
	return fmt.Errorf("instance %q: dns_mode multibird is not supported on linux — use `multibird set %s --dns-mode native` (netbird's systemd-resolved integration is per-link and coexists fine)", instanceName, instanceName)
}

// RemoveHostDNS is a no-op on linux (nothing is ever registered).
func (linuxPlatform) RemoveHostDNS(string) error { return nil }

// ListHostDNSOwners: nothing is ever registered on linux.
func (linuxPlatform) ListHostDNSOwners() ([]string, error) { return nil, nil }

// StockNetbirdDNSPresent: the darwin dynamic-store collision doesn't exist here.
func (linuxPlatform) StockNetbirdDNSPresent() (bool, error) { return false, nil }
