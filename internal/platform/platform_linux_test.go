//go:build linux

package platform

import "testing"

func TestParseRouteDev(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string // "" = error expected
	}{
		{"netbird custom table", "10.1.2.3 dev wt-mb-0 table 7120 src 100.92.14.7 uid 0\n    cache\n", "wt-mb-0"},
		{"main table", "8.8.8.8 via 192.168.0.1 dev eth0 src 192.168.0.10 uid 1000\n", "eth0"},
		{"local route no dev", "local 127.0.0.1 table local proto kernel scope host src 127.0.0.1\n", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRouteDev(tt.out)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
