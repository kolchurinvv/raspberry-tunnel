package web_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/views"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/k8s"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/store"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/tailnet"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/view"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/web"
)

// Secrets planted in every input the dashboard reads. None may reach a
// public response.
var secrets = []string{
	// The tailnet view: addresses, endpoints, names that aren't Pis.
	"100.101.102.1", "100.101.102.2", "100.101.102.3", "fd7a:115c:a1e0::1", "198.51.100.77", "2001:db8::77",
	"41641", "secret-tailnet", "alices-iphone", "dash-self", "hs-private", "authurl-token",
	// The Pis' answers: public addresses, and text a Pi made up.
	"203.0.113.7", "203.0.113.98", "203.0.113.99", "evil-host-name", "sneaky-peer", "tailscaled not answering",
	// The cluster.
	"k3s-secret-node-7", "system:serviceaccount", "sa-token-value",
}

func tags(t ...string) *views.Slice[string] {
	v := views.SliceOf(t)
	return &v
}

type statusSource struct {
	mu sync.Mutex
	st *ipnstate.Status
}

func (s *statusSource) Status(context.Context) (*ipnstate.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st, nil
}

func peer(host, ip string, tag ...string) *ipnstate.PeerStatus {
	return &ipnstate.PeerStatus{
		HostName: host, DNSName: host + ".secret-tailnet.example.", Tags: tags(tag...), Online: true,
		TailscaleIPs: []netip.Addr{netip.MustParseAddr(ip), netip.MustParseAddr("fd7a:115c:a1e0::1")},
		CurAddr:      "198.51.100.77:41641", Relay: "fra", ID: tailcfg.StableNodeID(host), Active: true,
		PeerAPIURL: []string{"http://" + ip + ":41641"},
	}
}

