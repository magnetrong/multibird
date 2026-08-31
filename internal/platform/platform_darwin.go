//go:build darwin

package platform

import (
	"fmt"
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

// InterfaceHint: macOS utun numbers are KERNEL-ASSIGNED. Passing a specific
// unit (e.g. "utun100") fails with "resource busy" when the unit can't be
// claimed, so we pass the literal name "utun", which wireguard-go treats as
// "pick the next free unit". The actual interface MUST then be discovered
// post-up via DiscoverInterface and recorded in instance state. Never
// predict a utun number.
func (darwinPlatform) InterfaceHint(int) string { return "utun" }

func (darwinPlatform) DiscoverInterface(addr netip.Addr) (string, error) {
	return discoverByAddr(addr)
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
