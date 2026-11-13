// Package k8s reads the CPU and memory of the cluster node that hosts
// Headscale, over plain HTTPS with the pod's service-account token (D3), with
// no Kubernetes client library.
//
// It takes two calls, because the metrics API only reports usage:
//   - GET /apis/metrics.k8s.io/v1beta1/nodes/<node>: CPU and memory in use;
//   - GET /api/v1/nodes/<node>: the node's capacity, to turn usage into %.
//
// So the service account needs "get" on "nodes" in both the core API group
// and "metrics.k8s.io".
package k8s

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
)

// The service account's files, as every pod gets them.
const (
	TokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	CAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// Source reads one node's metrics.
type Source struct {
	base      string
	node      string
	tokenPath string
	client    *http.Client
}

// InCluster returns a Source for node from the pod's environment. It returns
// nil, with no error, when node is empty or the program isn't in a cluster:
// the gauges then show no data, which D3 says isn't an error.
func InCluster(node string) (*Source, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if node == "" || host == "" || port == "" {
		return nil, nil
	}
	ca, err := os.ReadFile(CAPath)
	if err != nil {
		return nil, fmt.Errorf("reading the cluster's CA: %w", err)
	}
	return New("https://"+net.JoinHostPort(host, port), node, TokenPath, ca)
}

// New returns a Source for the API server at base.
func New(base, node, tokenPath string, caPEM []byte) (*Source, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("the cluster's CA holds no certificate")
	}
	return &Source{
		base: strings.TrimRight(base, "/"), node: node, tokenPath: tokenPath,
		client: &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}},
	}, nil
}

// NodeMetrics implements collect.NodeSource.
func (s *Source) NodeMetrics(ctx context.Context) (collect.NodeMetrics, error) {
	var usage struct {
		Usage map[string]string `json:"usage"`
	}
	if err := s.get(ctx, "/apis/metrics.k8s.io/v1beta1/nodes/"+url.PathEscape(s.node), &usage); err != nil {
		return collect.NodeMetrics{}, fmt.Errorf("node usage: %w", err)
	}
	var node struct {
		Status struct {
			Capacity map[string]string `json:"capacity"`
		} `json:"status"`
	}
	if err := s.get(ctx, "/api/v1/nodes/"+url.PathEscape(s.node), &node); err != nil {
		return collect.NodeMetrics{}, fmt.Errorf("node capacity: %w", err)
	}
	cpu, err := ratio(usage.Usage["cpu"], node.Status.Capacity["cpu"])
	if err != nil {
		return collect.NodeMetrics{}, fmt.Errorf("cpu: %w", err)
	}
	mem, err := ratio(usage.Usage["memory"], node.Status.Capacity["memory"])
	if err != nil {
		return collect.NodeMetrics{}, fmt.Errorf("memory: %w", err)
	}
	return collect.NodeMetrics{CPUPct: &cpu, MemPct: &mem}, nil
}

func ratio(used, total string) (float64, error) {
	u, err := ParseQuantity(used)
	if err != nil {
		return 0, fmt.Errorf("usage: %w", err)
	}
	t, err := ParseQuantity(total)
	if err != nil {
		return 0, fmt.Errorf("capacity: %w", err)
	}
	if t <= 0 || u < 0 {
		return 0, errors.New("usage or capacity out of range")
	}
	return u / t * 100, nil
}

// get reads one API object. The token is read on every call, because the
// kubelet rotates projected service-account tokens.
func (s *Source) get(ctx context.Context, path string, into any) error {
	tok, err := os.ReadFile(s.tokenPath)
	if err != nil {
		return fmt.Errorf("reading the service-account token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("HTTP %d from the API server", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into); err != nil {
		return fmt.Errorf("decoding: %w", err)
	}
	return nil
}

var suffixes = []struct {
	s string
	m float64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"Pi", 1 << 50}, {"Ei", 1 << 60},
	{"n", 1e-9}, {"u", 1e-6}, {"m", 1e-3}, {"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}, {"P", 1e15}, {"E", 1e18},
}

// ParseQuantity reads a Kubernetes resource quantity, such as "250m",
// "1534772Ki", "4" or "1e3", as a plain number: cores for CPU, bytes for
// memory.
func ParseQuantity(q string) (float64, error) {
	if q == "" {
		return 0, errors.New("missing quantity")
	}
	num, mult := q, 1.0
	for _, sf := range suffixes {
		if strings.HasSuffix(q, sf.s) {
			num, mult = strings.TrimSuffix(q, sf.s), sf.m
			break
		}
	}
	if num == "" || strings.ContainsAny(num, " _") {
		return 0, fmt.Errorf("bad quantity %q", q)
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("bad quantity %q", q)
	}
	return f * mult, nil
}
