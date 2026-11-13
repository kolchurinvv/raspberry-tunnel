package k8s

import (
	"context"
	"encoding/pem"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseQuantity(t *testing.T) {
	cases := map[string]float64{
		"4": 4, "250m": 0.25, "123456789n": 0.123456789, "1500u": 0.0015,
		"1Ki": 1024, "8048284Ki": 8048284 * 1024, "2Gi": 2 << 30, "1e3": 1000, "1.5": 1.5, "3k": 3000, "5M": 5e6,
	}
	for in, want := range cases {
		got, err := ParseQuantity(in)
		if err != nil || math.Abs(got-want) > 1e-9*math.Max(1, want) {
			t.Errorf("ParseQuantity(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "Ki", "abc", "1 Ki", "1_000", "--1"} {
		if _, err := ParseQuantity(bad); err == nil {
			t.Errorf("ParseQuantity(%q) succeeded", bad)
		}
	}
}

type apiServer struct {
	mu     sync.Mutex
	tokens []string
	status int
}

func (a *apiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.tokens = append(a.tokens, r.Header.Get("Authorization"))
	status := a.status
	a.mu.Unlock()
	if status != 0 {
		http.Error(w, "forbidden: nodes is forbidden for user system:serviceaccount:x", status)
		return
	}
	switch r.URL.Path {
	case "/apis/metrics.k8s.io/v1beta1/nodes/node-1":
		w.Write([]byte(`{"kind":"NodeMetrics","usage":{"cpu":"500m","memory":"2Gi"}}`))
	case "/api/v1/nodes/node-1":
		w.Write([]byte(`{"kind":"Node","status":{"capacity":{"cpu":"4","memory":"8Gi"},"allocatable":{"cpu":"3800m"}}}`))
	default:
		http.NotFound(w, r)
	}
}

func setup(t *testing.T) (*Source, *apiServer, string) {
	t.Helper()
	api := &apiServer{}
	srv := httptest.NewTLSServer(api)
	t.Cleanup(srv.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("first-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(srv.URL, "node-1", tok, ca)
	if err != nil {
		t.Fatal(err)
	}
	return s, api, tok
}

func TestNodeMetrics(t *testing.T) {
	s, api, tok := setup(t)
	m, err := s.NodeMetrics(context.Background())
	if err != nil {
		t.Fatalf("NodeMetrics: %v", err)
	}
	if m.CPUPct == nil || *m.CPUPct != 12.5 || m.MemPct == nil || *m.MemPct != 25 {
		t.Errorf("metrics = %v, %v; want 12.5, 25", m.CPUPct, m.MemPct)
	}

	// The token rotates; the next call must use the new one.
	if err := os.WriteFile(tok, []byte("second-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NodeMetrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"Bearer first-token", "Bearer first-token", "Bearer second-token", "Bearer second-token"}
	if strings.Join(api.tokens, ",") != strings.Join(want, ",") {
		t.Errorf("tokens = %q", api.tokens)
	}
}

func TestNodeMetricsErrors(t *testing.T) {
	s, api, _ := setup(t)
	api.status = http.StatusForbidden
	_, err := s.NodeMetrics(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "serviceaccount") {
		t.Errorf("err = %v; want the status, not the body", err)
	}

	s2, _, _ := setup(t)
	s2.node = "missing"
	if _, err := s2.NodeMetrics(context.Background()); err == nil {
		t.Error("a missing node gave metrics")
	}
}

func TestInClusterOutside(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	s, err := InCluster("node-1")
	if s != nil || err != nil {
		t.Errorf("outside a cluster: %v, %v; want nil, nil", s, err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	if s, err := InCluster(""); s != nil || err != nil {
		t.Errorf("no node name: %v, %v; want nil, nil", s, err)
	}
}

func TestNewRejectsEmptyCA(t *testing.T) {
	if _, err := New("https://x", "n", "t", []byte("not a cert")); err == nil {
		t.Error("New accepted a CA with no certificate")
	}
}
