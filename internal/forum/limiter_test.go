package forum

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// requestFrom is a sign-in attempt arriving directly from address.
func requestFrom(address string) *http.Request {
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = net.JoinHostPort(address, "41000")
	return r
}

func TestIPv6AddressesInOneNetworkShareABudget(t *testing.T) {
	a := &App{}
	l := newLimiter()
	allowed := 0
	for i := range 1000 {
		if l.allow(a.rateKey(requestFrom(fmt.Sprintf("2001:db8:0:1::%x", i+1)))) {
			allowed++
		}
	}
	if allowed != rateAttempts || l.shared.Keys() != 1 {
		t.Fatalf("one /64 got %d attempts across %d records", allowed, l.shared.Keys())
	}
	if !l.allow(a.rateKey(requestFrom("2001:db8:0:2::1"))) {
		t.Fatal("a neighbouring /64 was locked out")
	}
}

// Through the routes, too: rotating addresses inside one /64 does not buy a
// fresh budget, while IPv4 neighbours keep their own.
func TestSignInBudgetCoversTheWholeIPv6Network(t *testing.T) {
	app, client := newTestApp(t, false)
	client.request("GET", "/login", nil, nil)
	form := url.Values{"username": {"nobody"}, "password": {"incorrect"}, "csrf": {client.cookies[app.cookieName("csrf")].Value}}
	attempt := func(address string) int {
		r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
		r.RemoteAddr = net.JoinHostPort(address, "41000")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, cookie := range client.cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code
	}
	for i := range rateAttempts {
		if code := attempt(fmt.Sprintf("2001:db8:7:7::%x", i+1)); code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code := attempt("2001:db8:7:7:ffff::1"); code != http.StatusTooManyRequests {
		t.Fatalf("a fresh address in the same /64 got status %d", code)
	}
	if code := attempt("2001:db8:7:8::1"); code != http.StatusUnprocessableEntity {
		t.Fatalf("the next /64 got status %d", code)
	}
}

// Under any sequence of attempts and refunds the limiter stays bounded, its
// index and order agree, and no client exceeds its budget within a window.

func TestSuccessfulSignInsDoNotSpendTheBudget(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	for i := range rateAttempts + 5 {
		requireStatus(t, client.post("/logout", nil), http.StatusSeeOther)
		w := client.post("/login", url.Values{"username": {"alex"}, "password": {"a long test password"}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("sign-in %d was refused: %d", i, w.Code)
		}
	}
	requireStatus(t, client.post("/logout", nil), http.StatusSeeOther)
	for range rateAttempts {
		client.post("/login", url.Values{"username": {"alex"}, "password": {"wrong password!"}})
	}
	requireStatus(t, client.post("/login", url.Values{"username": {"alex"}, "password": {"a long test password"}}), http.StatusTooManyRequests)
}
