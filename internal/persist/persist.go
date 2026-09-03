// Package persist generates and manages boot-persistence units: systemd
// system units on Linux, launchd LaunchDaemons on macOS.
//
// Design rules (docs/privileges.md, ROADMAP v0.2):
//   - Units invoke the MULTIBIRD binary (`multibird up <name>`), never
//     netbird directly, so preflight checks always run — including at boot.
//   - Units are SYSTEM-level and run as root: daemons need root to create
//     TUN devices. On macOS that means a LaunchDaemon (LaunchAgents cannot
//     create utun devices).
//
// Unit CONTENT generation is pure and OS-independent (testable everywhere);
// installation/removal is build-tagged.
package persist

import "fmt"

// SystemdUnitPath is where the Linux unit for an instance lives.
func SystemdUnitPath(name string) string {
	return "/etc/systemd/system/multibird-" + name + ".service"
}

// LaunchdLabel is the launchd job label for an instance.
func LaunchdLabel(name string) string { return "io.github.magnetrong.multibird." + name }

// LaunchdPlistPath is where the macOS LaunchDaemon plist for an instance lives.
func LaunchdPlistPath(name string) string {
	return "/Library/LaunchDaemons/" + LaunchdLabel(name) + ".plist"
}

// SystemdUnit renders the systemd system unit. `up` spawns a detached daemon
// and exits, so the unit is oneshot+RemainAfterExit; the netbird daemon
// itself is not supervised by systemd (multibird nuke/up recovers crashes,
// matching stock netbird's model where the service wraps its own runner).
func SystemdUnit(name, multibirdBin string) string {
	return fmt.Sprintf(`[Unit]
Description=multibird instance %[1]s (isolated NetBird daemon)
Documentation=https://github.com/magnetrong/multibird
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%[2]s up %[1]s
ExecStop=%[2]s down %[1]s

[Install]
WantedBy=multi-user.target
`, name, multibirdBin)
}

// LaunchdPlist renders the macOS LaunchDaemon plist. RunAtLoad brings the
// instance up at boot; there is no KeepAlive because `up` exits after
// spawning the detached daemon.
func LaunchdPlist(name, multibirdBin string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%[1]s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%[2]s</string>
		<string>up</string>
		<string>%[3]s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`, LaunchdLabel(name), multibirdBin, name)
}

// SystemdDNSWatchUnitPath is where the Linux dns-watch unit lives.
func SystemdDNSWatchUnitPath(name string) string {
	return "/etc/systemd/system/multibird-dnswatch-" + name + ".service"
}

// LaunchdDNSWatchLabel is the launchd label for an instance's dns-watch job.
func LaunchdDNSWatchLabel(name string) string {
	return "io.github.magnetrong.multibird.dnswatch." + name
}

// LaunchdDNSWatchPlistPath is where the dns-watch LaunchDaemon plist lives.
func LaunchdDNSWatchPlistPath(name string) string {
	return "/Library/LaunchDaemons/" + LaunchdDNSWatchLabel(name) + ".plist"
}

// SystemdDNSWatchUnit renders the long-running dns-watch unit
// (`multibird dns sync <name> --watch`). Only meaningful for darwin's
// multibird DNS mode, but rendered on both OSes for symmetry.
func SystemdDNSWatchUnit(name, multibirdBin string) string {
	return fmt.Sprintf(`[Unit]
Description=multibird dns watch for instance %[1]s
Documentation=https://github.com/magnetrong/multibird
After=multibird-%[1]s.service
Wants=multibird-%[1]s.service

[Service]
ExecStart=%[2]s dns sync %[1]s --watch
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, name, multibirdBin)
}

// LaunchdDNSWatchPlist renders the KeepAlive dns-watch LaunchDaemon: it
// keeps the instance's host DNS registration in sync with daemon
// NETWORK/DNS events (docs/dns.md).
func LaunchdDNSWatchPlist(name, multibirdBin string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%[1]s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%[2]s</string>
		<string>dns</string>
		<string>sync</string>
		<string>%[3]s</string>
		<string>--watch</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`, LaunchdDNSWatchLabel(name), multibirdBin, name)
}
