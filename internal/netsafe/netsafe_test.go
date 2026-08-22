package netsafe

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestAddrAllowedBlocksRestrictedRanges(t *testing.T) {
	g := NewGuard(false)
	blocked := []string{
		"127.0.0.1",              // loopback
		"169.254.169.254",        // cloud metadata
		"10.1.2.3",               // private
		"172.16.0.1",             // private
		"192.168.1.1",            // private
		"100.64.0.1",             // CGNAT
		"0.0.0.0",                // this network
		"::1",                    // IPv6 loopback
		"fd00::1",                // IPv6 unique local
		"fe80::1",                // IPv6 link-local
		"::ffff:127.0.0.1",       // IPv4-mapped loopback
		"::ffff:169.254.169.254", // IPv4-mapped metadata
	}
	for _, ip := range blocked {
		addr := netip.MustParseAddr(ip)
		if g.AddrAllowed(addr) {
			t.Errorf("AddrAllowed(%s) = true, want false", ip)
		}
	}

	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		if !g.AddrAllowed(netip.MustParseAddr(ip)) {
			t.Errorf("AddrAllowed(%s) = false, want true", ip)
		}
	}
}

func TestAllowPrivateOptsOut(t *testing.T) {
	g := NewGuard(true)
	if !g.AddrAllowed(netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("AllowPrivate did not permit loopback")
	}
}

func TestValidateRejectsUnsafeURLs(t *testing.T) {
	g := NewGuard(false)
	cases := []struct {
		name, url, wantContains string
	}{
		{"loopback", "https://127.0.0.1/hook", "not permitted"},
		{"metadata", "https://169.254.169.254/latest/meta-data", "not permitted"},
		{"localhost", "https://localhost/hook", "not permitted"},
		{"private", "https://192.168.0.10/hook", "not permitted"},
		{"plain http", "http://example.com/hook", "must use https"},
		{"unsupported scheme", "file:///etc/passwd", "not supported"},
		{"gopher", "gopher://example.com", "not supported"},
		{"credentials", "https://user:pass@example.com/hook", "credentials"},
		{"no host", "https:///hook", "must include a host"},
		{"odd port", "https://example.com:22/hook", "port 22 is not permitted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := g.Validate(context.Background(), tc.url)
			if err == nil {
				t.Fatalf("Validate(%q) = nil, want error", tc.url)
			}
			if !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("Validate(%q) error = %q, want it to mention %q", tc.url, err, tc.wantContains)
			}
		})
	}
}

func TestValidateAcceptsPublicHTTPS(t *testing.T) {
	g := NewGuard(false)
	for _, u := range []string{
		"https://product-a.example.com/webhooks/paymux",
		"https://example.com:443/hook",
		"https://8.8.8.8/hook",
	} {
		if err := g.Validate(context.Background(), u); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", u, err)
		}
	}
}

func TestValidateAllowsPrivateWhenConfigured(t *testing.T) {
	g := NewGuard(true)
	for _, u := range []string{
		"http://product-a.internal:9000/webhooks/paymux",
		"http://127.0.0.1:8080/hook",
	} {
		if err := g.Validate(context.Background(), u); err != nil {
			t.Errorf("Validate(%q) with AllowPrivate = %v, want nil", u, err)
		}
	}
}

func TestDialControlBlocksResolvedPrivateAddress(t *testing.T) {
	control := NewGuard(false).DialControl()

	if err := control("tcp", "169.254.169.254:80", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("DialControl allowed the metadata address: %v", err)
	}
	if err := control("tcp", "127.0.0.1:8080", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("DialControl allowed loopback: %v", err)
	}
	if err := control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Fatalf("DialControl blocked a public address: %v", err)
	}
	if err := control("udp", "93.184.216.34:443", nil); err == nil {
		t.Fatal("DialControl allowed a non-TCP network")
	}
}

func TestDialControlHonoursAllowPrivate(t *testing.T) {
	control := NewGuard(true).DialControl()
	if err := control("tcp", "127.0.0.1:8080", nil); err != nil {
		t.Fatalf("AllowPrivate guard blocked loopback: %v", err)
	}
}
