package persist

import (
	"strings"
	"testing"
)

// Units must call MULTIBIRD, never netbird, so preflight always runs (rule
// from docs/privileges.md / ROADMAP v0.2).
func TestUnitsInvokeMultibirdNotNetbird(t *testing.T) {
	for name, content := range map[string]string{
		"systemd": SystemdUnit("home", "/usr/local/bin/multibird"),
		"launchd": LaunchdPlist("home", "/usr/local/bin/multibird"),
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
	u := SystemdUnit("home", "/usr/local/bin/multibird")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/multibird up home",
		"ExecStop=/usr/local/bin/multibird down home",
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
	p := LaunchdPlist("home", "/usr/local/bin/multibird")
	for _, want := range []string{
		"<string>io.github.magnetrong.multibird.home</string>",
		"<string>/usr/local/bin/multibird</string>",
		"<string>up</string>",
		"<string>home</string>",
		"<key>RunAtLoad</key>",
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
