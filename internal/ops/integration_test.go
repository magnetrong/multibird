//go:build integration

// Integration smoke test: requires a real netbird binary on PATH and root
// (TUN/socket creation). Never runs in default `go test ./...`.
//
//	sudo go test -tags integration ./...
//
// It exercises the daemon lifecycle + gRPC control surface WITHOUT a
// management server: spawn a daemon with an isolated config/socket, wait for
// gRPC readiness, query Status, stop, and verify nuke idempotency. This is
// exactly the surface that breaks on netbird version drift (CLI flags, proto),
// which is what the CI canary needs to detect.
package ops

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/magnetrong/multibird/internal/daemon"
	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbgrpc"
)

func TestIntegrationDaemonLifecycle(t *testing.T) {
	if _, err := exec.LookPath("netbird"); err != nil {
		t.Skip("netbird binary not on PATH")
	}
	if os.Geteuid() != 0 {
		t.Skip("integration test needs root (run: sudo go test -tags integration ./...)")
	}

	root := t.TempDir()
	run := t.TempDir() // sockets in a temp dir: --daemon-addr accepts any unix path
	inst := &instance.Instance{Name: "itest", Index: 0}
	p := inst.DeriveParams(root, run)

	// Hermetic daemon state. WITHOUT this the daemon resolves the machine's
	// /var/lib/netbird/active_profile.json, and on a box where stock netbird
	// has logged in under the profiles rework that names a NON-default
	// profile — so the daemon loads the STOCK install's config and ignores
	// our --config (verified on 0.77.1: it went on to bring up the stock
	// wt0 interface). The test would then be asserting against the host's
	// netbird setup rather than this instance. That difference is exactly
	// why this test passed on a dev box with netbird state and failed on the
	// clean CI runner.
	stateDir := t.TempDir()
	if err := daemon.Start(inst, p, []string{"NB_STATE_DIR=" + stateDir}); err != nil {
		t.Fatalf("daemon.Start: %v", err)
	}
	t.Cleanup(func() { daemon.Nuke(p) })

	c, err := nbgrpc.Dial(p.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.WaitReady(ctx, 20*time.Second); err != nil {
		t.Fatalf("daemon never became ready: %v", err)
	}
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatalf("status RPC: %v", err)
	}
	t.Logf("daemon status=%q version=%q", st.GetStatus(), st.GetDaemonVersion())

	// The daemon creates its profile's config.json ITSELF at startup
	// (Server.Start → getConfig → profilemanager.ReadConfig, which creates
	// when missing), asynchronously with respect to the gRPC server
	// answering RPCs — so wait for the file instead of racing it. Its
	// appearance at p.ConfigJSON is itself the assertion that --config, our
	// only isolation mechanism for netbird's own state, was honored.
	//
	// This supersedes the older "SetConfig must fail before Login" guard,
	// which raced that same bootstrap: it held only while config.json was
	// still absent. ops.Up's Login → SetConfig → Up order stays correct
	// under either semantics, because Login uses UpdateOrCreateConfig.
	waitForFile(t, p.ConfigJSON, 20*time.Second)

	// The isolation params must land in THIS instance's config.json. If the
	// profiles rework ever resolves the "default" handle to a shared global
	// file, two multibird daemons would silently overwrite each other's
	// interface and port — fail loudly here rather than in the field.
	const wantIface, wantPort = "wt-mb-itest", 51987
	if err := c.SetConfig(ctx, nbgrpc.SetConfigParams{
		ManagementURL: "https://example.invalid",
		InterfaceName: wantIface,
		WireguardPort: wantPort,
		DisableDNS:    true,
	}); err != nil {
		t.Fatalf("SetConfig on the instance's own profile: %v", err)
	}
	raw, err := os.ReadFile(p.ConfigJSON)
	if err != nil {
		t.Fatalf("reading %s: %v", p.ConfigJSON, err)
	}
	// Field names, not json tags: profilemanager.Config carries none.
	var cfg struct {
		WgIface    string
		WgPort     int
		DisableDNS bool
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parsing %s: %v", p.ConfigJSON, err)
	}
	if cfg.WgIface != wantIface || cfg.WgPort != wantPort || !cfg.DisableDNS {
		t.Errorf("SetConfig did not land in the instance's own config.json (%s):\n got WgIface=%q WgPort=%d DisableDNS=%t\nwant WgIface=%q WgPort=%d DisableDNS=true",
			p.ConfigJSON, cfg.WgIface, cfg.WgPort, cfg.DisableDNS, wantIface, wantPort)
	}

	if err := daemon.Stop(p); err != nil {
		t.Fatalf("daemon.Stop: %v", err)
	}
	if daemon.Running(p) {
		t.Fatal("daemon still running after Stop")
	}
	// Nuke must be idempotent, including on an already-clean instance.
	for i := 0; i < 2; i++ {
		if errs := daemon.Nuke(p); len(errs) != 0 {
			t.Fatalf("nuke pass %d: %v", i, errs)
		}
	}
}

// waitForFile blocks until path exists, failing the test if it never does.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared within %s — the daemon did not honor --config", path, timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
