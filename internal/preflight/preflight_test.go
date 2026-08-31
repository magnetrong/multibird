package preflight

import (
	"net/netip"
	"testing"
)

func mustNet(t *testing.T, name, cidr string) Net {
	t.Helper()
	return Net{Instance: name, Prefix: netip.MustParsePrefix(cidr)}
}

func TestIPRangeConflicts(t *testing.T) {
	tests := []struct {
		name string
		nets []Net
		want int // number of conflicts
	}{
		{
			name: "disjoint ranges",
			nets: []Net{
				mustNet(t, "home", "100.92.0.0/16"),
				mustNet(t, "lab", "100.101.0.0/16"),
			},
			want: 0,
		},
		{
			name: "identical ranges",
			nets: []Net{
				mustNet(t, "home", "100.92.0.0/16"),
				mustNet(t, "lab", "100.92.0.0/16"),
			},
			want: 1,
		},
		{
			name: "nested ranges",
			nets: []Net{
				mustNet(t, "home", "100.92.0.0/16"),
				mustNet(t, "lab", "100.92.14.0/24"),
			},
			want: 1,
		},
		{
			name: "three instances, one overlapping pair",
			nets: []Net{
				mustNet(t, "a", "100.64.0.0/16"),
				mustNet(t, "b", "100.65.0.0/16"),
				mustNet(t, "c", "100.64.128.0/17"),
			},
			want: 1,
		},
		{
			name: "wide range overlaps two disjoint ranges",
			nets: []Net{
				mustNet(t, "a", "100.64.0.0/10"),
				mustNet(t, "b", "100.64.0.0/16"),
				mustNet(t, "c", "100.70.0.0/16"),
			},
			want: 2,
		},
		{
			name: "all three overlap pairwise",
			nets: []Net{
				mustNet(t, "a", "100.64.0.0/10"),
				mustNet(t, "b", "100.64.0.0/16"),
				mustNet(t, "c", "100.64.0.0/24"),
			},
			want: 3,
		},
		{name: "empty", nets: nil, want: 0},
		{name: "single instance", nets: []Net{mustNet(t, "solo", "100.92.0.0/16")}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IPRangeConflicts(tt.nets)
			if len(got) != tt.want {
				t.Errorf("got %d conflicts (%v), want %d", len(got), got, tt.want)
			}
		})
	}
}

func TestParseAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string // expected prefix, "" means error expected
	}{
		{"cidr form", "100.92.14.7/16", "100.92.0.0/16"},
		{"bare ipv4 assumes /16", "100.92.14.7", "100.92.0.0/16"},
		{"bare ipv6 assumes /64", "fd00:1234:5678:9abc::7", "fd00:1234:5678:9abc::/64"},
		{"garbage", "not-an-ip", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAddr("x", tt.addr)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Prefix != netip.MustParsePrefix(tt.want) {
				t.Errorf("got %v, want %v", got.Prefix, tt.want)
			}
		})
	}
}
