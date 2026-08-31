// Package version holds multibird's own version and the range of netbird
// versions this build has been tested against.
//
// POLICY (see CLAUDE.md "Version policy"): TestedMax MUST equal the
// github.com/netbirdio/netbird pin in go.mod. CI enforces this via
// scripts/check-version-coupling.sh.
package version

import (
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
)

// Version is multibird's own version, injected by goreleaser.
var Version = "dev"

// Full returns Version plus, for source builds, the VCS revision baked in by
// the Go toolchain — so `multibird --version` always identifies the exact
// commit a binary was built from.
func Full() string {
	v := Version
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		v += " (" + rev + dirty + ")"
	}
	return v
}

const (
	// TestedMin is the oldest netbird release multibird is known to work with.
	TestedMin = "0.77.0"
	// TestedMax is the newest tested netbird release; must equal the go.mod pin.
	TestedMax = "0.77.1"
)

// InTestedRange reports whether a netbird version string (e.g. "0.77.1" or
// "v0.77.1") falls within [TestedMin, TestedMax]. Unparseable versions return
// an error so callers can warn rather than guess.
func InTestedRange(v string) (bool, error) {
	c, err := parse(v)
	if err != nil {
		return false, err
	}
	lo, _ := parse(TestedMin)
	hi, _ := parse(TestedMax)
	return cmp(c, lo) >= 0 && cmp(c, hi) <= 0, nil
}

func parse(v string) ([3]int, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// tolerate suffixes like "0.78.0-dev"
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("not a semver x.y.z version: %q", v)
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, fmt.Errorf("not a semver x.y.z version: %q", v)
		}
		out[i] = n
	}
	return out, nil
}

func cmp(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
