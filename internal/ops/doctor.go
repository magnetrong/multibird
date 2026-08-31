package ops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/OWNER/multibird/internal/daemon"
	"github.com/OWNER/multibird/internal/nbcli"
	"github.com/OWNER/multibird/internal/version"
)

// Check is one doctor finding.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"` // true = warning (fatal under --strict)
	Detail string `json:"detail"`
}

// Doctor runs environment sanity checks: binary presence and version range
// (version-drift defense), run-dir writability, and leftover state.
func (e *Env) Doctor(ctx context.Context) []Check {
	var out []Check

	insts, err := e.Store.List()
	if err != nil {
		out = append(out, Check{Name: "config root", OK: false, Detail: err.Error()})
		insts = nil
	}

	// Every distinct binary in play: PATH default + per-instance overrides.
	bins := map[string]bool{nbcli.DefaultBin: true}
	for _, i := range insts {
		if i.NetbirdBin != "" {
			bins[i.NetbirdBin] = true
		}
	}
	for bin := range bins {
		name := fmt.Sprintf("netbird binary (%s)", bin)
		v, err := nbcli.New(bin).Version(ctx)
		if err != nil {
			out = append(out, Check{Name: name, OK: false,
				Detail: fmt.Sprintf("%v — install netbird (https://docs.netbird.io) or fix the per-instance --netbird-bin path", err)})
			continue
		}
		in, err := version.InTestedRange(v)
		switch {
		case err != nil:
			out = append(out, Check{Name: name, OK: true, Warn: true,
				Detail: fmt.Sprintf("version %q is unparseable (%v); tested range is %s–%s", v, err, version.TestedMin, version.TestedMax)})
		case !in:
			out = append(out, Check{Name: name, OK: true, Warn: true,
				Detail: fmt.Sprintf("version %s is outside the tested range %s–%s — multibird may misbehave; pin a tested binary per instance with --netbird-bin, or update multibird", v, version.TestedMin, version.TestedMax)})
		default:
			out = append(out, Check{Name: name, OK: true, Detail: fmt.Sprintf("version %s (tested range %s–%s)", v, version.TestedMin, version.TestedMax)})
		}
	}

	// Run dir: sockets/pids live here; daemons (root) create it, but flag
	// obvious problems early.
	run := e.Store.RunDir
	if fi, err := os.Stat(run); err == nil && !fi.IsDir() {
		out = append(out, Check{Name: "run dir", OK: false, Detail: run + " exists but is not a directory — remove it"})
	} else if err == nil {
		out = append(out, Check{Name: "run dir", OK: true, Detail: run + " exists"})
	} else {
		out = append(out, Check{Name: "run dir", OK: true, Warn: true,
			Detail: run + " does not exist yet — it is created on first `sudo multibird up` (root required, see docs/privileges.md)"})
	}

	// Leftover state: pid files whose process is gone, sockets with no daemon.
	leftovers := 0
	for _, i := range insts {
		p := i.DeriveParams(e.Store.Root, e.Store.RunDir)
		if pid, ok := daemon.ReadPID(p); ok && !daemon.Alive(pid) {
			leftovers++
			out = append(out, Check{Name: "leftover state", OK: true, Warn: true,
				Detail: fmt.Sprintf("instance %q has a stale pid file (%s, pid %d dead) — run `multibird nuke %s`", i.Name, p.PIDFile, pid, i.Name)})
		}
		if _, err := os.Stat(p.SocketPath); err == nil && !daemon.Running(p) {
			leftovers++
			out = append(out, Check{Name: "leftover state", OK: true, Warn: true,
				Detail: fmt.Sprintf("instance %q has a stale socket (%s) — run `multibird nuke %s`", i.Name, p.SocketPath, i.Name)})
		}
	}
	if leftovers == 0 {
		out = append(out, Check{Name: "leftover state", OK: true, Detail: "none"})
	}

	// Orphaned files in the run dir that match no known instance.
	if entries, err := os.ReadDir(run); err == nil {
		known := map[string]bool{}
		for _, i := range insts {
			known[i.Name+".sock"] = true
			known[i.Name+".pid"] = true
		}
		for _, en := range entries {
			if !known[en.Name()] {
				out = append(out, Check{Name: "leftover state", OK: true, Warn: true,
					Detail: fmt.Sprintf("unknown file %s in run dir — from a removed instance? delete it manually", filepath.Join(run, en.Name()))})
			}
		}
	}

	return out
}
