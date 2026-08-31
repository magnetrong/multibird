// Package persist will generate and manage boot-persistence units in v0.2:
// systemd units on Linux, launchd plists on macOS (`multibird install` /
// `multibird uninstall`).
//
// Design constraints already decided (see ROADMAP.md v0.2 and
// docs/privileges.md):
//   - Generated units invoke the MULTIBIRD binary (`multibird up <name>`),
//     never netbird directly, so preflight checks always run.
//   - Units run as root by default: daemons need root to create TUN devices.
//     On macOS this means a LaunchDaemon, not a LaunchAgent (agents cannot
//     create utun devices) — a deliberate deviation from the original brief.
//   - Unit generation is OS-specific and belongs behind
//     internal/platform-style build tags when implemented.
package persist

import "errors"

// ErrNotImplemented marks the v0.2 surface.
var ErrNotImplemented = errors.New("boot persistence (install/uninstall) lands in v0.2 — see ROADMAP.md")

// Install will generate + load the boot unit for an instance. v0.2.
func Install(instanceName string, system bool) error { return ErrNotImplemented }

// Uninstall reverses Install. v0.2.
func Uninstall(instanceName string) error { return ErrNotImplemented }
