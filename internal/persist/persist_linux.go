//go:build linux

package persist

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// Install writes the systemd system unit for the instance and enables it at
// boot. Requires root. Returns the unit path.
func Install(name, multibirdBin, configRoot string) (string, error) {
	path := SystemdUnitPath(name)
	if err := os.WriteFile(path, []byte(SystemdUnit(name, multibirdBin, configRoot)), 0o644); err != nil { //nolint:gosec // G306: systemd units are conventionally world-readable and contain no secrets
		return "", fmt.Errorf("writing %s: %w — boot units are system-level, run with sudo", path, err)
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return "", fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}
	if out, err := exec.Command("systemctl", "enable", "multibird-"+name+".service").CombinedOutput(); err != nil {
		return "", fmt.Errorf("systemctl enable: %w: %s", err, out)
	}
	return path, nil
}

// Uninstall disables and removes the instance's unit. Idempotent. Requires root.
func Uninstall(name string) error {
	unit := "multibird-" + name + ".service"
	path := SystemdUnitPath(name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if out, err := exec.Command("systemctl", "disable", unit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl disable: %w: %s — run with sudo", err, out)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}
	return nil
}

// InstallDNSWatch writes and enables the dns-watch unit. Requires root.
func InstallDNSWatch(name, multibirdBin, configRoot string) (string, error) {
	path := SystemdDNSWatchUnitPath(name)
	if err := os.WriteFile(path, []byte(SystemdDNSWatchUnit(name, multibirdBin, configRoot)), 0o644); err != nil { //nolint:gosec // G306: units are world-readable, no secrets
		return "", fmt.Errorf("writing %s: %w — run with sudo", path, err)
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return "", fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}
	if out, err := exec.Command("systemctl", "enable", "--now", "multibird-dnswatch-"+name+".service").CombinedOutput(); err != nil {
		return "", fmt.Errorf("systemctl enable --now: %w: %s", err, out)
	}
	return path, nil
}

// UninstallDNSWatch removes the dns-watch unit. Idempotent.
func UninstallDNSWatch(name string) error {
	unit := "multibird-dnswatch-" + name + ".service"
	path := SystemdDNSWatchUnitPath(name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if out, err := exec.Command("systemctl", "disable", "--now", unit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl disable --now: %w: %s — run with sudo", err, out)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}
	return nil
}
