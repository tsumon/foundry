// Package mcp implements bounded MCP catalog discovery and explicitly gated,
// single-shot loopback tool calls.
package mcp

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ValidateEndpoint performs syntax/address checks without DNS or network I/O.
func ValidateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 4096 || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(endpoint, "#") || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return failure("protocol_error", false)
	}
	if u.Port() != "" {
		p, e := strconv.Atoi(u.Port())
		if e != nil || p < 1 || p > 65535 {
			return failure("protocol_error", false)
		}
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err == nil {
		if ip.Zone() != "" || (u.Scheme == "http" && !ip.IsLoopback()) || (!ip.IsLoopback() && !publicAddress(ip)) {
			return failure("protocol_error", false)
		}
	} else if u.Scheme == "http" || strings.ContainsAny(u.Hostname(), ":%\\") {
		return failure("protocol_error", false)
	}
	return nil
}

// validateCallEndpoint is deliberately narrower than discovery: R4 calls are
// permitted only over plain HTTP to an IP-literal loopback address.
func validateCallEndpoint(endpoint string) error {
	if err := ValidateEndpoint(endpoint); err != nil {
		return err
	}
	u, _ := url.Parse(endpoint)
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || u.Scheme != "http" || ip.Zone() != "" || !ip.IsLoopback() {
		return failure("invalid_connection", false)
	}
	return nil
}

func newCallHTTPClient(endpoint string) (*http.Client, *http.Transport, error) {
	if err := validateCallEndpoint(endpoint); err != nil {
		return nil, nil, err
	}
	client, transport, err := newHTTPClient(endpoint)
	if err != nil {
		return nil, nil, err
	}
	// The call itself may use the full overall deadline. Initialize/list still
	// carry their own five-second request contexts, while discovery keeps the
	// base transport's five-second header timeout.
	transport.ResponseHeaderTimeout = 30 * time.Second
	return client, transport, nil
}

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Reject special-use/shared/benchmark/documentation/transition ranges as well
	// as RFC1918. IPv6 is limited to global unicast without mapped/NAT64 escapes.
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return ip.Is4() || netip.MustParsePrefix("2000::/3").Contains(ip)
}

type lookupIP func(context.Context, string, string) ([]net.IP, error)

func pinnedDial(endpoint *url.URL, lookup lookupIP) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host != endpoint.Hostname() {
			return nil, errors.New("destination rejected")
		}
		expectedPort := endpoint.Port()
		if expectedPort == "" {
			expectedPort = "443"
			if endpoint.Scheme == "http" {
				expectedPort = "80"
			}
		}
		if port != expectedPort {
			return nil, errors.New("destination rejected")
		}
		literal, err := netip.ParseAddr(host)
		var chosen netip.Addr
		if err == nil {
			chosen = literal
			if !chosen.IsLoopback() && !publicAddress(chosen) {
				return nil, errors.New("destination rejected")
			}
		} else {
			addresses, e := lookup(ctx, "ip", host)
			if e != nil || len(addresses) == 0 {
				return nil, errors.New("destination unavailable")
			}
			for _, a := range addresses {
				ip, ok := netip.AddrFromSlice(a)
				if !ok || !publicAddress(ip) {
					return nil, errors.New("destination rejected")
				}
				if !chosen.IsValid() {
					chosen = ip.Unmap()
				}
			}
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(chosen.String(), port))
	}
}

func newHTTPClient(endpoint string) (*http.Client, *http.Transport, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, nil, err
	}
	u, _ := url.Parse(endpoint)
	transport := &http.Transport{Proxy: nil, DialContext: pinnedDial(u, net.DefaultResolver.LookupIP), TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableCompression: true, DisableKeepAlives: true, ForceAttemptHTTP2: false, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport, nil
}
