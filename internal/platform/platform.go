// Package platform isolates ALL OS-specific behavior behind one interface.
//
// RULE (CLAUDE.md): no runtime.GOOS switches anywhere else in the codebase.
// Adding Windows later means adding platform_windows.go, nothing else.
package platform

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/magnetrong/multibird/internal/hostdns"
	"github.com/magnetrong/multibird/internal/instance"
)

// Platform is the single seam for OS differences.
type Platform interface {
	// ConfigRoot is where multibird keeps per-instance state
	// (~/.config/multibird on Linux, ~/Library/Application Support/multibird
	// on macOS). Created 0700 on demand by callers.
	ConfigRoot() (string, error)

	// RunDir is where per-instance daemon sockets and pid files live
	// (/var/run/multibird). Root-owned, mirroring stock netbird's
	// /var/run/netbird.sock model — see docs/privileges.md.
	RunDir() string

	// InterfaceHint returns the interface name to REQUEST for instance
	// index i (via the Login gRPC request). On Linux this is authoritative
	// ("wt-mb-<i>", ≤15 chars). On macOS utun numbers are kernel-assigned:
	// the hint is best-effort and the real name must come from
	// DiscoverInterface after the engine is up. Never assume the hint stuck.
	InterfaceHint(i int) string

	// DiscoverInterface finds the actual interface carrying addr (the
	// instance's netbird IP) after the engine is up.
	DiscoverInterface(addr netip.Addr) (string, error)

	// RouteInterface asks the OS routing table which interface would carry
	// traffic to addr — used by preflight to report which instance WINS an
	// overlapping-route conflict.
	RouteInterface(addr netip.Addr) (string, error)

	// DefaultDNSMode is the dns_mode for NEW instances: multibird (the DNS
	// arbiter) on darwin, native on linux — see docs/dns.md.
	DefaultDNSMode() instance.DNSMode

	// ApplyHostDNS registers/refreshes the instance's scoped resolvers with
	// the host (darwin: multibird-<name>-* dynamic-store keys). Idempotent.
	ApplyHostDNS(instanceName string, spec hostdns.Spec) error

	// RemoveHostDNS removes the instance's host DNS registration. No-op when
	// nothing is registered (and always on linux).
	RemoveHostDNS(instanceName string) error

	// ListHostDNSOwners returns the instance names that currently have host
	// DNS registrations (nil on linux).
	ListHostDNSOwners() ([]string, error)

	// StockNetbirdDNSPresent reports whether a NON-multibird netbird daemon
	// (stock install) currently manages host DNS — doctor uses it to warn
	// about the upstream key collision. Always false on linux.
	StockNetbirdDNSPresent() (bool, error)

	// DaemonEnv returns extra environment variables for spawned netbird
	// daemons — OS-specific coexistence settings (e.g. disabling netbird's
	// single-instance-assuming advanced routing on macOS).
	DaemonEnv() []string
}

// New returns the implementation for the current OS (build-tagged).
func New() Platform { return newPlatform() }

// discoverByAddr is the shared pure-Go discovery: walk interfaces and return
// the one owning addr. Works on both platforms; on macOS it is the only
// reliable way to learn which utunN the kernel assigned.
func discoverByAddr(addr netip.Addr) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("listing network interfaces: %w", err)
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			if ip.Unmap() == addr.Unmap() {
				return iface.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no interface carries %s (is the instance up?)", addr)
}
