//go:build darwin

package persist

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// Install writes the LaunchDaemon plist for the instance and loads it.
// Requires root (LaunchAgents can't create utun devices — see
// docs/privileges.md). Returns the plist path.
func Install(name, multibirdBin string) (string, error) {
	path := LaunchdPlistPath(name)
	if err := os.WriteFile(path, []byte(LaunchdPlist(name, multibirdBin)), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w — LaunchDaemons are system-level, run with sudo", path, err)
	}
	if out, err := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err != nil {
		return "", fmt.Errorf("launchctl load: %w: %s", err, out)
	}
	return path, nil
}

// Uninstall unloads and removes the instance's LaunchDaemon. Idempotent.
// Requires root.
func Uninstall(name string) error {
	path := LaunchdPlistPath(name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if out, err := exec.Command("launchctl", "unload", "-w", path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl unload: %w: %s — run with sudo", err, out)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}
