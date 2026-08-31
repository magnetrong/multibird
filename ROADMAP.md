# multibird roadmap

> **Standing rule: re-evaluate the project's existence at every milestone against
> [netbirdio/netbird#446](https://github.com/netbirdio/netbird/issues/446).**
> The moment NetBird ships usable native simultaneous profiles, this project's job
> becomes writing a migration guide and archiving itself.

## v0.1 — core (current)

Multiple isolated NetBird instances on one machine, controllable from one CLI.

- `add`, `up [--all]`, `down [--all]`, `status [--json]`, `list`, `remove [--purge]`, `doctor`, `nuke`
- Hybrid control: process lifecycle via the `netbird` binary, control/status via the daemon gRPC API, CLI fallback confined to `internal/nbcli`
- Per-instance isolation: config dir, unix daemon socket, WireGuard interface name (Linux) / discovered utun (macOS), deterministic WireGuard listen port
- Basic preflight: netbird IP range overlap detection across running instances
- Per-instance netbird binary override
- macOS (arm64/amd64) and Linux (amd64/arm64)

Acceptance criteria:
- Two instances against two different management servers are simultaneously `Connected`, each with its own interface and socket; `multibird status` shows both correctly.
- `go test ./...` passes on a machine with no netbird installed.
- `multibird doctor` correctly flags a netbird version outside TESTED_VERSIONS.
- `multibird nuke` recovers a manually-killed daemon's leftovers and is idempotent.
- Stock netbird (default profile) keeps working untouched throughout.

## v0.2 — conflict safety & persistence

- Full preflight: routed-prefix overlap detection with OS routing-table "who wins" report; DNS-management conflict detection with per-instance disable-DNS / split-domain guidance (no auto-arbitration)
- Boot persistence: `install`/`uninstall` (launchd LaunchAgent/LaunchDaemon, systemd user/system units; units invoke multibird so preflight always runs)
- `logs <name> [-f]` tailing per-instance daemon log files

Acceptance criteria:
- Bringing up a second mesh that routes an overlapping prefix produces a loud, actionable warning naming the winning instance; `--strict` refuses.
- A macOS and a Linux box both reconnect all installed instances after reboot with no manual step.
- Docs `dns.md` / `privileges.md` reflect verified behavior, not assumptions.

## v0.3 — observability & distribution

- `multibird tui` (bubbletea): live state/IP/peers/routes/DNS/logs per instance
- `--json` on every read command
- Weekly NetBird canary in CI runs `doctor --strict` + integration smoke against latest upstream release, files an issue on failure
- Homebrew tap published; goreleaser fully wired

Acceptance criteria:
- TUI survives daemon crashes/restarts without wedging.
- Canary has caught (or demonstrably would catch) a flag/proto drift within a week of an upstream release.
- `brew install <tap>/multibird` works on both macOS architectures.

## v1.0 — hardening & the exit plan

- Hardening pass: fuzz config parsing, race-detector CI, error-message audit ("what to do next" everywhere)
- Docs site
- **Migration guide FROM multibird back to stock NetBird** — the whole point

Acceptance criteria:
- A user can follow the migration guide to move every multibird instance to native NetBird profiles (if shipped) or to stock single-profile use, with zero leftover multibird state.
- No open data-loss or credential-leak issues.

## Parking lot (explicitly not scheduled)

- Windows support
- Exit-node coordination between meshes
- Metrics endpoint (Prometheus)
