package collect

import "testing"

func TestScrub(t *testing.T) {
	cases := map[string]string{
		"dial tcp 100.64.0.2:9190: connect: connection refused": "dial tcp <addr>: connect: connection refused",
		"Get \"http://[fd7a:115c:a1e0::2]:9190/status\": EOF":   "Get \"http://[<addr>]:9190/status\": EOF",
		"public address 203.0.113.7 unreachable":                "public address <addr> unreachable",
		"endpoint 198.51.100.9:41641.":                          "endpoint <addr>.",
		"loopback ::1 and fe80::1 here":                         "loopback <addr> and <addr> here",
		"HTTP 500: tailscaled not answering at 21:31:05":        "HTTP 500: tailscaled not answering at 21:31:05",
		"schema 2, expected 1":                                  "schema 2, expected 1",
	}
	for in, want := range cases {
		if got := Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}
