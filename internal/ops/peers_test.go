package ops

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/netbirdio/netbird/client/proto"
)

var handshake = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func peerStatus(peers ...*proto.PeerState) *proto.StatusResponse {
	return &proto.StatusResponse{FullStatus: &proto.FullStatus{Peers: peers}}
}

func TestPeersFromStatus(t *testing.T) {
	tests := []struct {
		name string
		st   *proto.StatusResponse
		want []Peer
	}{
		{
			name: "nil status yields no peers, not a panic",
			st:   nil,
			want: []Peer{},
		},
		{
			name: "empty full status yields no peers",
			st:   peerStatus(),
			want: []Peer{},
		},
		{
			name: "sorted by name, not by daemon order",
			st: peerStatus(
				&proto.PeerState{Fqdn: "nas.mesh.example", IP: "100.96.0.9", ConnStatus: "Connected"},
				&proto.PeerState{Fqdn: "aaa.mesh.example", IP: "100.96.0.2", ConnStatus: "Idle"},
			),
			want: []Peer{
				{Name: "aaa.mesh.example", IP: "100.96.0.2", Status: "Idle"},
				{Name: "nas.mesh.example", IP: "100.96.0.9", Status: "Connected"},
			},
		},
		{
			name: "same name ties break on address",
			st: peerStatus(
				&proto.PeerState{Fqdn: "dup.mesh.example", IP: "100.96.0.20"},
				&proto.PeerState{Fqdn: "dup.mesh.example", IP: "100.96.0.3"},
			),
			want: []Peer{
				{Name: "dup.mesh.example", IP: "100.96.0.20"},
				{Name: "dup.mesh.example", IP: "100.96.0.3"},
			},
		},
		{
			name: "a prefix on the peer address is stripped",
			st: peerStatus(
				&proto.PeerState{Fqdn: "pi.mesh.example", IP: "100.96.0.7/16", ConnStatus: "Connected"},
			),
			want: []Peer{
				{Name: "pi.mesh.example", IP: "100.96.0.7", Status: "Connected"},
			},
		},
		{
			name: "relayed, ipv6 and a real handshake are carried through",
			st: peerStatus(
				&proto.PeerState{
					Fqdn: "vps.mesh.example", IP: "100.96.0.2", Ipv6: "fd00::2",
					ConnStatus: "Connected", Relayed: true,
					LastWireguardHandshake: timestamppb.New(handshake),
				},
			),
			want: []Peer{
				{
					Name: "vps.mesh.example", IP: "100.96.0.2", IPv6: "fd00::2",
					Status: "Connected", Relayed: true, LastHandshake: &handshake,
				},
			},
		},
		{
			name: "never-handshaked peers report no handshake, not 1970",
			st: peerStatus(
				&proto.PeerState{Fqdn: "a.mesh.example", IP: "100.96.0.4", ConnStatus: "Idle"},
				&proto.PeerState{
					Fqdn: "b.mesh.example", IP: "100.96.0.5", ConnStatus: "Idle",
					LastWireguardHandshake: &timestamppb.Timestamp{},
				},
			),
			want: []Peer{
				{Name: "a.mesh.example", IP: "100.96.0.4", Status: "Idle"},
				{Name: "b.mesh.example", IP: "100.96.0.5", Status: "Idle"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := peersFromStatus(tt.st)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d peers, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if !peerEqual(got[i], tt.want[i]) {
					t.Errorf("peer %d:\n got %+v (handshake %v)\nwant %+v (handshake %v)",
						i, got[i], got[i].LastHandshake, tt.want[i], tt.want[i].LastHandshake)
				}
			}
		})
	}
}

// peerEqual compares peers by value, dereferencing LastHandshake so two
// distinct pointers to the same instant compare equal.
func peerEqual(a, b Peer) bool {
	if a.Name != b.Name || a.IP != b.IP || a.IPv6 != b.IPv6 ||
		a.Status != b.Status || a.Relayed != b.Relayed {
		return false
	}
	switch {
	case a.LastHandshake == nil && b.LastHandshake == nil:
		return true
	case a.LastHandshake == nil || b.LastHandshake == nil:
		return false
	default:
		return a.LastHandshake.Equal(*b.LastHandshake)
	}
}
