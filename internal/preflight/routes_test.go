package preflight

import (
	"net/netip"
	"reflect"
	"testing"
)

func route(inst, cidr string, sel bool) Route {
	return Route{Instance: inst, Prefix: netip.MustParsePrefix(cidr), Selected: sel}
}

func TestRouteConflicts(t *testing.T) {
	tests := []struct {
		name   string
		routes []Route
		want   int
	}{
		{
			name: "the classic: both meshes route the same LAN",
			routes: []Route{
				route("work", "192.168.1.0/24", true),
				route("home", "192.168.1.0/24", true),
			},
			want: 1,
		},
		{
			name: "nested prefixes conflict",
			routes: []Route{
				route("work", "10.0.0.0/8", true),
				route("home", "10.1.2.0/24", true),
			},
			want: 1,
		},
		{
			name: "unselected routes never conflict",
			routes: []Route{
				route("work", "192.168.1.0/24", true),
				route("home", "192.168.1.0/24", false),
			},
			want: 0,
		},
		{
			name: "same instance advertising overlapping routes is not a cross-mesh conflict",
			routes: []Route{
				route("work", "10.0.0.0/8", true),
				route("work", "10.1.0.0/16", true),
			},
			want: 0,
		},
		{
			name: "disjoint routes",
			routes: []Route{
				route("work", "10.10.0.0/16", true),
				route("home", "10.20.0.0/16", true),
			},
			want: 0,
		},
		{name: "empty", routes: nil, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RouteConflicts(tt.routes); len(got) != tt.want {
				t.Errorf("got %d conflicts (%v), want %d", len(got), got, tt.want)
			}
		})
	}
}

func TestDNSConflicts(t *testing.T) {
	tests := []struct {
		name    string
		intents []DNSIntent
		want    []DNSConflict
	}{
		{
			name: "two primary claims",
			intents: []DNSIntent{
				{Instance: "work", ManagesDNS: true},
				{Instance: "home", ManagesDNS: true},
			},
			want: []DNSConflict{{Kind: "primary", Instances: []string{"home", "work"}}},
		},
		{
			name: "one primary, one disabled: fine",
			intents: []DNSIntent{
				{Instance: "work", ManagesDNS: true},
				{Instance: "home", ManagesDNS: false},
			},
			want: nil,
		},
		{
			name: "disjoint split domains: fine",
			intents: []DNSIntent{
				{Instance: "work", ManagesDNS: true, Domains: []string{"corp.example"}},
				{Instance: "home", ManagesDNS: true, Domains: []string{"lan.home"}},
			},
			want: nil,
		},
		{
			name: "same match domain",
			intents: []DNSIntent{
				{Instance: "work", ManagesDNS: true, Domains: []string{"internal.example"}},
				{Instance: "home", ManagesDNS: true, Domains: []string{"Internal.Example."}},
			},
			want: []DNSConflict{{Kind: "domain", Instances: []string{"home", "work"}, Domain: "internal.example"}},
		},
		{
			name: "empty-string domain counts as primary claim",
			intents: []DNSIntent{
				{Instance: "work", ManagesDNS: true, Domains: []string{""}},
				{Instance: "home", ManagesDNS: true, Domains: []string{"lan.home"}},
			},
			want: nil, // only one primary claim; lan.home is split
		},
		{
			name: "root-dot domain counts as primary claim, two of them conflict",
			intents: []DNSIntent{
				{Instance: "work", ManagesDNS: true, Domains: []string{"."}},
				{Instance: "home", ManagesDNS: true, Domains: []string{""}},
			},
			want: []DNSConflict{{Kind: "primary", Instances: []string{"home", "work"}}},
		},
		{
			name: "disabled instances never conflict on domains",
			intents: []DNSIntent{
				{Instance: "work", ManagesDNS: false, Domains: []string{"x.example"}},
				{Instance: "home", ManagesDNS: false, Domains: []string{"x.example"}},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DNSConflicts(tt.intents)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
