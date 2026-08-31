# Privileges

Research findings against netbird v0.77.1 (`client/cmd/root.go`, `client/cmd/service.go`).

## What needs root and why

The netbird daemon (`netbird service run`) requires root on both Linux and macOS: it
creates the TUN/WireGuard interface, edits the routing table, and (unless disabled)
manages DNS. This is true for stock netbird too — `netbird service install` registers a
systemd unit (Linux) / LaunchDaemon (macOS) running as root, and `netbird up
--foreground-mode` must be run with sudo.

A rootless mode exists in netbird (userspace WireGuard + CAP_NET_ADMIN tricks) but is
not its default model and is explicitly **out of scope for multibird v1**.

## multibird's model: mirror stock netbird

- **Daemons run as root.** `multibird up` therefore needs sudo (it may spawn the
  daemon). Boot units (v0.2 `multibird install --system`) run the daemon as root the
  same way stock netbird's service does.
- **Control happens over the per-instance unix socket** at
  `/var/run/multibird/<name>.sock` (dir 0755, root-owned), exactly mirroring stock
  netbird's `/var/run/netbird.sock`. Like stock netbird, socket permissions mean
  read-style commands (`status`, `list`) work unprivileged only if the socket allows
  it; otherwise use sudo. We deliberately do not chmod/chown sockets more openly than
  stock netbird does — an open control socket is equivalent to root.
- **No setuid binaries, no privilege daemon of our own.** multibird itself holds no
  privileges; it only inherits whatever the invoking user has.

## Practical guidance (put in user-facing errors)

- `permission denied` spawning a daemon → "re-run with sudo: creating WireGuard
  interfaces requires root (same as stock netbird)".
- `permission denied` dialing a socket → "this instance's daemon runs as root; re-run
  with sudo".

## Open questions (revisit in v0.2)

- macOS LaunchAgent (per-user) cannot create utun devices; `multibird install` on macOS
  therefore defaults to a LaunchDaemon despite the brief suggesting user-level default.
  Verify during v0.2 implementation.
- Group-based socket access (e.g. a `multibird` group with 0660 sockets) as an opt-in.