func fakeStatus() *ipnstate.Status {
	self := &ipnstate.PeerStatus{HostName: "dash-self", DNSName: "dash-self.secret-tailnet.example.",
		Tags: tags(tailnet.TagDashboard), Online: true, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.101.102.250")}}
	st := &ipnstate.Status{
		BackendState: "Running", Self: self, MagicDNSSuffix: "secret-tailnet.example",
		AuthURL:        "https://hs-private.example.net/register/authurl-token",
		CurrentTailnet: &ipnstate.TailnetStatus{Name: "secret-tailnet.example", MagicDNSSuffix: "secret-tailnet.example"},
		TailscaleIPs:   []netip.Addr{netip.MustParseAddr("100.101.102.250")},
		Peer:           map[key.NodePublic]*ipnstate.PeerStatus{},
	}
	for _, p := range []*ipnstate.PeerStatus{
		peer("pi-a", "100.101.102.1", tailnet.TagExit),
		peer("pi-b", "100.101.102.2", tailnet.TagGateway),
		peer("pi-c", "100.101.102.3", tailnet.TagGateway),
		peer("alices-iphone", "100.101.102.9"),
	} {
		st.Peer[key.NewNode().Public()] = p
	}
	st.Peer[key.NewNode().Public()] = &ipnstate.PeerStatus{
		HostName: "pi-c", Tags: tags(tailnet.TagGateway), Online: false, LastSeen: time.Now().Add(-time.Hour),
		TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.101.102.4")}, CurAddr: "[2001:db8::77]:41641", ID: "old-pi-c",
	}
	return st
}

// piHandler answers as a Pi, or misbehaves with addresses in its body.
func piHandler(body func() map[string]any, mode *string, mu *sync.Mutex) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		m := *mode
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch m {
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"schema":1,"error":"tailscaled not answering at 100.101.102.1 for 203.0.113.99"}`)
		case "garbage":
			io.WriteString(w, `{"schema":1,"host":"evil-host-name","public_ip":{"value":"203.0.113.98"`)
		case "slow":
			<-r.Context().Done()
		default:
			b, _ := json.Marshal(body())
			w.Write(b)
		}
	})
}

func statusOf(host, role string, extra map[string]any) map[string]any {
	m := map[string]any{
		"schema": 1, "host": host, "role": role, "time": time.Now().UTC().Format(time.RFC3339), "uptime_s": 5000,
		"cpu":    map[string]any{"load": []float64{0.1, 0.1, 0.1}, "stat": map[string]any{"user": 10, "nice": 0, "system": 0, "idle": 90, "iowait": 0, "irq": 0, "softirq": 0, "steal": 0}},
		"temp_c": 48.5, "undervoltage": false, "undervoltage_events": nil,
		"mem":       map[string]any{"total_kb": 1000, "available_kb": 700},
		"disk":      map[string]any{"total_b": 100, "used_b": 12, "avail_b": 88},
		"net":       map[string]any{"wan0": map[string]any{"rx_bytes": 1, "tx_bytes": 1}},
		"tunnel":    []any{},
		"public_ip": map[string]any{"value": "203.0.113.7", "checked": time.Now().UTC().Format(time.RFC3339)},
		"gateway":   nil,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

type site struct {
	srv   *httptest.Server
	web   *web.Server
	col   *collect.Collector
	modes map[string]*string
	mu    sync.Mutex
}

// newSite wires the whole dashboard, with fakes only where the world can't
// be ordered to misbehave: the tailnet view, the Pis, the cluster API.
func newSite(t *testing.T) *site {
	t.Helper()
	s := &site{modes: map[string]*string{}}
	routes := map[string]string{}
	for _, p := range []struct {
		ip, host, role string
		extra          map[string]any
	}{
		{"100.101.102.1", "pi-a", "exit", map[string]any{"tunnel": []any{map[string]any{"peer": "sneaky-peer", "online": true, "path": "direct", "relay": nil, "rx_bytes": 1, "tx_bytes": 1, "latency_ms": nil}}}},
		// pi-b claims another name and peer: the page must show its tailnet name only.
		{"100.101.102.2", "evil-host-name", "gateway", map[string]any{
			"tunnel":  []any{map[string]any{"peer": "sneaky-peer", "online": true, "path": "derp", "relay": "fra", "rx_bytes": 5, "tx_bytes": 5, "latency_ms": 40.5}},
			"gateway": map[string]any{"tv_port": map[string]any{"link": true, "speed_mbps": 100}, "tv_port_gated": true, "fallback": false},
		}},
		{"100.101.102.3", "pi-c", "gateway", nil},
	} {
		mode := ""
		s.modes[p.ip] = &mode
		host, role, extra := p.host, p.role, p.extra
		srv := httptest.NewServer(piHandler(func() map[string]any { return statusOf(host, role, extra) }, &mode, &s.mu))
		t.Cleanup(srv.Close)
		routes[p.ip+":9190"] = srv.Listener.Addr().String()
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		to, ok := routes[addr]
		if !ok {
			return nil, errors.New("dial tcp " + addr + ": connection refused")
		}
		var d net.Dialer
		return d.DialContext(ctx, network, to)
	}

	// The cluster API, with a node name and a token that must stay private.
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/metrics.k8s.io/v1beta1/nodes/k3s-secret-node-7":
			io.WriteString(w, `{"metadata":{"name":"k3s-secret-node-7"},"usage":{"cpu":"250m","memory":"1Gi"}}`)
		case "/api/v1/nodes/k3s-secret-node-7":
			io.WriteString(w, `{"metadata":{"name":"k3s-secret-node-7"},"status":{"capacity":{"cpu":"4","memory":"4Gi"}}}`)
		default:
			http.Error(w, "forbidden: system:serviceaccount", http.StatusForbidden)
		}
	}))
	t.Cleanup(api.Close)
	tok := filepath.Join(t.TempDir(), "token")
	os.WriteFile(tok, []byte("sa-token-value"), 0o600)
	ks, err := k8s.New(api.URL, "k3s-secret-node-7", tok, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw}))
	if err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.col = collect.New(collect.Options{
		Discoverer: tailnet.New(&statusSource{st: fakeStatus()}, log, nil), Store: db, Log: log, Node: ks,
		Timeout: 300 * time.Millisecond,
		Client:  &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}},
	})
	w, err := web.New(web.Options{Snapshot: s.col.Snapshot, Store: db, Loc: time.UTC, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	s.web = w
	s.srv = httptest.NewServer(w.Handler())
	t.Cleanup(s.srv.Close)
	return s
}

func (s *site) setMode(ip, mode string) {
	s.mu.Lock()
	*s.modes[ip] = mode
	s.mu.Unlock()
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b), res.Header
}

var assetRef = regexp.MustCompile(`/_dash/static/[A-Za-z0-9._-]+`)
var addrToken = regexp.MustCompile(`[0-9A-Fa-f:.]*[0-9A-Fa-f][0-9A-Fa-f:.]*`)

// publicResponses fetches every public path the page can lead to.
func publicResponses(t *testing.T, s *site) map[string]string {
	t.Helper()
	out := map[string]string{}
	paths := []string{"/", "/_dash/healthz", "/_dash/readyz", "/api/v1/node", "/debug/vars", "/favicon.ico", "/_dash/nope"}
	for _, r := range []string{"1h", "24h", "7d", "30d", "bogus"} {
		paths = append(paths, "/?range="+r, "/_dash/fragment?range="+r)
	}
	for _, p := range paths {
		_, body, _ := get(t, s.srv.URL+p)
		out[p] = body
		for _, a := range assetRef.FindAllString(body, -1) {
			if _, ok := out[a]; !ok {
				_, ab, _ := get(t, s.srv.URL+a)
				out[a] = ab
			}
		}
	}
	return out
}

func checkPrivate(t *testing.T, responses map[string]string) {
	t.Helper()
	for path, body := range responses {
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s contains %q", path, secret)
			}
		}
		if strings.HasSuffix(path, ".svg") {
			continue
		}
		for _, tok := range addrToken.FindAllString(body, -1) {
			for _, cand := range []string{tok, strings.Trim(tok, ":.")} {
				if _, err := netip.ParseAddr(cand); err == nil {
					t.Errorf("%s contains an IP address: %q", path, cand)
				}
				if _, err := netip.ParseAddrPort(cand); err == nil {
					t.Errorf("%s contains an endpoint: %q", path, cand)
				}
			}
		}
	}
}

// No public response holds an address, an endpoint or a name other than the
// Pis', through every way a poll can fail (D2, D3).
func TestPrivacy(t *testing.T) {
	s := newSite(t)
	ctx := context.Background()
	ts := time.Now().Truncate(30 * time.Second)
	s.col.Cycle(ctx, ts.Add(-90*time.Second))
	s.col.Cycle(ctx, ts.Add(-60*time.Second))
	checkPrivate(t, publicResponses(t, s))

	// Pi C fails every way there is, Pi A answers with a 500 naming
	// addresses, Pi B sends malformed JSON holding one.
	s.setMode("100.101.102.1", "500")
	s.setMode("100.101.102.2", "garbage")
	s.setMode("100.101.102.3", "slow")
	s.col.Cycle(ctx, ts.Add(-30*time.Second))
	checkPrivate(t, publicResponses(t, s))

	snap := s.col.Snapshot()
	if len(snap.Pis) != 3 || snap.Pis[0].Host != "pi-a" || snap.Pis[1].Host != "pi-b" || snap.Pis[2].Host != "pi-c" {
		t.Fatalf("Pis = %+v", snap.Pis)
	}
	_, page, _ := get(t, s.srv.URL+"/")
	for _, want := range []string{"Pi A", "Pi B", "Pi C", "error from the Pi", "bad data", "timed out"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestRoutes(t *testing.T) {
	s := newSite(t)
	for _, r := range s.web.Routes() {
		method, path, ok := strings.Cut(r.Pattern, " ")
		if !ok || method != "GET" {
			t.Errorf("route %q isn't a GET", r.Pattern)
		}
		if path != "/{$}" && !strings.HasPrefix(path, web.Prefix) {
			t.Errorf("route %q is neither / nor under %s", r.Pattern, web.Prefix)
		}
		if strings.HasPrefix(path, "/api") {
			t.Errorf("route %q is under /api", r.Pattern)
		}
	}

	for path, want := range map[string]int{
		"/": 200, "/?range=7d": 200, "/_dash/fragment": 200, "/_dash/healthz": 200, "/_dash/readyz": 200,
		"/api/v1/node": 404, "/api/": 404, "/debug/vars": 404, "/debug/pprof/": 404, "/favicon.ico": 404,
		"/index.html": 404, "/_dash/static/app.css": 404, "/_dash/": 404, "/status": 404,
	} {
		if code, _, _ := get(t, s.srv.URL+path); code != want {
			t.Errorf("GET %s = %d, want %d", path, code, want)
		}
	}
	res, err := http.Post(s.srv.URL+"/", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d", res.StatusCode)
	}

	// Every URL in the HTML, CSS and JavaScript is /, /?range=…, /_dash/…
	// or the GitHub link.
	allowed := regexp.MustCompile(`^(/|/\?range=(1h|24h|7d|30d)|/_dash/[A-Za-z0-9._/?=-]*|` + regexp.QuoteMeta(view.GitHub) + `)$`)
	attr := regexp.MustCompile(`(?:href|src|action|formaction)="([^"]*)"`)
	cssURL := regexp.MustCompile(`url\(\s*['"]?([^'")]*)`)
	jsURL := regexp.MustCompile(`['"]((?:/|https?:)[^'"\s]*)['"]`)
	anyURL := regexp.MustCompile(`https?://[^\s"'<>)]+`)
	resp := publicResponses(t, s)
	assets := 0
	for path, body := range resp {
		var found []string
		switch {
		case strings.HasSuffix(path, ".css"):
			assets++
			for _, m := range cssURL.FindAllStringSubmatch(body, -1) {
				found = append(found, m[1])
			}
		case strings.HasSuffix(path, ".js"):
			assets++
			for _, m := range jsURL.FindAllStringSubmatch(body, -1) {
				found = append(found, m[1])
			}
		case strings.HasSuffix(path, ".svg"):
			assets++
			// Only its XML namespace.
			for _, u := range anyURL.FindAllString(body, -1) {
				if u != "http://www.w3.org/2000/svg" {
					found = append(found, u)
				}
			}
			continue
		default:
			for _, m := range attr.FindAllStringSubmatch(body, -1) {
				found = append(found, strings.ReplaceAll(m[1], "&amp;", "&"))
			}
		}
		for _, u := range anyURL.FindAllString(body, -1) {
			found = append(found, u)
		}
		for _, u := range found {
			// The script builds "/_dash/fragment?range=" + the range.
			if !allowed.MatchString(u) && u != "/_dash/fragment?range=" {
				t.Errorf("%s links to %q", path, u)
			}
		}
	}
	if assets != 3 {
		t.Errorf("checked %d assets, want the CSS, the script and the icon", assets)
	}

	// The page asks for the icon under /_dash/.
	_, page, _ := get(t, s.srv.URL+"/")
	if !regexp.MustCompile(`<link rel="icon" href="/_dash/static/favicon\.[0-9a-f]+\.svg"`).MatchString(page) {
		t.Error("no favicon link under /_dash/")
	}
}

