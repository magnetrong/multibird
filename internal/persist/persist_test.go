package persist

import (
	"strings"
	"testing"
)

// Units must call MULTIBIRD, never netbird, so preflight always runs (rule
// from docs/privileges.md / ROADMAP v0.2).
func TestUnitsInvokeMultibirdNotNetbird(t *testing.T) {
	for name, content := range map[string]string{
		"systemd":          SystemdUnit("home", "/usr/local/bin/multibird", "/Users/u/cfg"),
		"launchd":          LaunchdPlist("home", "/usr/local/bin/multibird", "/Users/u/cfg"),
		"systemd dnswatch": SystemdDNSWatchUnit("home", "/usr/local/bin/multibird", "/Users/u/cfg"),
		"launchd dnswatch": LaunchdDNSWatchPlist("home", "/usr/local/bin/multibird", "/Users/u/cfg"),
	} {
		if !strings.Contains(content, "/usr/local/bin/multibird") {
			t.Errorf("%s unit does not invoke the multibird binary:\n%s", name, content)
		}
		if strings.Contains(content, "netbird ") || strings.Contains(content, "/netbird\n") {
			t.Errorf("%s unit invokes netbird directly:\n%s", name, content)
		}
	}
}

func TestSystemdUnit(t *testing.T) {
	u := SystemdUnit("home", "/usr/local/bin/multibird", "/Users/u/cfg")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/multibird up home",
		"ExecStop=/usr/local/bin/multibird down home",
		"Environment=MULTIBIRD_CONFIG_ROOT=/Users/u/cfg",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"After=network-online.target",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("systemd unit missing %q:\n%s", want, u)
		}
	}
}

func TestLaunchdPlist(t *testing.T) {
	p := LaunchdPlist("home", "/usr/local/bin/multibird", "/Users/u/cfg")
	for _, want := range []string{
		"<string>io.github.magnetrong.multibird.home</string>",
		"<string>/usr/local/bin/multibird</string>",
		"<string>up</string>",
		"<string>home</string>",
		"<key>RunAtLoad</key>",
		"<key>MULTIBIRD_CONFIG_ROOT</key>",
		"<string>/Users/u/cfg</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("launchd plist missing %q:\n%s", want, p)
		}
	}
}

func TestPaths(t *testing.T) {
	if got := SystemdUnitPath("home"); got != "/etc/systemd/system/multibird-home.service" {
		t.Errorf("SystemdUnitPath = %q", got)
	}
	if got := LaunchdPlistPath("home"); got != "/Library/LaunchDaemons/io.github.magnetrong.multibird.home.plist" {
		t.Errorf("LaunchdPlistPath = %q", got)
	}
}

func TestDNSWatchUnits(t *testing.T) {
	u := SystemdDNSWatchUnit("home", "/usr/local/bin/multibird", "/Users/u/cfg")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/multibird dns sync home --watch",
		"Restart=on-failure",
		"Environment=MULTIBIRD_CONFIG_ROOT=/Users/u/cfg",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("systemd dns-watch unit missing %q:\n%s", want, u)
		}
	}
	p := LaunchdDNSWatchPlist("home", "/usr/local/bin/multibird", "/Users/u/cfg")
	for _, want := range []string{
		"<string>io.github.magnetrong.multibird.dnswatch.home</string>",
		"<string>dns</string>", "<string>sync</string>", "<string>--watch</string>",
		"<key>KeepAlive</key>",
		"<key>MULTIBIRD_CONFIG_ROOT</key>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("launchd dns-watch plist missing %q:\n%s", want, p)
		}
	}
	if got := LaunchdDNSWatchPlistPath("home"); got != "/Library/LaunchDaemons/io.github.magnetrong.multibird.dnswatch.home.plist" {
		t.Errorf("LaunchdDNSWatchPlistPath = %q", got)
	}
	if got := SystemdDNSWatchUnitPath("home"); got != "/etc/systemd/system/multibird-dnswatch-home.service" {
		t.Errorf("SystemdDNSWatchUnitPath = %q", got)
	}
}
