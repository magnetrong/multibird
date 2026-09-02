// Package nbgrpc is the PRIMARY control path to each instance's daemon: a
// typed gRPC client over the per-instance unix socket, using upstream's
// generated stubs (github.com/netbirdio/netbird/client/proto). Bumping the
// pinned module surfaces incompatibilities at compile time — that's the point.
//
// Upstream's own dial helper lives under client/internal, so we implement the
// trivial unix:// target mapping here ourselves (see CLAUDE.md Decisions).
package nbgrpc

import (
	"context"
	"fmt"
	"time"

	"github.com/netbirdio/netbird/client/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps a DaemonService connection to one instance's daemon.
type Client struct {
	conn *grpc.ClientConn
	d    proto.DaemonServiceClient
}

// Dial connects to a daemon over its unix socket. It does not block: RPC
// calls fail if the daemon isn't there.
func Dial(socketPath string) (*Client, error) {
	conn, err := grpc.NewClient("unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dialing daemon socket %s: %w", socketPath, err)
	}
	return &Client{conn: conn, d: proto.NewDaemonServiceClient(conn)}, nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error { return c.conn.Close() }

// LoginParams is the subset of the Login request multibird sets.
//
// NOTE (verified against v0.77.1 client/server/server.go): Login persists
// ONLY managementUrl and preSharedKey ("persistLoginOverrides"); the
// interfaceName/wireguardPort/disable_dns fields in LoginRequest are
// silently ignored. Isolation parameters must be pushed via SetConfig
// BEFORE Login — see Env.Up.
type LoginParams struct {
	SetupKey      string // empty for SSO; never log this
	ManagementURL string
	Hostname      string // peer name; per-instance so meshes see distinct peers
}

// SSOChallenge is returned when the management server wants a browser login
// instead of (or in addition to) a setup key.
type SSOChallenge struct {
	UserCode        string
	VerificationURI string // complete URI including the code, for the user to open
}

// Login registers the instance with its management server and persists the
// isolation parameters. A nil, nil return means logged in; a non-nil
// *SSOChallenge means the caller must run the browser flow and then call
// WaitSSOLogin. Errors never include the setup key.
func (c *Client) Login(ctx context.Context, p LoginParams) (*SSOChallenge, error) {
	req := &proto.LoginRequest{
		SetupKey:      p.SetupKey,
		ManagementUrl: p.ManagementURL,
		Hostname:      p.Hostname,
	}
	resp, err := c.d.Login(ctx, req)
	if err != nil {
		hint := "check the management URL and that the setup key is valid and not expired"
		if p.SetupKey == "" {
			hint = "check the management URL"
		}
		return nil, fmt.Errorf("login against %s failed: %w — %s", p.ManagementURL, err, hint)
	}
	if resp.GetNeedsSSOLogin() {
		uri := resp.GetVerificationURIComplete()
		if uri == "" {
			uri = resp.GetVerificationURI()
		}
		return &SSOChallenge{UserCode: resp.GetUserCode(), VerificationURI: uri}, nil
	}
	return nil, nil
}

// WaitSSOLogin blocks until the user completes the browser flow for the
// given challenge (or ctx expires).
func (c *Client) WaitSSOLogin(ctx context.Context, ch *SSOChallenge, hostname string) error {
	_, err := c.d.WaitSSOLogin(ctx, &proto.WaitSSOLoginRequest{UserCode: ch.UserCode, Hostname: hostname})
	if err != nil {
		return fmt.Errorf("waiting for SSO login: %w — complete the login in your browser at %s and try again", err, ch.VerificationURI)
	}
	return nil
}

// SetConfigParams are the instance settings multibird can change after the
// initial login WITHOUT re-consuming the setup key.
type SetConfigParams struct {
	ManagementURL string
	InterfaceName string
	WireguardPort int
	DisableDNS    bool
	// CustomDNSAddress pins the daemon's resolver listen address
	// (multibird DNS mode). GOTCHA (v0.77.1 setConfigInputFromRequest):
	// SetConfig persists customDNSAddress UNCONDITIONALLY — an absent field
	// silently resets it — so every SetConfig call must state the intent:
	// the address, or empty here (sent as the literal "empty") to clear.
	CustomDNSAddress string
}

// SetConfig updates the daemon's persisted config. Takes effect on the next
// engine up (callers should down/up to apply).
func (c *Client) SetConfig(ctx context.Context, p SetConfigParams) error {
	iface := p.InterfaceName
	port := int64(p.WireguardPort)
	dns := p.DisableDNS
	customDNS := p.CustomDNSAddress
	if customDNS == "" {
		customDNS = "empty" // upstream's literal for "clear the setting"
	}
	req := &proto.SetConfigRequest{
		// Each multibird daemon has exactly one profile: the default one.
		// SetConfig requires a profile handle; "default" needs no username.
		ProfileName:      "default",
		CustomDNSAddress: []byte(customDNS),
		ManagementUrl: p.ManagementURL,
		InterfaceName: &iface,
		WireguardPort: &port,
		DisableDns:    &dns,
	}
	if _, err := c.d.SetConfig(ctx, req); err != nil {
		return fmt.Errorf("updating daemon config: %w", err)
	}
	return nil
}

// Network is one route/network the mesh offers this peer.
type Network struct {
	ID       string
	Range    string // CIDR, or invalid for DNS-routed networks (Domains set)
	Selected bool
	Domains  []string
}

// Networks lists the networks (routes) available to this instance.
func (c *Client) Networks(ctx context.Context) ([]Network, error) {
	resp, err := c.d.ListNetworks(ctx, &proto.ListNetworksRequest{})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	out := make([]Network, 0, len(resp.GetRoutes()))
	for _, r := range resp.GetRoutes() {
		out = append(out, Network{
			ID: r.GetID(), Range: r.GetRange(),
			Selected: r.GetSelected(), Domains: r.GetDomains(),
		})
	}
	return out, nil
}

// DNSDomains extracts the DNS match domains this instance's daemon currently
// serves (empty strings mean "all domains", i.e. primary resolver).
func DNSDomains(st *proto.StatusResponse) []string {
	var out []string
	for _, g := range st.GetFullStatus().GetDnsServers() {
		if !g.GetEnabled() {
			continue
		}
		out = append(out, g.GetDomains()...)
	}
	return out
}

// Up starts the engine and blocks until it is running.
func (c *Client) Up(ctx context.Context) error {
	if _, err := c.d.Up(ctx, &proto.UpRequest{}); err != nil {
		return fmt.Errorf("bringing the engine up: %w — check the daemon log (multibird status shows its path via list)", err)
	}
	return nil
}

// Down stops the engine (daemon keeps running).
func (c *Client) Down(ctx context.Context) error {
	if _, err := c.d.Down(ctx, &proto.DownRequest{}); err != nil {
		return fmt.Errorf("bringing the engine down: %w", err)
	}
	return nil
}

// Status returns the full daemon status (with peer detail).
func (c *Client) Status(ctx context.Context) (*proto.StatusResponse, error) {
	resp, err := c.d.Status(ctx, &proto.StatusRequest{GetFullPeerStatus: true})
	if err != nil {
		return nil, fmt.Errorf("querying daemon status: %w", err)
	}
	return resp, nil
}

// WaitReady polls Status until the daemon answers or the deadline passes.
// Used right after spawning a daemon: the socket appears slightly before the
// gRPC server serves.
func (c *Client) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		rpcCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := c.d.Status(rpcCtx, &proto.StatusRequest{})
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon did not become ready within %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