func TestRangeInput(t *testing.T) {
	s := newSite(t)
	for q, want := range map[string]string{"": "24h", "?range=1h": "1h", "?range=30d": "30d", "?range=1y": "24h", "?range=7d&range=1h": "7d", "?range=%3Cb%3E": "24h"} {
		_, body, _ := get(t, s.srv.URL+"/"+q)
		cur := regexp.MustCompile(`href="/\?range=([0-9a-z]+)" aria-current="page"`).FindStringSubmatch(body)
		if cur == nil || cur[1] != want {
			t.Errorf("/%s: current range %v, want %s", q, cur, want)
		}
		if !strings.Contains(body, `data-range="`+want+`"`) {
			t.Errorf("/%s: main isn't marked %s", q, want)
		}
	}
}

func TestHeadersAndCache(t *testing.T) {
	s := newSite(t)
	s.col.Cycle(context.Background(), time.Now().Truncate(30*time.Second))
	_, a, h := get(t, s.srv.URL+"/")
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		t.Errorf("headers = %v", h)
	}
	_, b, _ := get(t, s.srv.URL+"/")
	if a != b {
		t.Error("two renders in one tick differ")
	}
	css := assetRef.FindString(a)
	_, _, ch := get(t, s.srv.URL+css)
	if !strings.Contains(ch.Get("Cache-Control"), "immutable") {
		t.Errorf("asset cache = %q", ch.Get("Cache-Control"))
	}
}
