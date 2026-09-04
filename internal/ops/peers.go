package ops

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/magnetrong/multibird/internal/daemon"
	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbgrpc"

	"github.com/netbirdio/netbird/client/proto"
)

// Peer is one remote peer of a mesh, as that mesh's daemon reports it.
type Peer struct {
	Name          string     `json:"name"` // FQDN, e.g. nas.mesh.example
	IP            string     `json:"ip"`   // mesh address, no prefix length
	IPv6          string     `json:"ipv6,omitempty"`
	Status        string     `json:"status"` // Connected | Idle | ...
	Relayed       bool       `json:"relayed"`
	LastHandshake *time.Time `json:"last_handshake,omitempty"` // nil: never
}

// InstancePeers is one instance's peer list. State mirrors
// InstanceStatus.State so that an empty Peers is explainable (daemon stopped,
// engine still connecting) instead of ambiguous.
type InstancePeers struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Peers []Peer `json:"peers"`
}

// Peers collects the peer lists of the given instances. An unreachable
// instance yields its state and no peers rather than an error — one dead
// instance must not hide the other meshes' addresses.
func (e *Env) Peers(ctx context.Context, insts []*instance.Instance) []InstancePeers {
	out := make([]InstancePeers, 0, len(insts))
	for _, inst := range insts {
		p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
		// Peers stays non-nil so --json emits [] rather than null for an
		// instance that reported nothing.
		g := InstancePeers{Name: inst.Name, State: "stopped", Peers: []Peer{}}
		if daemon.Running(p) {
			g.State = "daemon-only"
			if c, err := nbgrpc.Dial(p.SocketPath); err == nil {
				sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if st, err := c.Status(sctx); err == nil {
					g.State = st.GetStatus()
					g.Peers = peersFromStatus(st)
				}
				cancel()
				c.Close() //nolint:gosec // best-effort close of a status probe
			}
		}
		out = append(out, g)
	}
	return out
}

// peersFromStatus maps a daemon status onto the peer view, sorted by name:
// the question this answers is "what address does <peer> have?". Pure
// function of its input — table-driven-tested, no daemon required.
func peersFromStatus(st *proto.StatusResponse) []Peer {
	ps := st.GetFullStatus().GetPeers()
	out := make([]Peer, 0, len(ps))
	for _, p := range ps {
		// Peer IPs come through bare today, unlike LocalPeerState's
		// "100.96.43.121/16"; strip a prefix anyway so the column is always
		// a copy-pasteable address.
		ip, _, _ := strings.Cut(p.GetIP(), "/")
		peer := Peer{
			Name:    p.GetFqdn(),
			IP:      ip,
			IPv6:    p.GetIpv6(),
			Status:  p.GetConnStatus(),
			Relayed: p.GetRelayed(),
		}
		// A never-connected peer carries a zero (or unset) timestamp, not a
		// 1970 handshake.
		if hs := p.GetLastWireguardHandshake(); hs.IsValid() && hs.GetSeconds() > 0 {
			t := hs.AsTime()
			peer.LastHandshake = &t
		}
		out = append(out, peer)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].IP < out[j].IP
	})
	return out
}
