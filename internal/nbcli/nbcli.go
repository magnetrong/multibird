// Package nbcli is the ONLY place in the codebase allowed to exec the netbird
// binary. It exists as the single choke point for CLI-drift risk.
//
// THE CLI CONTRACT — the exhaustive list of netbird CLI surface we depend on
// (mirror of CLAUDE.md "THE CLI CONTRACT"; update both in the same PR):
//
//   - `netbird version`
//     Prints the bare version string on stdout. Used by doctor and status.
//
//   - `netbird service run --config <path> --daemon-addr unix://<path>
//     --log-file <path> --log-level <level>`
//     Runs one daemon per instance. All four flags are persistent root flags
//     in upstream client/cmd/root.go; --daemon-addr accepts unix:// and
//     tcp:// on Linux and macOS.
//
// Nothing else. Interface name, WireGuard port, setup key, management URL and
// DNS toggles are set via the Login gRPC request (internal/nbgrpc), not CLI
// flags. If you need more CLI surface, add it here, cover it in the contract
// test, and update CLAUDE.md — same PR.
package nbcli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultBin is used when an instance doesn't pin its own binary.
const DefaultBin = "netbird"

// Runner runs one specific netbird binary.
type Runner struct {
	Bin string
}

// New returns a Runner for bin, falling back to DefaultBin.
func New(bin string) Runner {
	if bin == "" {
		bin = DefaultBin
	}
	return Runner{Bin: bin}
}

// Version runs `netbird version` and returns the trimmed stdout.
func (r Runner) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, r.Bin, "version").Output()
	if err != nil {
		return "", fmt.Errorf("running `%s version`: %w — is netbird installed and on PATH (or set --netbird-bin)?", r.Bin, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ServiceRunArgs builds the argv (excluding argv[0]) for the per-instance
// daemon. Argument construction is separated from execution so the contract
// test can pin it without running anything.
func ServiceRunArgs(configPath, daemonAddr, logFile, logLevel string) []string {
	return []string{
		"service", "run",
		"--config", configPath,
		"--daemon-addr", daemonAddr,
		"--log-file", logFile,
		"--log-level", logLevel,
	}
}

// ServiceRunCmd returns the (unstarted) daemon process command.
// internal/daemon owns starting, supervising and stopping it.
func (r Runner) ServiceRunCmd(configPath, daemonAddr, logFile, logLevel string) *exec.Cmd {
	return exec.Command(r.Bin, ServiceRunArgs(configPath, daemonAddr, logFile, logLevel)...)
}
