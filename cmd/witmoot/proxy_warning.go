package main

import (
	"log/slog"
	"net"
	"net/netip"
)

func warnUntrustedProxy(address string, trusted []netip.Prefix) {
	if len(trusted) > 0 {
		return
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return
	}
	ip := net.ParseIP(host)
	if host == "localhost" || ip != nil && ip.IsLoopback() {
		slog.Warn("WITMOOT_TRUSTED_PROXIES is unset on a loopback listener. If a reverse proxy serves this site, configure its IP address; otherwise every visitor shares one login rate limit.")
	}
}
