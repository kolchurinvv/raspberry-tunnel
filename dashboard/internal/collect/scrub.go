package collect

import (
	"net/netip"
	"regexp"
	"strings"
)

// addrLike matches runs that could hold an address, with or without a port.
var addrLike = regexp.MustCompile(`[0-9A-Fa-f:.%]*[0-9A-Fa-f][0-9A-Fa-f:.%]*`)

// Scrub replaces every IP address, and every address with a port, in s with
// "<addr>". Errors from dialing and from the Pis can name tailnet or public
// addresses, and the log is no place to keep them.
func Scrub(s string) string {
	return addrLike.ReplaceAllStringFunc(s, func(tok string) string {
		// "dial tcp 100.64.0.2:9190: refused" ends the token with a colon.
		core := strings.TrimRight(tok, ":.")
		if isAddr(core) {
			return "<addr>" + tok[len(core):]
		}
		if isAddr(tok) {
			return "<addr>"
		}
		return tok
	})
}

func isAddr(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	_, err := netip.ParseAddrPort(s)
	return err == nil
}
