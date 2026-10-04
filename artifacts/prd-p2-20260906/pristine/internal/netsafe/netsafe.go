// Package netsafe builds HTTP clients that refuse to reach non-public
// addresses. It exists because MiniCloud makes outbound requests on behalf of
// tenants (cloud functions, webhooks), and those must never be usable to probe
// the platform's own network.
package netsafe

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

var ErrBlockedAddress = errors.New("destination is not a publicly routable address")

// blocked lists ranges tenant traffic must never reach: loopback, private,
// link-local (which includes the 169.254.169.254 cloud metadata service),
// CGNAT, multicast and reserved space.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// Blocked reports whether ip is outside publicly routable space.
func Blocked(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap() // ::ffff:127.0.0.1 must be judged as 127.0.0.1
	for _, p := range blocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Client returns an HTTP client that validates the resolved address at
// connect time. Checking after resolution — rather than looking up the host
// first and dialing separately — is what closes the DNS-rebinding hole: the
// address vetted here is the exact one the socket connects to.
//
// allowPrivate disables the check for local development.
func Client(timeout time.Duration, allowPrivate bool, checkRedirect func(*http.Request, []*http.Request) error) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || Blocked(ip) {
				return ErrBlockedAddress
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: checkRedirect,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
			DisableKeepAlives:     true,
			MaxIdleConns:          8,
		},
	}
}
