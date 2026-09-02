// scutil.go holds the PURE parts of the macOS dynamic-store DNS writer:
// key naming, domain batching, script rendering and output parsing. No build
// tag so the golden tests run on every OS; the exec wiring lives in
// platform_darwin.go.
//
// Key-naming contract (see CLAUDE.md Decisions 2026-09-02): every key we
// write is `State:/Network/Service/multibird-<instance>-(Match|Search)-<n>/DNS`.
// The `multibird-` prefix guarantees stock netbird's removeKeysContaining /
// discoverExistingKeys (which act on `NetBird-*`) never touch our keys, and
// we never touch theirs.
package platform

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"github.com/magnetrong/multibird/internal/hostdns"
)

const dynStorePrefix = "State:/Network/Service/multibird-"

// Mirror upstream host_darwin.go v0.77.1: scutil d.add takes at most 99
// values, upstream stays at 50 domains and ~1500 bytes per key to keep under
// scutil's undocumented value-buffer limit.
const (
	maxDomainsPerKey     = 50
	maxDomainBytesPerKey = 1500
)

// dnsKey is one dynamic-store key to write.
type dnsKey struct {
	Key      string
	Domains  []string
	NoSearch bool // 1 for match-only keys, 0 for search-domain keys
}

// hostDNSKeys lays out the spec into concrete keys, batching domains.
func hostDNSKeys(instanceName string, spec hostdns.Spec) []dnsKey {
	var keys []dnsKey
	for n, batch := range batchDomains(spec.SearchDomains) {
		keys = append(keys, dnsKey{
			Key:     fmt.Sprintf("%s%s-Search-%d/DNS", dynStorePrefix, instanceName, n),
			Domains: batch,
		})
	}
	for n, batch := range batchDomains(spec.MatchDomains) {
		keys = append(keys, dnsKey{
			Key:      fmt.Sprintf("%s%s-Match-%d/DNS", dynStorePrefix, instanceName, n),
			Domains:  batch,
			NoSearch: true,
		})
	}
	return keys
}

// batchDomains splits domains into chunks of at most maxDomainsPerKey
// entries and maxDomainBytesPerKey total bytes.
func batchDomains(domains []string) [][]string {
	var out [][]string
	var cur []string
	bytes := 0
	for _, d := range domains {
		if len(cur) > 0 && (len(cur) >= maxDomainsPerKey || bytes+len(d) > maxDomainBytesPerKey) {
			out = append(out, cur)
			cur, bytes = nil, 0
		}
		cur = append(cur, d)
		bytes += len(d)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// renderApplyScript renders one scutil stdin script that removes the
// instance's stale keys and writes the new ones. Dictionary shape mirrors
// upstream addDNSState (host_darwin.go:433): ServerAddresses (array),
// ServerPort (number), SupplementalMatchDomains (array),
// SupplementalMatchDomainsNoSearch (0/1).
func renderApplyScript(stale []string, keys []dnsKey, listen netip.AddrPort) string {
	var b strings.Builder
	for _, k := range stale {
		fmt.Fprintf(&b, "remove %s\n", k)
	}
	for _, k := range keys {
		b.WriteString("d.init\n")
		fmt.Fprintf(&b, "d.add ServerAddresses * %s\n", listen.Addr())
		fmt.Fprintf(&b, "d.add ServerPort # %d\n", listen.Port())
		fmt.Fprintf(&b, "d.add SupplementalMatchDomains * %s\n", strings.Join(k.Domains, " "))
		noSearch := 0
		if k.NoSearch {
			noSearch = 1
		}
		fmt.Fprintf(&b, "d.add SupplementalMatchDomainsNoSearch # %d\n", noSearch)
		fmt.Fprintf(&b, "set %s\n", k.Key)
	}
	b.WriteString("quit\n")
	return b.String()
}

// renderRemoveScript removes the given keys.
func renderRemoveScript(keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "remove %s\n", k)
	}
	b.WriteString("quit\n")
	return b.String()
}

// listScript lists every multibird-owned DNS key (scutil list takes a
// regex pattern).
func listScript() string {
	return "list " + regexp.QuoteMeta(dynStorePrefix) + ".*/DNS\nquit\n"
}

// parseListedKeys extracts key names from `scutil list` output, whose lines
// look like `  subKey [0] = State:/Network/Service/multibird-vpn-Match-0/DNS`.
func parseListedKeys(out string) []string {
	var keys []string
	for _, line := range strings.Split(out, "\n") {
		if _, after, ok := strings.Cut(line, "= "); ok {
			key := strings.TrimSpace(after)
			if strings.HasPrefix(key, dynStorePrefix) {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

var keyRe = regexp.MustCompile(`^` + regexp.QuoteMeta(dynStorePrefix) + `(.+)-(?:Match|Search)-\d+/DNS$`)

// ownerOfKey extracts the instance name from one of our keys ("" if the key
// doesn't match the contract).
func ownerOfKey(key string) string {
	m := keyRe.FindStringSubmatch(key)
	if m == nil {
		return ""
	}
	return m[1]
}

// ownersFromKeys returns the sorted, deduplicated instance names owning keys.
func ownersFromKeys(keys []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		if o := ownerOfKey(k); o != "" && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out
}

// keysOfInstance filters keys down to those owned by one instance.
func keysOfInstance(keys []string, instanceName string) []string {
	var out []string
	for _, k := range keys {
		if ownerOfKey(k) == instanceName {
			out = append(out, k)
		}
	}
	return out
}
