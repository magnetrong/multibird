// Package daemon owns the LIFECYCLE of per-instance `netbird service run`
// processes: spawn, stop, health-check, and forceful cleanup (nuke). It is
// the only lifecycle mechanism — control and status go through
// internal/nbgrpc, never through signals or CLI beyond what's here.
package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/magnetrong/multibird/internal/instance"
	"github.com/magnetrong/multibird/internal/nbcli"
)

// LogLevel for spawned daemons.
const LogLevel = "info"

// Start spawns the instance's daemon detached (its own session, survives us)
// and records the pid. No-op if the daemon is already running.
func Start(inst *instance.Instance, p instance.Params) error {
	if pid, ok := ReadPID(p); ok && Alive(pid) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.SocketPath), 0o755); err != nil {
		return fmt.Errorf("creating run dir %s: %w (daemons need root — try sudo, see docs/privileges.md)", filepath.Dir(p.SocketPath), err)
	}
	// A stale socket from a crashed daemon prevents the new one from binding.
	_ = os.Remove(p.SocketPath)

	cmd := nbcli.New(inst.NetbirdBin).ServiceRunCmd(p.ConfigJSON, p.DaemonAddr, p.LogFile, LogLevel)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning netbird daemon for %q: %w — is netbird installed (multibird doctor)?", inst.Name, err)
	}
	pid := cmd.Process.Pid
	// Detach: the daemon runs in its own session; we must not wait on it,
	// but releasing avoids a zombie if it exits before we do.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("detaching daemon process: %w", err)
	}
	if err := os.WriteFile(p.PIDFile, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("writing pid file %s: %w", p.PIDFile, err)
	}
	// Wait for the socket to appear so callers can dial immediately after.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(p.SocketPath); err == nil {
			return nil
		}
		if !Alive(pid) {
			return fmt.Errorf("daemon for %q exited immediately — check its log: %s", inst.Name, p.LogFile)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon for %q did not create %s within 10s — check its log: %s", inst.Name, p.SocketPath, p.LogFile)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Stop terminates the daemon gracefully (SIGTERM, then SIGKILL after 10s)
// and removes the pid file. No-op if not running.
func Stop(p instance.Params) error {
	pid, ok := ReadPID(p)
	if !ok {
		return nil
	}
	if !Alive(pid) {
		_ = os.Remove(p.PIDFile)
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stopping daemon (pid %d): %w — try sudo, or `multibird nuke`", pid, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for Alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.Remove(p.PIDFile)
	return nil
}

// Running reports whether the instance's daemon process is alive.
func Running(p instance.Params) bool {
	pid, ok := ReadPID(p)
	return ok && Alive(pid)
}

// ReadPID reads the recorded daemon pid.
func ReadPID(p instance.Params) (int, bool) {
	b, err := os.ReadFile(p.PIDFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// Alive reports whether pid exists (signal 0).
func Alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil || errors.Is(syscall.Kill(pid, 0), syscall.EPERM)
}

// Nuke is the forceful, IDEMPOTENT cleanup for crashed or half-up instances:
// kill the process if any, remove the stale socket and pid file. Interface
// teardown is left to the kernel (killing the daemon destroys its TUN device
// on both platforms). Safe to run repeatedly; collects problems instead of
// stopping at the first.
func Nuke(p instance.Params) []error {
	var errs []error
	if pid, ok := ReadPID(p); ok && Alive(pid) {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, fmt.Errorf("killing pid %d: %w (try sudo)", pid, err))
		} else {
			deadline := time.Now().Add(5 * time.Second)
			for Alive(pid) && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	for _, f := range []string{p.SocketPath, p.PIDFile} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("removing %s: %w (try sudo)", f, err))
		}
	}
	return errs
}
