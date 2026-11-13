// Command dashboard is the tunnel's public health page (D2): one Go binary
// that joins the tunnel's tailnet with tsnet, asks each Pi for its status
// every 30 seconds, keeps 30 days of history in SQLite, and renders the page
// on the server (D3).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // the image has no zone files

	"tailscale.com/ipn/ipnstate"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/devmode"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/k8s"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/store"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/tailnet"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/tsnode"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/web"
)

type settings struct {
	listen      string
	stateDir    string
	controlURL  string
	hostname    string
	k8sNode     string
	tz          string
	dev         bool
	devScenario string
	devGateways int
	// stateDirSet is whether the state directory was given at all.
	stateDirSet bool
}

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

func parseSettings(args []string) (settings, error) {
	var s settings
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.StringVar(&s.listen, "listen", env("DASH_LISTEN", ":8080"), "public HTTP address (DASH_LISTEN)")
	fs.StringVar(&s.stateDir, "state-dir", env("DASH_STATE_DIR", "/var/lib/dashboard"), "directory for the database and the tailnet state, on a volume (DASH_STATE_DIR)")
	fs.StringVar(&s.controlURL, "control-url", env("DASH_CONTROL_URL", ""), "the tunnel's Headscale URL (DASH_CONTROL_URL)")
	fs.StringVar(&s.hostname, "hostname", env("DASH_HOSTNAME", "dashboard"), "this node's name on the tailnet (DASH_HOSTNAME)")
	fs.StringVar(&s.k8sNode, "k8s-node", env("DASH_K8S_NODE", ""), "the cluster node hosting Headscale, for its CPU and memory (DASH_K8S_NODE)")
	fs.StringVar(&s.tz, "tz", env("DASH_TZ", "UTC"), "time zone for the page's times (DASH_TZ)")
	devDefault, _ := strconv.ParseBool(env("DASH_DEV", "false"))
	fs.BoolVar(&s.dev, "dev", devDefault, "run with fake Pis, without tsnet (DASH_DEV)")
	fs.StringVar(&s.devScenario, "dev-scenario", "healthy", "dev mode: healthy, relayed, fallback or cutoff")
	fs.IntVar(&s.devGateways, "dev-gateways", 1, "dev mode: how many gateways, 1 to 3")
	if err := fs.Parse(args); err != nil {
		return s, err
	}
	_, s.stateDirSet = os.LookupEnv("DASH_STATE_DIR")
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "state-dir" {
			s.stateDirSet = true
		}
	})
	if fs.NArg() > 0 {
		return s, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if !s.dev {
		u, err := url.Parse(s.controlURL)
		if s.controlURL == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return s, errors.New("-control-url (DASH_CONTROL_URL) must be the Headscale server's URL")
		}
		if s.hostname == "" {
			return s, errors.New("-hostname must not be empty")
		}
	}
	return s, nil
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s, err := parseSettings(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		log.Error("settings", "err", err)
		os.Exit(2)
	}
	if err := run(s, log); err != nil {
		log.Error("stopping", "err", err)
		os.Exit(1)
	}
}

