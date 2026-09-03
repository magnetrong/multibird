# DNS: how netbird manages it, and what conflicts look like with multiple instances

Research findings against netbird v0.77.1 (`client/internal/dns/host_unix.go`,
`host_darwin.go`).

## Linux

netbird auto-detects a host DNS manager, in order of preference:

1. **systemd-resolved** via D-Bus, whenever resolved owns libc resolution (nss-resolve,
   or the 127.0.0.53 stub detected in /etc/resolv.conf). netbird registers its match
   domains / default route on its own interface — this is per-link, so **two instances
   coexist reasonably well here** as long as their DNS domains don't overlap.
2. **resolvconf**, if installed.
3. **Direct /etc/resolv.conf rewrite** (original nameservers preserved as upstream).
   This is a single global file: **two instances that both want to be the primary
   resolver will fight over it**. Last writer wins; on teardown ordering bugs the file
   can be left pointing at a dead resolver.

## macOS: the verified collision (netbird v0.77.1, observed 2026-09-02)

netbird drives `/usr/sbin/scutil`, writing dynamic-store keys. The key names are
FIXED format strings (`client/internal/dns/host_darwin.go:26-27`):
`State:/Network/Service/NetBird-Match-0/DNS`, `NetBird-Search-0`, etc. Consequences,
verified with two daemons (a multibird instance + a stock client of another mesh),
each with disjoint split-DNS domains:

- `addBatchedDomains` (line 414) makes EVERY daemon write its match domains to
  `NetBird-Match-0`. scutil `set` replaces the whole dictionary, so the LAST daemon
  to apply wins — disjoint domains do not help, the collision is on the key name.
  Result: only one mesh's names resolve at any time, flipping on every network change
  (each re-apply is a new "last writer").
- `removeKeysContaining` (line 397) only removes keys the daemon itself created, so
  this is overwrite, not deletion.
- `discoverExistingKeys` (line 170): after an unclean shutdown a daemon removes EVERY
  `NetBird-*` DNS key it finds — including another daemon's. Second collision path.

Writing a UNIQUELY NAMED key by hand fixed it immediately and survived the other
daemon's re-applies. That experiment is the design.

## The arbiter: dns_mode=multibird (darwin only)

On macOS multibird supersedes v1's "detect, explain, never arbitrate" policy: with
`--dns-mode multibird` (the darwin default for new instances):

- The daemon runs with `disable_dns=true` — its resolver keeps running (upstream
  `server.go Initialize()` calls `service.Listen()` before the host-configurator
  swap) and keeps forwarding nameserver groups and serving peer names; only the
  host configuration is skipped.
- The resolver serves at a DETERMINISTIC in-tunnel address: on macOS netbird
  always runs a userspace-WireGuard bind, so DNS is an in-memory packet hook at
  the last IP of the mesh network minus one, port 53 (e.g. `100.96.255.254:53`
  for a /16) — upstream `ServiceViaMemory`/`GetLastIPFromNetwork(network, 1)`.
  `customDNSAddress` is IGNORED on that path (verified 2026-09-03), so multibird
  derives the address from the local peer CIDR instead, and every SetConfig call
  explicitly CLEARS customDNSAddress (upstream persists that field
  unconditionally, so every call must state it; "empty" clears).
- multibird derives the instance's scoped-resolver spec from live status (the
  in-tunnel resolver address above; peer domain = fqdn minus first label;
  nameserver-group match domains; in-addr.arpa / ip6.arpa reverse zones mirroring
  upstream's rounding) and writes keys named
  `State:/Network/Service/multibird-<instance>-(Match|Search)-<n>/DNS`, ≤50 domains
  and ≤1500 bytes per key (upstream's own limits), followed by
  `dscacheutil -flushcache` + `killall -HUP mDNSResponder`.
- The `multibird-` prefix means stock netbird's `removeKeysContaining` /
  `discoverExistingKeys` never touch our keys, and we never touch `NetBird-*`.
- Registrations refresh on `up`, on every `status`, and via
  `multibird dns sync [--watch]` (`--watch` re-applies on daemon NETWORK/DNS events;
  suitable as a launchd KeepAlive job). `down`/`remove`/`nuke` remove the keys;
  `doctor` reports strays.
- Primary-resolver claims (a nameserver group with no match domains, i.e. route-all
  DNS) are REFUSED in multibird mode: arbitrating a primary would fight the host's
  own DNS exactly like the upstream bug. Use `--dns-mode native` on at most one
  instance for that.

Linux is unaffected (systemd-resolved settings are per link): `--dns-mode native`
stays the default and `multibird` mode errors with guidance.

## Testing DNS on macOS: the dig caveat

`dig` reads /etc/resolv.conf and BYPASSES scoped resolvers — it will "prove" the
setup broken while everything works. Test with:

```
dscacheutil -q host -a name peer.mesh.example
ping peer.mesh.example
scutil --dns        # inspect the registered scoped resolvers
```

## The conflict, stated plainly

Safe: each mesh handles only its own DNS domains (split DNS), disjoint domain sets.
Unsafe: more than one instance wants to manage the default/primary resolver, or their
match domains overlap.

## Policy: arbitrate placement (darwin), never arbitrate ownership

- Preflight (v0.2 fully; v0.1 stub) inspects each instance's DNS intent and reports:
  which instances manage DNS, which domains, and whether anything overlaps.
- Resolution is the **user's choice** via `--dns-mode` per instance. Recommended
  pattern on macOS: every multibird instance in `multibird` mode; at most one daemon
  on the whole machine (typically stock netbird) in native mode. On Linux: native,
  with disjoint domains server-side.
- Domain-set overlaps between meshes are still detected and reported, never
  auto-resolved — arbitration places resolvers, it does not decide which mesh owns
  a contested domain.
