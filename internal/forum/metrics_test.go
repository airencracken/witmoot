package forum

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOwnerMetricsTotalsPermissionsAndFailure(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	hash, err := HashPassword("a long test password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.CreateUser(t.Context(), "member", hash); err != nil {
		t.Fatal(err)
	}
	metrics, err := a.store.Metrics(t.Context(), time.Now())
	if err != nil || len(metrics) < 5 || metrics[0].Value != 2 {
		t.Fatal(metrics, err)
	}
	guest := &testClient{app: a, cookies: map[string]*http.Cookie{}}
	member := &testClient{app: a, cookies: map[string]*http.Cookie{}}
	member.request("GET", "/login", nil, nil)
	if w := member.post("/login", url.Values{"username": {"member"}, "password": {"a long test password"}}); w.Code != 303 {
		t.Fatal("member login", w.Code)
	}
	for _, path := range []string{"/admin/metrics", "/admin/metrics?owner=true&role=admin"} {
		w := owner.request("GET", path, nil, nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "Instance overview") {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, client := range []*testClient{member, guest} {
			w = client.request("GET", path, nil, nil)
			if w.Code != 303 && w.Code != 403 {
				t.Fatal("metrics authorization", w.Code)
			}
			if strings.Contains(w.Body.String(), "Instance overview") {
				t.Fatal("metrics leaked")
			}
		}
	}
	if _, err = a.store.db.Exec("ALTER TABLE topics RENAME TO unavailable_topics"); err != nil {
		t.Fatal(err)
	}
	if w := owner.request("GET", "/admin/metrics", nil, nil); w.Code != 500 || strings.Contains(w.Body.String(), "Instance overview") {
		t.Fatal("partial metrics published", w.Code)
	}
}
