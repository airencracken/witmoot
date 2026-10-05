package main

import (
	"bytes"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
)

func TestLoopbackWithoutTrustedProxiesWarnsAboutSharedLoginLimits(t *testing.T) {
	previous := slog.Default()
	defer slog.SetDefault(previous)
	for _, test := range []struct {
		address string
		trusted []netip.Prefix
		warning bool
	}{
		{"127.0.0.1:8082", nil, true}, {"[::1]:8082", nil, true}, {"localhost:8082", nil, true},
		{"0.0.0.0:8082", nil, false}, {"127.0.0.1:8082", []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, false},
	} {
		var output bytes.Buffer
		slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
		warnUntrustedProxy(test.address, test.trusted)
		if strings.Contains(output.String(), "WITMOOT_TRUSTED_PROXIES") != test.warning {
			t.Fatalf("warning for %s=%q", test.address, output.String())
		}
	}
}
