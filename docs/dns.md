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

## macOS

netbird drives `/usr/sbin/scutil`, writing dynamic-store keys
(`State:/Network/Service/NetBird-*/DNS`). Match domains become scoped resolvers
(visible in `scutil --dns`, not in resolv.conf); an instance claiming the *primary*
resolver role causes macOS to regenerate /etc/resolv.conf. It does **not** write
/etc/resolver/ files. Two instances with disjoint match domains coexist; two instances
both claiming primary DNS conflict — the system picks by service order, effectively
arbitrary from the user's perspective.

## The conflict, stated plainly

Safe: each mesh handles only its own DNS domains (split DNS), disjoint domain sets.
Unsafe: more than one instance wants to manage the default/primary resolver, or their
match domains overlap.

## multibird v1 policy: detect, explain, never arbitrate

- Preflight (v0.2 fully; v0.1 stub) inspects each instance's DNS intent and reports:
  which instances manage DNS, which domains, and whether anything overlaps.
- Resolution is the **user's choice**, applied per instance via the `SetConfig` gRPC
  request's `disable_dns` field (equivalent of `netbird up --disable-dns`), stored in
  that instance's own config.json. Recommended pattern: the mesh whose DNS matters most
  keeps DNS management; every other instance runs with DNS disabled or with split
  domains configured server-side.
- No automatic arbitration in v1. Period.
