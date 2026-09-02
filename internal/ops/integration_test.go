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
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/magnetrong/multibird/internal/daemon"
	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbgrpc"
	"github.com/magnetrong/multibird/internal/version"
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

	if err := daemon.Start(inst, p, nil); err != nil {
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

	// Guard the Login-before-SetConfig ordering (see ops.Up): on a FRESH
	// daemon with no config.json, SetConfig must fail — in the tested
	// netbird range, SetConfig only updates an existing profile config and
	// Login is what creates it. (Verified version-dependent: 0.76 happily
	// creates the file instead, so only assert in-range.) If this fails on a
	// new in-range version, upstream semantics changed: re-check the
	// ordering in ops.Up before bumping TestedMax.
	if in, verr := version.InTestedRange(st.GetDaemonVersion()); verr == nil && in {
		err = c.SetConfig(ctx, nbgrpc.SetConfigParams{ManagementURL: "https://example.invalid"})
		if err == nil {
			t.Fatal("SetConfig succeeded on a daemon with no config.json — upstream semantics changed, re-verify the Login/SetConfig ordering in ops.Up")
		}
		t.Logf("SetConfig before Login correctly refused: %v", err)
	} else {
		t.Logf("daemon %s outside tested range — skipping SetConfig-ordering guard", st.GetDaemonVersion())
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
