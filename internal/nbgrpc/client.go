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

func (c *Client) Close() error { return c.conn.Close() }

// LoginParams is the subset of the Login request multibird sets. This is
// where isolation parameters (interface name, WireGuard port, DNS toggle)
// enter netbird: the daemon persists them into its own config.json.
type LoginParams struct {
	SetupKey      string // never log this
	ManagementURL string
	InterfaceName string
	WireguardPort int
	DisableDNS    bool
}

// Login registers the instance with its management server and persists the
// isolation parameters. Errors never include the setup key.
func (c *Client) Login(ctx context.Context, p LoginParams) error {
	iface := p.InterfaceName
	port := int64(p.WireguardPort)
	dns := p.DisableDNS
	req := &proto.LoginRequest{
		SetupKey:      p.SetupKey,
		ManagementUrl: p.ManagementURL,
		InterfaceName: &iface,
		WireguardPort: &port,
		DisableDns:    &dns,
	}
	resp, err := c.d.Login(ctx, req)
	if err != nil {
		return fmt.Errorf("login against %s failed: %w — check the management URL and that the setup key is valid and not expired", p.ManagementURL, err)
	}
	if resp.GetNeedsSSOLogin() {
		return fmt.Errorf("this account requires SSO login, which multibird does not support yet (v0.1) — use a setup key: create one in the NetBird dashboard under Setup Keys")
	}
	return nil
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
