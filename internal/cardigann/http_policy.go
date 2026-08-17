package cardigann

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// NewRestrictedHTTPClient returns the production transport for local tracker
// definitions. Every initial request, redirect, DNS answer, and actual dial is
// constrained to public HTTP(S) destinations.
func NewRestrictedHTTPClient(timeout time.Duration) *http.Client {
	defaultTransport := http.DefaultTransport
	baseTransport, ok := defaultTransport.(*http.Transport)
	if !ok {
		// Tests and embedding applications may install a routing transport. The
		// URL/redirect policy still applies; dial-time address validation remains
		// the responsibility of that custom transport.
		return restrictHTTPClient(&http.Client{Timeout: timeout, Transport: defaultTransport})
	}
	transport := baseTransport.Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialValidatedAddress(ctx, network, address, publicIPs, dialer.DialContext)
	}
	return restrictHTTPClient(&http.Client{Timeout: timeout, Transport: transport})
}

func dialValidatedAddress(
	ctx context.Context,
	network, address string,
	lookup func(context.Context, string) ([]net.IP, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("validate outbound address: %w", err)
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialErr error
	for _, ip := range ips {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, fmt.Errorf("dial public tracker: %w", dialErr)
}

func restrictHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = outboundPolicyTransport{next: transport}
	previousRedirectPolicy := base.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validatePublicURL(req.Context(), req.URL); err != nil {
			return err
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &client
}

type outboundPolicyTransport struct{ next http.RoundTripper }

func (t outboundPolicyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validatePublicURL(req.Context(), req.URL); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(req)
}

func validatePublicURL(ctx context.Context, target *url.URL) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil {
		return fmt.Errorf("outbound tracker URL must be HTTP(S) without user information")
	}
	if _, err := publicIPs(ctx, target.Hostname()); err != nil {
		return err
	}
	return nil
}

func publicIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("outbound tracker URL must resolve to a public network")
		}
		return []net.IP{ip}, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve outbound tracker host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("outbound tracker host has no addresses")
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			return nil, fmt.Errorf("outbound tracker URL must resolve to a public network")
		}
		ips = append(ips, address.IP)
	}
	return ips, nil
}

var blockedSpecialUsePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	for _, prefix := range blockedSpecialUsePrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
