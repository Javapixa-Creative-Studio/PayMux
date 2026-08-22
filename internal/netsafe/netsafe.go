// Package netsafe guards PayMux's outbound HTTP requests against SSRF.
//
// PayMux delivers webhooks to URLs its own users configure, which makes it a
// confused deputy: without restrictions an application could point a
// destination at a private service or a cloud metadata endpoint and have
// PayMux fetch it from inside the network (PRD §73).
//
// Two layers cooperate:
//
//   - Validate rejects obviously unsafe URLs when a destination is saved, so
//     the operator gets immediate feedback.
//   - Guard's DialControl re-checks the resolved address at connection time.
//     That check is what defeats DNS rebinding, because a name that resolved
//     publicly during validation may resolve privately later.
package netsafe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
)

// ErrBlockedAddress reports a destination that resolves to a restricted range.
var ErrBlockedAddress = errors.New("netsafe: destination address is not permitted")

// Guard decides which outbound destinations are permitted.
type Guard struct {
	// AllowPrivate disables range blocking for self-hosted deployments whose
	// applications live on the same private network.
	AllowPrivate bool
	// AllowedPorts restricts destination ports. Empty means 80 and 443 only.
	AllowedPorts []int
}

// NewGuard builds a Guard.
func NewGuard(allowPrivate bool) *Guard {
	return &Guard{AllowPrivate: allowPrivate}
}

// blockedPrefixes are ranges no webhook destination may target. Link-local
// covers 169.254.169.254, the cloud metadata address that makes SSRF
// especially damaging.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // "this network"
	netip.MustParsePrefix("10.0.0.0/8"),         // private
	netip.MustParsePrefix("100.64.0.0/10"),      // carrier-grade NAT
	netip.MustParsePrefix("127.0.0.0/8"),        // loopback
	netip.MustParsePrefix("169.254.0.0/16"),     // link-local, cloud metadata
	netip.MustParsePrefix("172.16.0.0/12"),      // private
	netip.MustParsePrefix("192.0.0.0/24"),       // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),       // documentation
	netip.MustParsePrefix("192.88.99.0/24"),     // 6to4 relay anycast
	netip.MustParsePrefix("192.168.0.0/16"),     // private
	netip.MustParsePrefix("198.18.0.0/15"),      // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),    // documentation
	netip.MustParsePrefix("203.0.113.0/24"),     // documentation
	netip.MustParsePrefix("224.0.0.0/4"),        // multicast
	netip.MustParsePrefix("240.0.0.0/4"),        // reserved
	netip.MustParsePrefix("255.255.255.255/32"), // broadcast
	netip.MustParsePrefix("::/128"),             // unspecified
	netip.MustParsePrefix("::1/128"),            // loopback
	netip.MustParsePrefix("fc00::/7"),           // unique local
	netip.MustParsePrefix("fe80::/10"),          // link-local
	netip.MustParsePrefix("ff00::/8"),           // multicast
}

// AddrAllowed reports whether an already-resolved address may be contacted.
func (g *Guard) AddrAllowed(addr netip.Addr) bool {
	if g != nil && g.AllowPrivate {
		return true
	}
	if !addr.IsValid() {
		return false
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) must be judged as its IPv4 form,
	// otherwise a mapped loopback address would slip past the IPv4 prefixes.
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// Validate checks a webhook destination URL before it is stored.
//
// It resolves the host as a best effort: a name that cannot be resolved now is
// accepted, because DNS may simply not be configured yet, and DialControl will
// block it at delivery time if it resolves somewhere unsafe.
func (g *Guard) Validate(ctx context.Context, rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("netsafe: destination URL is malformed: %w", err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		// Plain HTTP leaks event payloads in transit. It is tolerated only
		// where private destinations are already permitted.
		if !g.AllowPrivate {
			return errors.New("netsafe: destination URL must use https")
		}
	default:
		return fmt.Errorf("netsafe: destination URL scheme %q is not supported", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("netsafe: destination URL must include a host")
	}
	if u.User != nil {
		return errors.New("netsafe: destination URL must not embed credentials")
	}
	if u.Fragment != "" {
		return errors.New("netsafe: destination URL must not include a fragment")
	}
	if err := g.checkPort(u); err != nil {
		return err
	}

	host := u.Hostname()
	if addr, err := netip.ParseAddr(host); err == nil {
		if !g.AddrAllowed(addr) {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
		}
		return nil
	}
	if strings.EqualFold(host, "localhost") && !g.AllowPrivate {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
	}

	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		// A name that will not resolve today is accepted: DNS may simply not
		// be configured yet, and DialControl blocks it at delivery time if it
		// later resolves somewhere unsafe. Rejecting here would make PayMux
		// refuse valid destinations during a transient resolver outage.
		return nil //nolint:nilerr // deliberate: DialControl is the real guard
	}
	for _, addr := range addrs {
		if !g.AddrAllowed(addr) {
			return fmt.Errorf("%w: %s resolves to %s", ErrBlockedAddress, host, addr)
		}
	}
	return nil
}

func (g *Guard) checkPort(u *url.URL) error {
	port := u.Port()
	if port == "" {
		return nil
	}
	allowed := g.AllowedPorts
	if len(allowed) == 0 {
		if g.AllowPrivate {
			return nil // self-hosted services routinely listen elsewhere
		}
		allowed = []int{80, 443}
	}
	var parsed int
	if _, err := fmt.Sscanf(port, "%d", &parsed); err != nil {
		return fmt.Errorf("netsafe: destination URL port %q is not a number", port)
	}
	for _, p := range allowed {
		if p == parsed {
			return nil
		}
	}
	return fmt.Errorf("netsafe: destination URL port %d is not permitted", parsed)
}

// DialControl returns a net.Dialer Control function that refuses connections
// to restricted addresses.
//
// Control runs after DNS resolution with the concrete address the socket is
// about to reach, which is precisely what makes it immune to a name that
// changes its answer between validation and delivery.
func (g *Guard) DialControl() func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		switch network {
		case "tcp", "tcp4", "tcp6":
		default:
			return fmt.Errorf("netsafe: network %q is not permitted", network)
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("netsafe: cannot parse dial address %q: %w", address, err)
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("netsafe: cannot parse dial address %q: %w", host, err)
		}
		if !g.AddrAllowed(addr) {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, addr)
		}
		return nil
	}
}
