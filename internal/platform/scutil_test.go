package platform

import (
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/magnetrong/multibird/internal/hostdns"
)

func TestRenderApplyScriptGolden(t *testing.T) {
	spec := hostdns.Spec{
		SearchDomains: []string{"mesh.magnetrong.com"},
		MatchDomains:  []string{"96.100.in-addr.arpa", "home.magnetrong.com", "mesh.magnetrong.com"},
	}
	keys := hostDNSKeys("vpn", spec)
	got := renderApplyScript(
		[]string{"State:/Network/Service/multibird-vpn-Match-0/DNS"},
		keys,
		netip.MustParseAddrPort("127.0.0.1:5300"),
	)
	want := `remove State:/Network/Service/multibird-vpn-Match-0/DNS
d.init
d.add ServerAddresses * 127.0.0.1
d.add ServerPort # 5300
d.add SupplementalMatchDomains * mesh.magnetrong.com
d.add SupplementalMatchDomainsNoSearch # 0
set State:/Network/Service/multibird-vpn-Search-0/DNS
d.init
d.add ServerAddresses * 127.0.0.1
d.add ServerPort # 5300
d.add SupplementalMatchDomains * 96.100.in-addr.arpa home.magnetrong.com mesh.magnetrong.com
d.add SupplementalMatchDomainsNoSearch # 1
set State:/Network/Service/multibird-vpn-Match-0/DNS
quit
`
	if got != want {
		t.Errorf("script mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderRemoveScript(t *testing.T) {
	got := renderRemoveScript([]string{
		"State:/Network/Service/multibird-vpn-Match-0/DNS",
		"State:/Network/Service/multibird-vpn-Search-0/DNS",
	})
	want := "remove State:/Network/Service/multibird-vpn-Match-0/DNS\nremove State:/Network/Service/multibird-vpn-Search-0/DNS\nquit\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBatchDomains(t *testing.T) {
	long := strings.Repeat("x", 400) + ".example"
	tests := []struct {
		name    string
		domains []string
		want    [][]int // batch sizes
	}{
		{"empty", nil, nil},
		{"one batch", []string{"a.example", "b.example"}, [][]int{{2}}},
		{"splits at 50 domains", manyDomains(120), [][]int{{50}, {50}, {20}}},
		{"splits at byte cap", []string{long, long, long, long}, [][]int{{3}, {1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := batchDomains(tt.domains)
			var sizes [][]int
			total := 0
			for _, b := range got {
				sizes = append(sizes, []int{len(b)})
				total += len(b)
			}
			if !reflect.DeepEqual(sizes, tt.want) {
				t.Errorf("batch sizes %v, want %v", sizes, tt.want)
			}
			if total != len(tt.domains) {
				t.Errorf("lost domains: %d in, %d out", len(tt.domains), total)
			}
		})
	}
}

func manyDomains(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("d%03d.example", i)
	}
	return out
}

func TestParseListedKeysAndOwners(t *testing.T) {
	out := `  subKey [0] = State:/Network/Service/multibird-vpn-Match-0/DNS
  subKey [1] = State:/Network/Service/multibird-vpn-Search-0/DNS
  subKey [2] = State:/Network/Service/multibird-my-lab-2-Match-1/DNS
  subKey [3] = State:/Network/Service/NetBird-Match-0/DNS
noise line
`
	keys := parseListedKeys(out)
	if len(keys) != 3 {
		t.Fatalf("parsed %d keys, want 3 (NetBird-* must be excluded): %v", len(keys), keys)
	}
	owners := ownersFromKeys(keys)
	if !reflect.DeepEqual(owners, []string{"my-lab-2", "vpn"}) {
		t.Errorf("owners = %v, want [my-lab-2 vpn]", owners)
	}
	vpnKeys := keysOfInstance(keys, "vpn")
	if len(vpnKeys) != 2 {
		t.Errorf("vpn owns %v, want 2 keys", vpnKeys)
	}
	// An instance name that is a prefix of another must not steal keys.
	if got := keysOfInstance(keys, "my-lab"); len(got) != 0 {
		t.Errorf("prefix instance stole keys: %v", got)
	}
}

func TestListScript(t *testing.T) {
	got := listScript()
	if !strings.Contains(got, "list State:/Network/Service/multibird-") || !strings.HasSuffix(got, "quit\n") {
		t.Errorf("unexpected list script: %q", got)
	}
}

func TestHostDNSKeysEmptySpec(t *testing.T) {
	if keys := hostDNSKeys("vpn", hostdns.Spec{}); len(keys) != 0 {
		t.Errorf("empty spec should produce no keys, got %v", keys)
	}
}
