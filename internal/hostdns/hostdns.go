// Package hostdns derives, from a daemon's live status, the scoped-resolver
// specification that multibird registers with the host on behalf of an
// instance running in the "multibird" DNS mode (the macOS DNS arbiter — see
// docs/dns.md). Pure logic: no gRPC, no OS calls.
package hostdns

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/netbirdio/netbird/client/proto"
)

// Spec is everything the host needs to route the right lookups to one
// instance's resolver.
type Spec struct {
	// Listen is the instance's fixed resolver address (127.0.0.1:<port>).
	Listen netip.AddrPort
	// SearchDomains complete bare hostnames (the peer domain).
	SearchDomains []string
	// MatchDomains are routed to this resolver without affecting search:
	// nameserver-group domains plus the mesh's reverse zones.
	MatchDomains []string
}

// ErrPrimaryClaim is returned when the instance's nameserver groups claim the
// primary resolver (an empty / "." match domain, i.e. route-all DNS).
// multibird refuses to arbitrate that: writing a primary resolver into the
// dynamic store would fight the host's own DNS exactly like the upstream bug
// we are working around. The fix is `--dns-mode native` on AT MOST one
// instance (preflight enforces the "at most one" part).
var ErrPrimaryClaim = fmt.Errorf("this mesh claims ALL DNS (a nameserver group with no match domains) — multibird-arbitrated DNS only handles split domains; use `multibird set <name> --dns-mode native` on exactly one instance, or scope the nameserver group to domains in the dashboard")

// Derive builds the Spec for one instance from its daemon's full status.
// The status must carry a local peer IP (engine up); callers skip and retry
// otherwise.
func Derive(st *proto.StatusResponse, listen netip.AddrPort) (Spec, error) {
	fs := st.GetFullStatus()
	spec := Spec{Listen: listen}

	seen := map[string]bool{}
	addMatch := func(d string) {
		d = canon(d)
		if d != "" && !seen[d] {
			seen[d] = true
			spec.MatchDomains = append(spec.MatchDomains, d)
		}
	}

	// Peer domain: fqdn minus its first label, as search + match domain.
	if fqdn := canon(fs.GetLocalPeerState().GetFqdn()); fqdn != "" {
		if _, rest, ok := strings.Cut(fqdn, "."); ok && rest != "" {
			spec.SearchDomains = []string{rest}
			addMatch(rest)
		}
	}

	// Nameserver-group match domains (enabled groups only).
	for _, g := range fs.GetDnsServers() {
		if !g.GetEnabled() {
			continue
		}
		if len(g.GetDomains()) == 0 {
			// A nameserver group with no match domains is route-all DNS.
			return Spec{}, ErrPrimaryClaim
		}
		for _, d := range g.GetDomains() {
			if canon(d) == "" {
				return Spec{}, ErrPrimaryClaim
			}
			addMatch(d)
		}
	}

	// Reverse zones for the mesh's own ranges, so PTR lookups reach the mesh.
	for _, ipStr := range []string{fs.GetLocalPeerState().GetIP(), fs.GetLocalPeerState().GetIpv6()} {
		if ipStr == "" {
			continue
		}
		if zone, err := ReverseZone(ipStr); err == nil {
			addMatch(zone)
		}
	}

	sort.Strings(spec.MatchDomains)
	return spec, nil
}

// canon lowercases and strips the trailing dot ("." canonicalizes to "").
func canon(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

// ReverseZone computes the in-addr.arpa / ip6.arpa zone for a peer address
// in CIDR form ("100.96.43.121/16"); a bare IP assumes /16 (v4) or /64 (v6).
// Mirrors upstream generateReverseZoneName (client/internal/dns.go,
// v0.77.1): round the mask up to whole octets (v4) or nibbles (v6) of the
// MASKED network address.
func ReverseZone(s string) (string, error) {
	pfx, err := netip.ParsePrefix(s)
	if err != nil {
		addr, aerr := netip.ParseAddr(s)
		if aerr != nil {
			return "", fmt.Errorf("unparseable peer address %q: %w", s, err)
		}
		bits := 16
		if addr.Is6() {
			bits = 64
		}
		pfx, err = addr.Prefix(bits)
		if err != nil {
			return "", err
		}
	}
	ip := pfx.Masked().Addr().Unmap()
	bits := pfx.Bits()

	if ip.Is4() {
		octetsToUse := (bits + 7) / 8
		o := ip.As4()
		parts := make([]string, 0, octetsToUse)
		for i := octetsToUse - 1; i >= 0; i-- {
			parts = append(parts, fmt.Sprintf("%d", o[i]))
		}
		return strings.Join(parts, ".") + ".in-addr.arpa", nil
	}

	nibblesToUse := (bits + 3) / 4
	raw := ip.As16()
	parts := make([]string, 0, nibblesToUse)
	for i := nibblesToUse - 1; i >= 0; i-- {
		b := raw[i/2]
		if i%2 == 0 {
			b >>= 4
		} else {
			b &= 0x0f
		}
		parts = append(parts, fmt.Sprintf("%x", b))
	}
	return strings.Join(parts, ".") + ".ip6.arpa", nil
}
