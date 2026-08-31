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
	"path/filepath"
)

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
	WireguardPort int  `toml:"wireguard_port,omitempty"`
	DisableDNS    bool `toml:"disable_dns,omitempty"`
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
	return Params{
		Dir:        dir,
		TOMLPath:   filepath.Join(dir, "instance.toml"),
		ConfigJSON: filepath.Join(dir, "config.json"),
		LogFile:    filepath.Join(dir, "daemon.log"),
		SocketPath: sock,
		DaemonAddr: "unix://" + sock,
		PIDFile:    filepath.Join(runDir, i.Name+".pid"),
		WGPort:     port,
	}
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
		DisableDNS    bool   `json:"disable_dns,omitempty"`
		LoggedIn      bool   `json:"logged_in,omitempty"`
		Interface     string `json:"interface,omitempty"`
		HasSetupKey   bool   `json:"has_setup_key"`
	}
	return json.Marshal(redacted{
		Name: i.Name, ManagementURL: i.ManagementURL, SSO: i.SSO,
		NetbirdBin: i.NetbirdBin, Index: i.Index, WireguardPort: i.WireguardPort,
		DisableDNS: i.DisableDNS, LoggedIn: i.LoggedIn, Interface: i.Interface,
		HasSetupKey: i.SetupKey != "",
	})
}
