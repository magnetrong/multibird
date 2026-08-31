// Package preflight detects conflicts between instances BEFORE (or right as)
// they come up together: overlapping netbird IP ranges (v0.1), overlapping
// routed prefixes and DNS-management fights (v0.2 — see ROADMAP.md and
// docs/dns.md).
//
// Everything here is pure logic over plain inputs so it can be table-driven
// tested with fake fixtures — no gRPC, no OS calls.
package preflight

import (
	"fmt"
	"net/netip"
)

// Net is one instance's claim on an IP range.
type Net struct {
	Instance string
	Prefix   netip.Prefix
}

// Conflict is a detected overlap between two instances.
type Conflict struct {
	A, B Net
}

func (c Conflict) String() string {
	return fmt.Sprintf("instances %q (%s) and %q (%s) have overlapping netbird IP ranges — traffic will silently route to the wrong mesh; give one mesh a different network range in its management server, or keep only one up",
		c.A.Instance, c.A.Prefix, c.B.Instance, c.B.Prefix)
}

// ParseAddr parses a netbird local address as reported by the daemon —
// either "100.92.14.7/16" or a bare IP (assumed /16, netbird's default
// network size).
func ParseAddr(instanceName, addr string) (Net, error) {
	if p, err := netip.ParsePrefix(addr); err == nil {
		return Net{Instance: instanceName, Prefix: p.Masked()}, nil
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return Net{}, fmt.Errorf("instance %q reported unparseable netbird address %q: %w", instanceName, addr, err)
	}
	bits := 16
	if ip.Is6() {
		bits = 64
	}
	p, err := ip.Prefix(bits)
	if err != nil {
		return Net{}, fmt.Errorf("deriving prefix for %q: %w", addr, err)
	}
	return Net{Instance: instanceName, Prefix: p}, nil
}

// IPRangeConflicts returns every pairwise overlap among the given nets.
func IPRangeConflicts(nets []Net) []Conflict {
	var out []Conflict
	for i := 0; i < len(nets); i++ {
		for j := i + 1; j < len(nets); j++ {
			if nets[i].Prefix.Overlaps(nets[j].Prefix) {
				out = append(out, Conflict{A: nets[i], B: nets[j]})
			}
		}
	}
	return out
}
