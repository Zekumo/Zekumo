package netsafe

import (
	"net/netip"
	"testing"
)

func TestBlocked(t *testing.T) {
	for _, ip := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", // cloud metadata
		"100.64.0.1",      // CGNAT
		"0.1.2.3", "255.255.255.255", "198.18.0.1", "224.0.0.1",
		"::1", "fd00::1", "fe80::1", "fec0::1", "ff01::1",
		"::ffff:127.0.0.1", // IPv4-mapped loopback
	} {
		if !Blocked(netip.MustParseAddr(ip)) {
			t.Errorf("Blocked(%s) = false, want true", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700::1111"} {
		if Blocked(netip.MustParseAddr(ip)) {
			t.Errorf("Blocked(%s) = true, want false", ip)
		}
	}
	if !Blocked(netip.Addr{}) {
		t.Error("the zero Addr must be blocked")
	}
}
