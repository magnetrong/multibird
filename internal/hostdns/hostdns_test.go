package hostdns

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/netbirdio/netbird/client/proto"
)

var listen = netip.MustParseAddrPort("127.0.0.1:5300")

func status(fqdn, ip, ipv6 string, groups ...*proto.NSGroupState) *proto.StatusResponse {
	return &proto.StatusResponse{
		FullStatus: &proto.FullStatus{
			LocalPeerState: &proto.LocalPeerState{Fqdn: fqdn, IP: ip, Ipv6: ipv6},
			DnsServers:     groups,
		},
	}
}

func group(enabled bool, domains ...string) *proto.NSGroupState {
	return &proto.NSGroupState{Enabled: enabled, Domains: domains, Servers: []string{"1.2.3.4:53"}}
}

func TestDerive(t *testing.T) {
	tests := []struct {
		name    string
		st      *proto.StatusResponse
		want    Spec
		wantErr error
	}{
		{
			name: "peer domain only, no nameserver groups",
			st:   status("mac.mesh.magnetrong.com", "100.96.43.121/16", ""),
			want: Spec{
				Listen:        listen,
				SearchDomains: []string{"mesh.magnetrong.com"},
				MatchDomains:  []string{"96.100.in-addr.arpa", "mesh.magnetrong.com"},
			},
		},
		{
			name: "several groups, dedup + lowercase + trailing dot",
			st: status("mac.mesh.example", "100.96.43.121/16", "",
				group(true, "Corp.Example.", "lab.example"),
				group(true, "corp.example")),
			want: Spec{
				Listen:        listen,
				SearchDomains: []string{"mesh.example"},
				MatchDomains:  []string{"96.100.in-addr.arpa", "corp.example", "lab.example", "mesh.example"},
			},
		},
		{
			name: "disabled group ignored",
			st: status("mac.mesh.example", "100.96.43.121/16", "",
				group(false, "off.example")),
			want: Spec{
				Listen:        listen,
				SearchDomains: []string{"mesh.example"},
				MatchDomains:  []string{"96.100.in-addr.arpa", "mesh.example"},
			},
		},
		{
			name: "ipv6 present adds ip6.arpa zone",
			st:   status("mac.mesh.example", "100.96.43.121/16", "fd00:b14d:0:96::7/64"),
			want: Spec{
				Listen:        listen,
				SearchDomains: []string{"mesh.example"},
				MatchDomains: []string{
					"6.9.0.0.0.0.0.0.d.4.1.b.0.0.d.f.ip6.arpa",
					"96.100.in-addr.arpa",
					"mesh.example",
				},
			},
		},
		{
			name:    "primary claim (empty domain) refused",
			st:      status("mac.mesh.example", "100.96.43.121/16", "", group(true, "")),
			wantErr: ErrPrimaryClaim,
		},
		{
			name:    "primary claim (dot) refused",
			st:      status("mac.mesh.example", "100.96.43.121/16", "", group(true, ".")),
			wantErr: ErrPrimaryClaim,
		},
		{
			name:    "primary claim (no domains in group) refused",
			st:      status("mac.mesh.example", "100.96.43.121/16", "", &proto.NSGroupState{Enabled: true, Servers: []string{"9.9.9.9:53"}}),
			wantErr: ErrPrimaryClaim,
		},
		{
			name: "no fqdn, no ip: empty spec",
			st:   status("", "", ""),
			want: Spec{Listen: listen},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Derive(tt.st, listen)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Derive() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestDeriveGroupWithNoDomainsButDisabled(t *testing.T) {
	// A DISABLED route-all group must not trip the primary-claim refusal.
	st := status("mac.mesh.example", "100.96.43.121/16", "", group(false))
	if _, err := Derive(st, listen); err != nil {
		t.Fatalf("disabled route-all group should be ignored: %v", err)
	}
}

func TestReverseZone(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"100.96.43.121/16", "96.100.in-addr.arpa"}, // verified in the field 2026-09-02
		{"100.64.0.5/10", "64.100.in-addr.arpa"},    // /10 rounds up to 2 octets (upstream behavior)
		{"10.1.2.3/8", "10.in-addr.arpa"},
		{"192.168.1.7/24", "1.168.192.in-addr.arpa"},
		{"100.96.43.121", "96.100.in-addr.arpa"}, // bare v4 assumes /16
		{"fd00:b14d:0:96::7/64", "6.9.0.0.0.0.0.0.d.4.1.b.0.0.d.f.ip6.arpa"},
	}
	for _, tt := range tests {
		got, err := ReverseZone(tt.in)
		if err != nil {
			t.Errorf("ReverseZone(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ReverseZone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if _, err := ReverseZone("garbage"); err == nil {
		t.Error("expected error for garbage input")
	}
}
