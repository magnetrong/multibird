//go:build darwin

package platform

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
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

// InterfaceHint: macOS utun numbers are KERNEL-ASSIGNED. We still pass a
// hint via Login (netbird treats non-utun names as advisory on darwin), but
// the actual interface MUST be discovered post-up via DiscoverInterface and
// recorded in instance state. Never predict a utun number.
func (darwinPlatform) InterfaceHint(i int) string { return fmt.Sprintf("utun%d", 100+i) }

func (darwinPlatform) DiscoverInterface(addr netip.Addr) (string, error) {
	return discoverByAddr(addr)
}
