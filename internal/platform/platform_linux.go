//go:build linux

package platform

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
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

func (linuxPlatform) DiscoverInterface(addr netip.Addr) (string, error) {
	return discoverByAddr(addr)
}
