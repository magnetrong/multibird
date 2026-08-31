# multibird

Run multiple [NetBird](https://netbird.io) VPN meshes on one machine, at the same time.

## The problem

NetBird supports profiles, but only **one profile can be connected at a time**. If your
laptop needs to live on both a company NetBird mesh and a personal/home NetBird mesh —
always, simultaneously — stock NetBird can't do it today. Native simultaneous profiles
are tracked upstream in [netbirdio/netbird#446](https://github.com/netbirdio/netbird/issues/446),
which remains open.

multibird fills the gap: it spawns one fully isolated NetBird daemon per mesh — its own
config directory, its own daemon socket, its own WireGuard interface and listen port —
and gives you one CLI to manage them all.

## This project intends to become obsolete

That is a design goal, not a disclaimer. multibird is deliberately thin and stateless
where possible: NetBird's own `config.json` stays untouched and owned by NetBird, we
never invent proprietary config formats where NetBird's can be reused, and every
milestone re-evaluates the project against upstream #446. When NetBird ships native
simultaneous profiles, migrating back to stock NetBird should be trivial — v1.0's
headline feature is the migration guide *away* from multibird.

## Quickstart

```sh
# register two meshes (setup keys are stored 0600 and never logged)
multibird add home --management-url https://netbird.example.home --setup-key <KEY>
multibird add lab  --management-url https://api.netbird.io --setup-key <KEY>

# bring them both up
multibird up --all

# see everything at a glance
multibird status
NAME  STATE      NETBIRD IP     PEERS  IFACE    MGMT                          VERSION
home  Connected  100.92.14.7    12     wt-mb-0  https://netbird.example.home  0.59.5
lab   Connected  100.101.3.22   4      wt-mb-1  https://api.netbird.io        0.59.5
```

`multibird status --json` for scripts, `multibird doctor` to sanity-check your setup,
`multibird nuke <name>` when an instance crashes half-up.

## Recommended pattern: stock for work, multibird for the rest

multibird **never touches your stock NetBird installation** — the default daemon/profile
that the official CLI and GUI app manage. Instances live strictly alongside it. The
pattern we recommend:

- Keep your **company/work mesh on stock NetBird** — it keeps the official GUI, MDM
  compatibility, and IT's expectations intact.
- Run your **personal/secondary meshes under multibird**.

## How it compares

|  | multibird | NetBird profile switching | Two VPN vendors |
|---|---|---|---|
| Both meshes connected at once | ✅ | ❌ one at a time | ✅ |
| One client stack to trust/update | ✅ (NetBird only) | ✅ | ❌ |
| Uses your existing NetBird accounts | ✅ | ✅ | ❌ re-onboard everything |
| Survives NetBird shipping native multi-profile | migrate back, delete multibird (by design) | n/a | stuck with vendor #2 |
| Extra moving parts | one thin CLI | none | a whole second vendor |

## What multibird is not (non-goals)

- **No GUI or tray app.** The official NetBird GUI manages its own default daemon only;
  multibird instances are CLI/TUI-only.
- **No management-server features.** Client-side only.
- **No Windows** in v1.
- **No automatic DNS arbitration** in v1 — multibird detects DNS-management conflicts
  between meshes, explains them, and lets you disable DNS or scope domains per instance.
- **Never manages your stock NetBird install.**

## Requirements & privileges

A `netbird` binary on your PATH (or per-instance via `--netbird-bin`). Creating
WireGuard/TUN interfaces requires elevation, same as stock NetBird — see
[docs/privileges.md](docs/privileges.md) for the per-platform story.

## Name & credits

multibird is a spiritual successor to the archived Python project
[OseSem/twinbird](https://github.com/OseSem/twinbird) (MIT) — thanks to its author for
proving the idea. This is a from-scratch Go implementation, not a port. "multi" because
nothing limits you to two instances.

## License

MIT — see [LICENSE](LICENSE).
