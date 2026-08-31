package nbcli

import (
	"reflect"
	"testing"
)

// TestServiceRunArgs pins the CLI contract (see package doc + CLAUDE.md).
// If this test needs changing, the contract changed: update CLAUDE.md's
// "THE CLI CONTRACT" in the same PR.
func TestServiceRunArgs(t *testing.T) {
	tests := []struct {
		name                            string
		config, addr, logFile, logLevel string
		want                            []string
	}{
		{
			name:     "typical instance",
			config:   "/home/u/.config/multibird/home/config.json",
			addr:     "unix:///var/run/multibird/home.sock",
			logFile:  "/home/u/.config/multibird/home/daemon.log",
			logLevel: "info",
			want: []string{
				"service", "run",
				"--config", "/home/u/.config/multibird/home/config.json",
				"--daemon-addr", "unix:///var/run/multibird/home.sock",
				"--log-file", "/home/u/.config/multibird/home/daemon.log",
				"--log-level", "info",
			},
		},
		{
			name:     "debug log level",
			config:   "/c.json",
			addr:     "unix:///s.sock",
			logFile:  "/l.log",
			logLevel: "debug",
			want: []string{
				"service", "run",
				"--config", "/c.json",
				"--daemon-addr", "unix:///s.sock",
				"--log-file", "/l.log",
				"--log-level", "debug",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceRunArgs(tt.config, tt.addr, tt.logFile, tt.logLevel)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ServiceRunArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewDefaultsBin(t *testing.T) {
	if got := New("").Bin; got != "netbird" {
		t.Errorf("New(\"\").Bin = %q, want netbird", got)
	}
	if got := New("/opt/nb/netbird").Bin; got != "/opt/nb/netbird" {
		t.Errorf("New(path).Bin = %q", got)
	}
}
