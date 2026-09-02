package preflight

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Route is one routed prefix an instance is (or would be) accepting.
type Route struct {
	Instance string
	Prefix   netip.Prefix
	Selected bool // only selected routes are actually installed
}

// RouteConflict is an overlap between routed prefixes of two instances — the
// classic silent failure: both meshes route 192.168.1.0/24 and one silently
// wins.
type RouteConflict struct {
	A, B Route
	// Winner names the instance whose interface currently owns the OS route
	// for the overlapping destination ("" when undetermined).
	Winner string
}

func (c RouteConflict) String() string {
	s := fmt.Sprintf("instances %q and %q both route overlapping prefixes (%s vs %s)",
		c.A.Instance, c.B.Instance, c.A.Prefix, c.B.Prefix)
	if c.Winner != "" {
		s += fmt.Sprintf(" — the OS routing table currently sends this traffic to %q; the other mesh's hosts in that range are unreachable", c.Winner)
	} else {
		s += " — which mesh wins is up to the OS routing table and may change"
	}
	return s + ". Fix: deselect the route on one side (netbird dashboard / route settings) or narrow the advertised prefixes"
}

// RouteConflicts finds pairwise overlaps between SELECTED routes of different
// instances. Pure logic — the caller resolves winners via the OS routing
// table afterwards.
func RouteConflicts(routes []Route) []RouteConflict {
	var out []RouteConflict
	for i := 0; i < len(routes); i++ {
		for j := i + 1; j < len(routes); j++ {
			a, b := routes[i], routes[j]
			if a.Instance == b.Instance || !a.Selected || !b.Selected {
				continue
			}
			if a.Prefix.Overlaps(b.Prefix) {
				out = append(out, RouteConflict{A: a, B: b})
			}
		}
	}
	return out
}

// DNSIntent describes one running instance's claim on host DNS.
type DNSIntent struct {
	Instance   string
	ManagesDNS bool // instance-level: DNS management not disabled
	// Mode is the instance's dns_mode ("native", "multibird", "disabled") —
	// used only to word conflict messages.
	Mode    string
	Domains []string // match domains served; contains "" (or is empty while managing) => wants to be the primary resolver
}

// primary reports whether the intent amounts to claiming the primary resolver.
func (d DNSIntent) primary() bool {
	if !d.ManagesDNS {
		return false
	}
	if len(d.Domains) == 0 {
		return true // managing DNS with no split domains = primary claim
	}
	for _, dom := range d.Domains {
		if dom == "" || dom == "." {
			return true
		}
	}
	return false
}

// DNSConflict describes a DNS-management fight between instances.
type DNSConflict struct {
	Kind      string // "primary" (≥2 primary claims) or "domain" (same match domain)
	Instances []string
	Domain    string // set for Kind == "domain"
}

func (c DNSConflict) String() string {
	switch c.Kind {
	case "primary":
		return fmt.Sprintf("instances %s all want to manage the host's primary DNS resolver — only one can win (last writer, or arbitrary service order on macOS). Fix: keep DNS on the mesh whose DNS matters most and run the others with `multibird set <name> --dns-mode disabled` (or, on macOS with split domains, `--dns-mode multibird` — see docs/dns.md)",
			strings.Join(c.Instances, ", "))
	default:
		return fmt.Sprintf("instances %s both serve DNS for domain %q — lookups will land on an arbitrary mesh even with multibird arbitration. Fix: make the domain sets disjoint server-side, or disable DNS on one instance (see docs/dns.md)",
			strings.Join(c.Instances, ", "), c.Domain)
	}
}

// DNSConflicts detects primary-resolver fights and overlapping match domains
// across running instances. Pure logic.
func DNSConflicts(intents []DNSIntent) []DNSConflict {
	var out []DNSConflict

	var primaries []string
	for _, d := range intents {
		if d.primary() {
			primaries = append(primaries, d.Instance)
		}
	}
	if len(primaries) > 1 {
		sort.Strings(primaries)
		out = append(out, DNSConflict{Kind: "primary", Instances: primaries})
	}

	byDomain := map[string][]string{}
	for _, d := range intents {
		if !d.ManagesDNS {
			continue
		}
		seen := map[string]bool{}
		for _, dom := range d.Domains {
			dom = strings.TrimSuffix(strings.ToLower(dom), ".")
			if dom == "" || seen[dom] {
				continue
			}
			seen[dom] = true
			byDomain[dom] = append(byDomain[dom], d.Instance)
		}
	}
	var doms []string
	for dom, insts := range byDomain {
		if len(insts) > 1 {
			doms = append(doms, dom)
		}
	}
	sort.Strings(doms)
	for _, dom := range doms {
		insts := byDomain[dom]
		sort.Strings(insts)
		out = append(out, DNSConflict{Kind: "domain", Instances: insts, Domain: dom})
	}
	return out
}
