// Package tsnode joins the tunnel's tailnet with tsnet, for outbound requests
// to the Pis only (D3). It never listens on the tailnet, and the public page
// is served by a plain TCP listener elsewhere.
package tsnode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"tailscale.com/envknob"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/logtail"
	"tailscale.com/tsnet"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
)

// Config is the node's settings.
type Config struct {
	// Dir holds the node's state, on the same volume as the database, so a
	// restarted pod comes back as the same node.
	Dir string
	// Hostname is the name presented to the control server.
	Hostname string
	// ControlURL is the tunnel's Headscale.
	ControlURL string
	// AuthKey is needed only for the first login. Its tag, tag:dashboard,
	// comes from the key, as the Pis' tags do.
	AuthKey string
	Log     *slog.Logger
}

// Node is the dashboard's tailnet node.
type Node struct {
	srv *tsnet.Server
}

// Start begins joining the tailnet and returns at once. Requests wait, up to
// their own deadline, for the node to be running.
func Start(cfg Config) (*Node, error) {
	if cfg.Dir == "" || cfg.ControlURL == "" || cfg.Hostname == "" {
		return nil, errors.New("tsnode: Dir, ControlURL and Hostname are required")
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the tailnet state directory: %w", err)
	}
	// tsnet uploads its logs to log.tailscale.com unless told not to. The
	// tunnel's tailnet is the user's own; nothing about it goes there.
	envknob.SetNoLogsNoSupport()
	logtail.Disable()

	srv := &tsnet.Server{
		Dir:        cfg.Dir,
		Hostname:   cfg.Hostname,
		ControlURL: cfg.ControlURL,
		AuthKey:    cfg.AuthKey,
		UserLogf: func(format string, args ...any) {
			cfg.Log.Info(collect.Scrub(fmt.Sprintf(format, args...)), "from", "tsnet")
		},
		// Logf, the backend's verbose log, stays nil: discarded.
		RunWebClient: false,
	}
	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("starting tsnet: %w", err)
	}
	return &Node{srv: srv}, nil
}

// Status implements tailnet.StatusSource.
func (n *Node) Status(ctx context.Context) (*ipnstate.Status, error) {
	lc, err := n.srv.LocalClient()
	if err != nil {
		return nil, err
	}
	return lc.Status(ctx)
}

// Dial connects to an address on the tailnet. It waits, up to ctx's
// deadline, for the node to be running.
func (n *Node) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return n.srv.Dial(ctx, network, addr)
}

// Close leaves the tailnet.
func (n *Node) Close() error {
	done := make(chan error, 1)
	go func() { done <- n.srv.Close() }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		return errors.New("tsnet didn't close within 10 seconds")
	}
}
