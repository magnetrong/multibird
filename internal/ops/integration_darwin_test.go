//go:build integration && darwin

// macOS DNS-arbiter integration test. Needs root, a netbird binary, AND a
// real management server + reusable setup key:
//
//	sudo MULTIBIRD_ITEST_MGMT_URL=https://... MULTIBIRD_ITEST_SETUP_KEY=... \
//	  go test -tags integration -run TestIntegrationDarwinDNSArbiter ./internal/ops/
//
// Skipped without the env vars. NOTE: dig bypasses scoped resolvers on macOS
// — this test uses scutil --dns and dscacheutil instead (docs/dns.md).
package ops

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbgrpc"
	"github.com/magnetrong/multibird/internal/platform"
)

func TestIntegrationDarwinDNSArbiter(t *testing.T) {
	mgmt := os.Getenv("MULTIBIRD_ITEST_MGMT_URL")
	key := os.Getenv("MULTIBIRD_ITEST_SETUP_KEY")
	if mgmt == "" || key == "" {
		t.Skip("set MULTIBIRD_ITEST_MGMT_URL and MULTIBIRD_ITEST_SETUP_KEY to run")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("netbird"); err != nil {
		t.Skip("netbird binary not on PATH")
	}

	e := &Env{
		Store:    &instance.Store{Root: t.TempDir(), RunDir: t.TempDir()},
		Platform: platform.New(),
		Warnf:    t.Logf,
		Printf:   t.Logf,
	}
	inst := &instance.Instance{
		Name: "itest-dns", ManagementURL: mgmt, SetupKey: key,
		DNSMode: instance.DNSMultibird,
	}
	if err := e.Add(inst); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { e.Nuke(inst) })

	if err := e.Up(ctx, inst, false); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := e.DNSSync(ctx, inst); err != nil {
		t.Fatalf("dns sync: %v", err)
	}

	out, err := exec.Command("scutil", "--dns").Output()
	if err != nil {
		t.Fatal(err)
	}
	p := inst.DeriveParams(e.Store.Root, e.Store.RunDir)
	if !strings.Contains(string(out), p.DNSListen.Addr().String()) {
		t.Errorf("scutil --dns does not show a resolver at %s:\n%s", p.DNSListen, out)
	}

	// Resolve our own fqdn through the system resolver (NOT dig).
	c, err := nbgrpc.Dial(p.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fqdn := st.GetFullStatus().GetLocalPeerState().GetFqdn()
	if fqdn != "" {
		res, err := exec.Command("dscacheutil", "-q", "host", "-a", "name", fqdn).Output()
		if err != nil || !strings.Contains(string(res), "ip_address") {
			t.Errorf("dscacheutil could not resolve %s: %v\n%s", fqdn, err, res)
		}
	}

	if err := e.Down(ctx, inst); err != nil {
		t.Fatalf("down: %v", err)
	}
	owners, err := e.Platform.ListHostDNSOwners()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range owners {
		if o == inst.Name {
			t.Errorf("host DNS keys for %q survive down", inst.Name)
		}
	}
}
