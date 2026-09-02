// Package instance defines the multibird instance model and derives every
// isolation parameter from (config root, run dir, name, index).
//
// One TOML file per instance under <config root>/<name>/instance.toml.
// NetBird's own config.json lives next to it and remains UNTOUCHED by us —
// netbird owns it entirely (obsolescence by design).
//
// SECURITY: instances carry setup keys. TOML files are written 0600 inside
// 0700 dirs, and the key is redacted from String() and JSON output. Keep it
// that way.
package instance

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
)

// DNSMode says who configures host DNS for an instance.
type DNSMode string

const (
	// DNSNative lets netbird manage host DNS itself (upstream behavior;
	// collides with other daemons on macOS — see docs/dns.md).
	DNSNative DNSMode = "native"
	// DNSMultibird runs netbird's resolver on a fixed local address with
	// host configuration disabled; multibird writes the per-instance scoped
	// resolvers into the macOS dynamic store (the arbiter). darwin-only.
	DNSMultibird DNSMode = "multibird"
	// DNSDisabled turns host DNS registration off entirely.
	DNSDisabled DNSMode = "disabled"
)

// ParseDNSMode validates a user-supplied mode string.
func ParseDNSMode(s string) (DNSMode, error) {
	switch DNSMode(s) {
	case DNSNative, DNSMultibird, DNSDisabled:
		return DNSMode(s), nil
	}
	return "", fmt.Errorf("invalid dns mode %q: use native, multibird or disabled (see docs/dns.md)", s)
}

// DNSDisableSys reports whether netbird's own host-DNS configuration must be
// off for this mode (the disable_dns daemon setting).
func (m DNSMode) DNSDisableSys() bool { return m != DNSNative }

// DNSBasePort is the base for derived per-instance resolver listen ports
// (multibird DNS mode): 5300+index on 127.0.0.1.
const DNSBasePort = 5300

// DefaultBasePort is the default WireGuard listen port for index 0.
// Deliberately NOT 51820 (stock netbird's default): multibird lives alongside
// a stock install and must never collide with it. See CLAUDE.md Decisions.
const DefaultBasePort = 51900

// Instance is the persisted model of one isolated netbird daemon.
type Instance struct {
	Name          string `toml:"name"`
	ManagementURL string `toml:"management_url"`
	// SetupKey is a credential: never logged, never printed, redacted in
	// String() and MarshalJSON.
	SetupKey string `toml:"setup_key,omitempty"`
	SSO      bool   `toml:"sso,omitempty"`
	// NetbirdBin optionally pins this instance to a specific netbird binary
	// (version-drift defense: work mesh on a company-pinned version, home on
	// latest). Empty means "netbird" from PATH.
	NetbirdBin string `toml:"netbird_bin,omitempty"`
	// Index drives deterministic isolation parameters; allocated at add time
	// and never reused while the instance exists.
	Index int `toml:"index"`
	// WireguardPort overrides the derived listen port when non-zero.
	WireguardPort int `toml:"wireguard_port,omitempty"`
	// DNSMode says who configures host DNS (see the DNSMode constants).
	// Empty means "not migrated yet" — Normalize fills it in.
	DNSMode DNSMode `toml:"dns_mode,omitempty"`
	// LegacyDisableDNS is the pre-dns_mode boolean, read only for migration
	// (true→disabled, false→native). Never written back.
	LegacyDisableDNS bool `toml:"disable_dns,omitempty"`
	// LoggedIn records that a successful Login gRPC call persisted the
	// isolation params into netbird's config.json.
	LoggedIn bool `toml:"logged_in,omitempty"`
	// Interface is the ACTUAL interface name discovered after the engine
	// came up (on macOS the kernel assigns utunN; never predict it).
	Interface string `toml:"interface,omitempty"`
}

// Params are the derived isolation parameters for an instance.
type Params struct {
	Dir        string // per-instance state dir (holds instance.toml, config.json, logs)
	TOMLPath   string // multibird's instance metadata (credential, 0600)
	ConfigJSON string // netbird's own config.json (owned by netbird)
	LogFile    string // per-instance daemon log
	SocketPath string // per-instance daemon unix socket
	DaemonAddr string // --daemon-addr value (unix://<SocketPath>)
	PIDFile    string // daemon pid file (written by internal/daemon)
	WGPort     int    // WireGuard listen port
	// DNSListen is the fixed resolver address used in DNSMultibird mode
	// (sent as customDNSAddress): 127.0.0.1:<5300+index>.
	DNSListen netip.AddrPort
}

// DeriveParams computes every isolation parameter. Pure function of its
// inputs — table-driven-tested, no OS calls.
func (i *Instance) DeriveParams(configRoot, runDir string) Params {
	dir := filepath.Join(configRoot, i.Name)
	sock := filepath.Join(runDir, i.Name+".sock")
	port := i.WireguardPort
	if port == 0 {
		port = DefaultBasePort + i.Index
	}
	dnsPort := uint16(DNSBasePort + i.Index) //nolint:gosec // G115: index is small
	return Params{
		Dir:        dir,
		TOMLPath:   filepath.Join(dir, "instance.toml"),
		ConfigJSON: filepath.Join(dir, "config.json"),
		LogFile:    filepath.Join(dir, "daemon.log"),
		SocketPath: sock,
		DaemonAddr: "unix://" + sock,
		PIDFile:    filepath.Join(runDir, i.Name+".pid"),
		WGPort:     port,
		DNSListen:  netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), dnsPort),
	}
}

// Normalize migrates legacy fields: instances saved before dns_mode carry
// only disable_dns (true→disabled, false→native — an EXISTING instance's
// effective behavior never changes on upgrade; the platform default applies
// only to newly added instances). Returns true if the instance changed and
// should be re-saved.
func (i *Instance) Normalize() bool {
	if i.DNSMode != "" {
		return false
	}
	if i.LegacyDisableDNS {
		i.DNSMode = DNSDisabled
	} else {
		i.DNSMode = DNSNative
	}
	i.LegacyDisableDNS = false // never write the legacy field back
	return true
}

// String redacts the setup key.
func (i Instance) String() string {
	key := ""
	if i.SetupKey != "" {
		key = " setup_key=<redacted>"
	}
	return fmt.Sprintf("instance %s (mgmt=%s index=%d%s)", i.Name, i.ManagementURL, i.Index, key)
}

// MarshalJSON redacts the setup key so `status --json` and friends can never
// leak it.
func (i Instance) MarshalJSON() ([]byte, error) {
	type redacted struct {
		Name          string `json:"name"`
		ManagementURL string `json:"management_url"`
		SSO           bool   `json:"sso,omitempty"`
		NetbirdBin    string `json:"netbird_bin,omitempty"`
		Index         int    `json:"index"`
		WireguardPort int    `json:"wireguard_port,omitempty"`
		DNSMode       string `json:"dns_mode,omitempty"`
		LoggedIn      bool   `json:"logged_in,omitempty"`
		Interface     string `json:"interface,omitempty"`
		HasSetupKey   bool   `json:"has_setup_key"`
	}
	return json.Marshal(redacted{
		Name: i.Name, ManagementURL: i.ManagementURL, SSO: i.SSO,
		NetbirdBin: i.NetbirdBin, Index: i.Index, WireguardPort: i.WireguardPort,
		DNSMode: string(i.DNSMode), LoggedIn: i.LoggedIn, Interface: i.Interface,
		HasSetupKey: i.SetupKey != "",
	})
}
