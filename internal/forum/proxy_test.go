package forum

import (
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestTrustedProxyConfiguration(t *testing.T) {
	for _, invalid := range []string{"localhost", "127.0.0.1:8080", "127.0.0.1,", "127.0.0.1/33", "fe80::1%eth0", "::ffff:127.0.0.1/80"} {
		if _, err := ParseTrustedProxies(invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
	proxies, err := ParseTrustedProxies(" 127.0.0.1, ::1/128, 10.1.2.3/8, ::ffff:192.0.2.1/120 ")
	if err != nil || len(proxies) != 4 {
		t.Fatalf("parse: %v %v", proxies, err)
	}
	for i, want := range []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8", "192.0.2.0/24"} {
		if proxies[i].String() != want {
			t.Errorf("proxy %d = %s; want %s", i, proxies[i], want)
		}
	}
	if proxies, err := ParseTrustedProxies(" "); err != nil || len(proxies) != 0 {
		t.Fatal("empty configuration should trust no proxies")
	}
}

func TestForwardedClientTrustBoundary(t *testing.T) {
	proxies, err := ParseTrustedProxies("127.0.0.1,::1,10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{config: Config{TrustedProxies: proxies}}
	for _, tc := range []struct {
		name, peer string
		headers    []string
		want       string
	}{
		{"direct ignores spoof", "192.0.2.9:2345", []string{"198.51.100.1"}, "192.0.2.9"},
		{"local proxy", "127.0.0.1:2345", []string{"198.51.100.1"}, "198.51.100.1"},
		{"IPv6 proxy and client", "[::1]:2345", []string{"2001:db8::1234"}, "2001:db8::1234"},
		{"normalize mapped peer", "[::ffff:127.0.0.1]:2345", []string{"::ffff:198.51.100.1"}, "198.51.100.1"},
		{"ignore spoofed left entry", "127.0.0.1:2345", []string{"203.0.113.6, 198.51.100.1"}, "198.51.100.1"},
		{"trusted chain", "127.0.0.1:2345", []string{"203.0.113.6, 198.51.100.1, 10.2.3.4"}, "198.51.100.1"},
		{"multiple header fields", "127.0.0.1:2345", []string{"203.0.113.6", "198.51.100.1, 10.2.3.4"}, "198.51.100.1"},
		{"client in trusted range", "127.0.0.1:2345", []string{"10.3.4.5"}, "10.3.4.5"},
		{"missing header", "127.0.0.1:2345", nil, "127.0.0.1"},
		{"malformed nearest hop", "127.0.0.1:2345", []string{"198.51.100.1, garbage"}, "127.0.0.1"},
		{"malformed middle hop", "127.0.0.1:2345", []string{"198.51.100.1, garbage, 10.2.3.4"}, "127.0.0.1"},
		{"trailing comma", "127.0.0.1:2345", []string{"198.51.100.1,"}, "127.0.0.1"},
		{"port is not an IP", "127.0.0.1:2345", []string{"198.51.100.1:1234"}, "127.0.0.1"},
		{"zone is not accepted", "127.0.0.1:2345", []string{"fe80::1%eth0"}, "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/login", nil)
			r.RemoteAddr = tc.peer
			for _, header := range tc.headers {
				r.Header.Add("X-Forwarded-For", header)
			}
			if got := a.clientIP(r); got != tc.want {
				t.Fatalf("client = %s; want %s", got, tc.want)
			}
		})
	}
	a.config.TrustedProxies = nil
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = "127.0.0.1:2345"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := a.clientIP(r); got != "127.0.0.1" {
		t.Fatalf("default trusted forwarded header: %s", got)
	}
}

func TestProxyVisitorsHaveSeparateAuthenticationBudgets(t *testing.T) {
	a, c := newTestApp(t, false)
	a.config.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	c.request("GET", "/login", nil, nil)
	form := url.Values{"username": {"nobody"}, "password": {"incorrect"}, "csrf": {c.cookies[a.cookieName("csrf")].Value}}
	// Varying an attacker-controlled prefix must not reset this visitor's budget.
	for i := 0; i < 20; i++ {
		headers := map[string]string{"X-Forwarded-For": strings.Repeat("1", i%3+1) + ".0.0.1, 198.51.100.10"}
		requireStatus(t, c.request("POST", "/login", form, headers), 422)
	}
	requireStatus(t, c.request("POST", "/login", form, map[string]string{"X-Forwarded-For": "198.51.100.10"}), 429)
	requireStatus(t, c.request("POST", "/login", form, map[string]string{"X-Forwarded-For": "198.51.100.20"}), 422)
	// Registration consumes the same identity budget as sign-in.
	requireStatus(t, c.request("POST", "/join", form, map[string]string{"X-Forwarded-For": "198.51.100.10"}), 429)
}