func run(s settings, log *slog.Logger) error {
	loc, err := time.LoadLocation(s.tz)
	if err != nil {
		return fmt.Errorf("time zone %q: %w", s.tz, err)
	}
	if s.dev && !s.stateDirSet {
		dir, err := os.MkdirTemp("", "dashboard-dev-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		s.stateDir = dir
	}
	if err := os.MkdirAll(s.stateDir, 0o700); err != nil {
		return fmt.Errorf("creating the state directory: %w", err)
	}

	// One instance only: two would share the tailnet node and the database.
	unlock, err := lockDir(s.stateDir)
	if err != nil {
		return err
	}
	defer unlock()

	// The image has no /tmp; keep temporary files on the volume.
	if os.Getenv("TMPDIR") == "" {
		tmp := filepath.Join(s.stateDir, "tmp")
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return fmt.Errorf("creating the temporary directory: %w", err)
		}
		os.Setenv("TMPDIR", tmp)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, filepath.Join(s.stateDir, "dashboard.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	opt := collect.Options{Store: db, Log: log}
	var node *lazyNode
	var sim *devmode.Sim
	if s.dev {
		sc, err := devmode.ParseScenario(s.devScenario)
		if err != nil {
			return err
		}
		sim, err = devmode.New(sc, s.devGateways, time.Now().Truncate(30*time.Second))
		if err != nil {
			return err
		}
		opt.Discoverer, opt.Client, opt.Node = sim, sim.Client(), sim
	} else {
		node = &lazyNode{}
		opt.Discoverer = tailnet.New(node, log, nil)
		opt.Client = &http.Client{Transport: &http.Transport{
			DialContext: node.Dial, DisableKeepAlives: true,
			ResponseHeaderTimeout: collect.DefaultTimeout, MaxResponseHeaderBytes: 16 << 10,
		}}
		ks, err := k8s.InCluster(s.k8sNode)
		if err != nil {
			log.Warn("no node metrics", "err", err)
		} else if ks != nil {
			opt.Node = ks
		} else {
			log.Info("no node metrics: not in a cluster, or no node name set")
		}
	}
	if s.dev {
		log.Info("dev mode: backfilling 30 days of fake history")
		if err := devmode.Backfill(ctx, sim, db, opt); err != nil {
			return fmt.Errorf("backfilling: %w", err)
		}
	}
	col := collect.New(opt)
	if err := col.Load(ctx); err != nil {
		return err
	}

	site, err := web.New(web.Options{Snapshot: col.Snapshot, Store: db, Loc: loc, Dev: s.dev, Log: log})
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr: s.listen, Handler: site.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.listen, err)
	}
	// The page is up at once; the tailnet joins behind it.
	errc := make(chan error, 2)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("serving the page", "addr", ln.Addr().String(), "dev", s.dev)

	if node != nil {
		tn, err := tsnode.Start(tsnode.Config{
			Dir: filepath.Join(s.stateDir, "tailscale"), Hostname: s.hostname, ControlURL: s.controlURL,
			AuthKey: os.Getenv("TS_AUTHKEY"), Log: log,
		})
		if err != nil {
			srv.Close()
			return err
		}
		node.set(tn)
		defer func() {
			if err := tn.Close(); err != nil {
				log.Warn("leaving the tailnet", "err", err)
			}
		}()
	}

	var wg sync.WaitGroup
	wg.Go(func() { col.Run(ctx) })

	select {
	case <-ctx.Done():
	case err := <-errc:
		stop()
		wg.Wait()
		return fmt.Errorf("serving: %w", err)
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		log.Warn("shutting down the page", "err", err)
	}
	wg.Wait()
	return nil
}

// lockDir takes an exclusive lock on the state directory, or fails if
// another instance holds it.
func lockDir(dir string) (func(), error) {
	path := filepath.Join(dir, "dashboard.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another dashboard is using %s (deploy with the Recreate strategy): %w", dir, err)
	}
	return func() { f.Close() }, nil
}

// lazyNode stands in for the tailnet node until it has started, so the page
// can serve before it.
type lazyNode struct {
	mu sync.Mutex
	n  *tsnode.Node
}

func (l *lazyNode) set(n *tsnode.Node) {
	l.mu.Lock()
	l.n = n
	l.mu.Unlock()
}

func (l *lazyNode) get() (*tsnode.Node, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == nil {
		return nil, errors.New("tsnet hasn't started")
	}
	return l.n, nil
}

func (l *lazyNode) Status(ctx context.Context) (*ipnstate.Status, error) {
	n, err := l.get()
	if err != nil {
		return nil, err
	}
	return n.Status(ctx)
}

func (l *lazyNode) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	n, err := l.get()
	if err != nil {
		return nil, err
	}
	return n.Dial(ctx, network, addr)
}
