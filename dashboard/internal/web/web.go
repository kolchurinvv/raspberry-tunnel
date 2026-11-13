// Package web serves the public page. Everything lives at "/" or under
// "/_dash/", never "/api/", which belongs to Headscale on the same hostname
// (D2, D3). The page's only input is the range.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/view"
)

//go:embed templates/*.html static/*
var files embed.FS

// Prefix is the dashboard's own path prefix, besides "/".
const Prefix = "/_dash/"

// Reader is what the page reads from the store.
type Reader interface {
	Samples(ctx context.Context, from, to time.Time) ([]model.Sample, error)
	Rollups(ctx context.Context, from, to time.Time) ([]model.Rollup, error)
	Events(ctx context.Context, from, to time.Time, limit int) ([]model.Event, error)
	Ping(ctx context.Context) error
}

// Options configures a Server.
type Options struct {
	Snapshot func() collect.Snapshot
	Store    Reader
	Loc      *time.Location
	Dev      bool
	Now      func() time.Time
	Log      *slog.Logger
}

// Server is the public site.
type Server struct {
	opt    Options
	tmpl   *template.Template
	assets map[string]asset // by published name, which carries a content hash
	names  map[string]string
	cache  renderCache
}

type asset struct {
	body  []byte
	ctype string
}

// Route is one public route.
type Route struct {
	Pattern string
	Handler http.HandlerFunc
}

// New loads the templates and assets.
func New(opt Options) (*Server, error) {
	if opt.Snapshot == nil || opt.Store == nil || opt.Loc == nil || opt.Log == nil {
		return nil, fmt.Errorf("web.New: Snapshot, Store, Loc and Log are required")
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	s := &Server{opt: opt, assets: map[string]asset{}, names: map[string]string{}}
	entries, err := fs.ReadDir(files, "static")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		body, err := files.ReadFile("static/" + e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		ext := path.Ext(e.Name())
		published := strings.TrimSuffix(e.Name(), ext) + "." + hex.EncodeToString(sum[:])[:12] + ext
		ctype := mime.TypeByExtension(ext)
		if ctype == "" {
			return nil, fmt.Errorf("no content type for %s", e.Name())
		}
		s.assets[published] = asset{body, ctype}
		s.names[e.Name()] = Prefix + "static/" + published
	}
	s.tmpl, err = template.New("").Funcs(template.FuncMap{
		"asset": func(name string) (string, error) {
			if p, ok := s.names[name]; ok {
				return p, nil
			}
			return "", fmt.Errorf("no asset %q", name)
		},
		"sz":  func(i view.Icon, n int) map[string]any { return map[string]any{"I": i, "N": n} },
		"box": func(b view.TopoBox, cls string) map[string]any { return map[string]any{"B": b, "Cls": cls} },
	}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	return s, nil
}

// Routes are every public route. Handler builds its mux from this list, and
// the tests check it: "/" or "/_dash/…", nothing else.
func (s *Server) Routes() []Route {
	return []Route{
		{"GET /{$}", s.page},
		{"GET " + Prefix + "fragment", s.fragment},
		{"GET " + Prefix + "static/{name}", s.static},
		{"GET " + Prefix + "healthz", s.healthz},
		{"GET " + Prefix + "readyz", s.readyz},
	}
}

// Handler is the public site, on its own mux. Never http.DefaultServeMux:
// tsnet links expvar, which registers /debug/vars there.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, r := range s.Routes() {
		mux.HandleFunc(r.Pattern, r.Handler)
	}
	return headers(mux)
}

// csp allows only this site's own script, stylesheet and requests, plus the
// style attributes the server renders for gauges and charts.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; " +
	"img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	s.serveRendered(w, r, false)
}

func (s *Server) fragment(w http.ResponseWriter, r *http.Request) {
	s.serveRendered(w, r, true)
}

func (s *Server) serveRendered(w http.ResponseWriter, r *http.Request, frag bool) {
	rng := view.ParseRange(r.URL.Query().Get("range"))
	body, err := s.Render(r.Context(), rng, frag)
	if err != nil {
		// A template that fails to render is this program's bug.
		s.opt.Log.Error("rendering the page", "range", rng.Key, "err", err)
		http.Error(w, "The page couldn't be rendered.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(body)
}

// renderCache keeps each range's page and fragment for one 30-second tick,
// so a busy public page costs one render per tick.
type renderCache struct {
	mu   sync.Mutex
	tick time.Time
	at   time.Time
	m    map[string][]byte
}

func (c *renderCache) get(tick, at time.Time, key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.tick.Equal(tick) || !c.at.Equal(at) {
		return nil, false
	}
	b, ok := c.m[key]
	return b, ok
}

func (c *renderCache) put(tick, at time.Time, key string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.tick.Equal(tick) || !c.at.Equal(at) {
		c.tick, c.at, c.m = tick, at, map[string][]byte{}
	}
	c.m[key] = b
}

// Render renders the page, or only its refreshable part, for one range.
func (s *Server) Render(ctx context.Context, rng view.Range, frag bool) ([]byte, error) {
	now := s.opt.Now()
	tick := now.Truncate(model.Interval)
	snap := s.opt.Snapshot()
	key := rng.Key
	if frag {
		key += "/fragment"
	}
	if b, ok := s.cache.get(tick, snap.At, key); ok {
		return b, nil
	}

	w := view.WindowAt(rng, now)
	in := view.Input{Now: now, Loc: s.opt.Loc, Window: w, Snap: snap, Dev: s.opt.Dev}
	if rng.Raw {
		samples, err := s.opt.Store.Samples(ctx, w.Start, w.End())
		in.Hist, in.HistErr = view.FromSamples(w, samples), err != nil
		if err != nil {
			s.opt.Log.Warn("reading samples for the page", "err", err)
		}
	} else {
		rollups, err := s.opt.Store.Rollups(ctx, w.Start, w.End())
		in.Hist, in.HistErr = view.FromRollups(w, rollups), err != nil
		if err != nil {
			s.opt.Log.Warn("reading rollups for the page", "err", err)
		}
	}
	events, err := s.opt.Store.Events(ctx, w.Start, now.Add(time.Second), 50)
	if err != nil {
		s.opt.Log.Warn("reading events for the page", "err", err)
	}
	in.Events = events

	page := view.Build(in)
	var buf bytes.Buffer
	name := "page"
	if frag {
		name = "main"
	}
	if err := s.tmpl.ExecuteTemplate(&buf, name, page); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if in.HistErr || err != nil {
		// Don't keep a page that's missing its history for the whole tick.
		return b, nil
	}
	s.cache.put(tick, snap.At, key, b)
	return b, nil
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	a, ok := s.assets[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.ctype)
	// The name carries the content's hash, so it never changes under a URL.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(a.body)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintln(w, "ok")
}

// readyz is ready when the database answers. The tailnet isn't required: the
// page still serves its history, and says when the tailnet can't be seen.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.opt.Store.Ping(ctx); err != nil {
		s.opt.Log.Warn("readiness: the database isn't answering", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "not ready")
		return
	}
	fmt.Fprintln(w, "ready")
}
