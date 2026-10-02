package forum

import (
	"crypto/tls"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/clientip"
)

// The parsing and the walk along X-Forwarded-For are comfylib's; this checks
// that the board counts attempts against what WITMOOT_TRUSTED_PROXIES allows.
func TestRateKeysFollowTheTrustedProxies(t *testing.T) {
	proxies, err := clientip.ParseTrusted("127.0.0.1,::1,10.0.0.0/8", "WITMOOT_TRUSTED_PROXIES")
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
		{"IPv6 proxy and client", "[::1]:2345", []string{"2001:db8::1234"}, "2001:db8::/64"},
		{"normalize mapped peer", "[::ffff:127.0.0.1]:2345", []string{"::ffff:198.51.100.1"}, "198.51.100.1"},
		{"ignore spoofed left entry", "127.0.0.1:2345", []string{"203.0.113.6, 198.51.100.1"}, "198.51.100.1"},
		{"trusted chain", "127.0.0.1:2345", []string{"203.0.113.6", "198.51.100.1, 10.2.3.4"}, "198.51.100.1"},
		{"malformed nearest hop", "127.0.0.1:2345", []string{"198.51.100.1, garbage"}, "127.0.0.1"},
		{"direct IPv6 client", "[2001:db8:5:6:7::8]:2345", nil, "2001:db8:5:6::/64"},
		{"unparsable peer", "not an address", []string{"198.51.100.1"}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/login", nil)
			r.RemoteAddr = tc.peer
			for _, header := range tc.headers {
				r.Header.Add("X-Forwarded-For", header)
			}
			if got := a.rateKey(r); got != tc.want {
				t.Fatalf("rate key = %s; want %s", got, tc.want)
			}
		})
	}
	a.config.TrustedProxies = nil
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = "127.0.0.1:2345"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := a.rateKey(r); got != "127.0.0.1" {
		t.Fatalf("default trusted forwarded header: %s", got)
	}
}

// Shareable links built from the request use HTTPS when a trusted proxy says
// the visitor used it, and never because an untrusted client claims so.
func TestLinkOriginsFollowTheTrustedProxy(t *testing.T) {
	a := &App{config: Config{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}}
	for _, tc := range []struct {
		peer, proto, want string
		tls               bool
	}{
		{"127.0.0.1:2345", "https", "https://board.example.org", false},
		{"127.0.0.1:2345", "http", "http://board.example.org", false},
		{"127.0.0.1:2345", "", "http://board.example.org", false},
		{"127.0.0.1:2345", "http, https", "https://board.example.org", false},
		{"127.0.0.1:2345", "https, http", "http://board.example.org", false},
		{"192.0.2.9:2345", "https", "http://board.example.org", false},
		{"192.0.2.9:2345", "", "https://board.example.org", true},
	} {
		r := httptest.NewRequest("GET", "/invites", nil)
		r.Host, r.RemoteAddr = "board.example.org", tc.peer
		if tc.proto != "" {
			r.Header.Set("X-Forwarded-Proto", tc.proto)
		}
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if got := a.origin(r); got != tc.want {
			t.Errorf("%s with X-Forwarded-Proto %q: origin %s, want %s", tc.peer, tc.proto, got, tc.want)
		}
	}
	a.config.SecureCookies = true
	r := httptest.NewRequest("GET", "/invites", nil)
	r.Host = "board.example.org"
	if got := a.origin(r); got != "https://board.example.org" {
		t.Errorf("secure cookies imply HTTPS: %s", got)
	}
	a.config.BaseURL = "https://configured.example.org"
	if got := a.origin(r); got != a.config.BaseURL {
		t.Errorf("WITMOOT_BASE_URL wins: %s", got)
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
