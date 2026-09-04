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
- Two instances against two different management servers are simultaneously `Connected`, each with its own interface and socket; `multibird status` shows both correctly. ✅ **VERIFIED 2026-08-31 (macOS)**: stock netbird (work mesh) + a multibird instance (netbird.io cloud) carried simultaneous traffic, 0% loss on the stock side. Took v0.2.1–v0.2.6 fixes to get there — see the Decisions log.
- `go test ./...` passes on a machine with no netbird installed. ✅
- `multibird doctor` correctly flags a netbird version outside TESTED_VERSIONS. ✅ verified in the field (flagged a 0.72.2 install).
- `multibird nuke` recovers a manually-killed daemon's leftovers and is idempotent. ✅ integration-tested + used in anger.
- Stock netbird (default profile) keeps working untouched throughout. ✅ verified on macOS (after the v0.2.5 legacy-routing fix; advanced routing upstream is NOT multi-daemon-safe).

## v0.2 — conflict safety & persistence

In priority order (1–3 land before boot persistence: persistence multiplies the cost
of preflight gaps, since conflicts would then happen unattended at boot):

1. Route-overlap preflight: routed-prefix overlap detection across instances
   (`ListNetworks` gRPC) with an OS routing-table "who wins" report
2. DNS-management conflict preflight with per-instance disable-DNS / split-domain
   guidance; `multibird set <name>` to toggle instance settings. ("No
   auto-arbitration" superseded on macOS 2026-09-02: `dns_mode=multibird` makes
   multibird the darwin DNS arbiter — see docs/dns.md and the Decisions log.)
3. `logs <name> [-f]` tailing per-instance daemon log files
4. SSO login (`WaitSSOLogin` browser flow); `up --all` fails SSO-pending instances
   with a clear message and continues the others
5. Boot persistence: `install`/`uninstall` (launchd LaunchDaemon on macOS — agents
   can't create utun devices — systemd units on Linux; units invoke multibird so
   preflight always runs)
6. Housekeeping: friendlier permission-denied errors (point at sudo), shell
   completions

Acceptance criteria:
- Bringing up a second mesh that routes an overlapping prefix produces a loud, actionable warning naming the winning instance; `--strict` refuses. (implemented; not yet exercised against two real meshes with overlapping routes)
- An SSO-only NetBird account can be added and brought up with no setup key. ✅ **VERIFIED 2026-08-31** against app.netbird.io (browser PKCE flow, including retry after pending email verification).
- A macOS and a Linux box both reconnect all installed instances after reboot with no manual step. (implemented; reboot behavior NOT yet tested on either OS)
- Docs `dns.md` / `privileges.md` reflect verified behavior, not assumptions.
- macOS DNS arbitration: with stock netbird (mesh A) + a multibird-mode instance
  (mesh B) on one Mac, names of BOTH meshes resolve simultaneously, keep resolving
  across network changes and down/up, and `down|remove|nuke` leave no multibird-*
  DNS keys (`doctor` reports strays). ✅ **VERIFIED 2026-09-03**: stock netbird
  (netbird.selfhosted) + multibird instance (mesh.magnetrong.com) both resolved via
  dscacheutil after a Wi-Fi toggle; `down` removed all multibird-* keys. The
  registered resolvers point at the daemon's in-tunnel address
  (`nameserver 100.96.255.254 port 53`, Supplemental flags) — see docs/dns.md for
  why (userspace-bind ServiceViaMemory; took the 2026-09-03 corrections to land).

Resolved after v0.2.10:
- **Stock-install isolation was incomplete on BOTH platforms** — `--config` alone
  does not isolate a daemon from the host's netbird profile registry, so on a host
  with a named stock profile active a multibird daemon loaded *and rewrote* the
  stock install's config. Fixed with a per-instance `NB_STATE_DIR`; see the
  2026-09-04 Decisions entry. Verified on Linux against a live stock install
  (`Profile: vpn`, `vpn.magnetrong.com`): state byte-identical after runs.
  **Still to confirm on macOS** — the mechanism is platform-neutral (`NB_STATE_DIR`
  is read in an unguarded `init()`, same `/var/lib/netbird` default) but it has not
  been exercised on a Mac.

Open questions carried out of v0.2 field testing:
- **Linux multi-instance routing**: does netbird's Linux advanced routing (policy
  tables) have the same multi-daemon collision the macOS scoped-default flush had
  (fixed via NB_USE_LEGACY_ROUTING on darwin)? Two daemons now start cleanly with
  isolated state, but this has NOT been tested with two real meshes and overlapping
  routes; platform.DaemonEnv is the hook if Linux needs the same treatment.
- **Upstream**: consider filing an issue on netbirdio/netbird — Login silently
  ignores config fields (surprising API), and flushScopedDefaults assumes a single
  daemon per host.

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
- Group-based socket access (0660 + `multibird` group) so read-only commands work
  without sudo — security-sensitive (an open control socket ≈ root); stock netbird
  has the same sudo ergonomics, so this stays parked until there's real demand
