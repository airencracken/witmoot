package forum

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies accepts explicit proxy IPs and CIDRs. Empty means that
// forwarded headers have no authority, including requests from loopback.
func ParseTrustedProxies(value string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var proxies []netip.Prefix
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			addr, addrErr := netip.ParseAddr(entry)
			if addrErr != nil || addr.Zone() != "" {
				return nil, fmt.Errorf("WITMOOT_TRUSTED_PROXIES: invalid proxy IP or CIDR %q", entry)
			}
			addr = addr.Unmap()
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return nil, fmt.Errorf("WITMOOT_TRUSTED_PROXIES: use an IPv4 CIDR for %q", entry)
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		proxies = append(proxies, prefix.Masked())
	}
	return proxies, nil
}

func (a *App) trustedProxy(addr netip.Addr) bool {
	for _, prefix := range a.config.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (a *App) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !a.trustedProxy(peer) {
		return peer.String()
	}
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if forwarded == "" {
		return peer.String()
	}
	// Work back from the trusted peer. Values to the left of the first
	// untrusted hop are supplied by that client and cannot establish identity.
	hops := strings.Split(forwarded, ",")
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		client, err = netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil || client.Zone() != "" {
			return peer.String()
		}
		client = client.Unmap()
		if !a.trustedProxy(client) {
			return client.String()
		}
	}
	return client.String()
}
