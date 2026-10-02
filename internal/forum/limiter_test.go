package forum

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestRateKeysGroupIPv6Networks(t *testing.T) {
	for _, tc := range []struct{ client, key string }{
		{"198.51.100.7", "198.51.100.7"},
		{"::ffff:198.51.100.7", "198.51.100.7"},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
		{"2001:db8:1:2::ffff", "2001:db8:1:2::/64"},
		{"fe80::1%eth0", "fe80::/64"},
		{"not an address", "not an address"},
	} {
		if got := rateKey(tc.client); got != tc.key {
			t.Errorf("rateKey(%q) = %q, want %q", tc.client, got, tc.key)
		}
	}
}

func TestIPv6AddressesInOneNetworkShareABudget(t *testing.T) {
	l := newLimiter()
	allowed := 0
	for i := range 1000 {
		if l.allow(fmt.Sprintf("2001:db8:0:1::%x", i+1)) {
			allowed++
		}
	}
	if allowed != rateAttempts || len(l.entries) != 1 {
		t.Fatalf("one /64 got %d attempts across %d records", allowed, len(l.entries))
	}
	if !l.allow("2001:db8:0:2::1") {
		t.Fatal("a neighbouring /64 was locked out")
	}
}

func TestFloodedLimiterStillAdmitsNewClients(t *testing.T) {
	l := newLimiter()
	for i := range rateClients + 500 {
		l.allow(fmt.Sprintf("2001:db8:%x:%x::1", i>>16, i&0xffff))
	}
	if len(l.entries) > rateClients || l.order.Len() != len(l.entries) {
		t.Fatalf("limiter grew to %d records (%d in order)", len(l.entries), l.order.Len())
	}
	if !l.allow("198.51.100.7") {
		t.Fatal("a full limiter locked out a new client")
	}
}

func TestExpiredBudgetsAreForgotten(t *testing.T) {
	l := newLimiter()
	for l.allow("198.51.100.7") {
	}
	l.order.Front().Value.(*rateEntry).until = time.Now().Add(-time.Second)
	if !l.allow("198.51.100.7") || len(l.entries) != 1 {
		t.Fatal("an expired budget still blocked its client")
	}
}

// Under any sequence of attempts and refunds the limiter stays bounded, its
// index and order agree, and no client exceeds its budget within a window.
func TestLimiterPropertiesUnderRandomTraffic(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	for round := range 20 {
		l := newLimiter()
		granted := map[string]int{}
		for range 30000 {
			var client string
			switch random.IntN(3) {
			case 0:
				client = fmt.Sprintf("10.%d.%d.%d", random.IntN(4), random.IntN(256), random.IntN(256))
			case 1:
				client = fmt.Sprintf("2001:db8:%x:%x::%x", random.IntN(8), random.IntN(4096), random.IntN(65536))
			default:
				client = fmt.Sprintf("2001:db8:ffff:1::%x", random.IntN(65536))
			}
			if random.IntN(10) == 0 {
				l.refund(client)
				continue
			}
			if l.allow(client) {
				granted[rateKey(client)]++
			}
		}
		if len(l.entries) > rateClients || l.order.Len() != len(l.entries) {
			t.Fatalf("round %d: %d records, %d ordered", round, len(l.entries), l.order.Len())
		}
		for element := l.order.Front(); element != nil; element = element.Next() {
			entry := element.Value.(*rateEntry)
			if l.entries[entry.key] != element || entry.count < 0 || entry.count > rateAttempts {
				t.Fatalf("round %d: inconsistent record %+v", round, entry)
			}
		}
	}
}

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
