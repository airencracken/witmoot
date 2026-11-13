// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/analytics/analyticstest"
)

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var found int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&found); err != nil {
		t.Fatal(err)
	}
	return found == 1
}

func activity(t *testing.T, a *App) (summary struct {
	Requests, FailedSignIns, RateLimited, RejectedTokens int64
	Routes                                               []string
}) {
	t.Helper()
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	report, err := a.store.Activity(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	summary.Requests, summary.FailedSignIns, summary.RateLimited, summary.RejectedTokens = report.Requests, report.FailedSignIns, report.RateLimited, report.RejectedTokens
	rows, err := a.store.db.Query("SELECT route FROM comfy_analytics_routes")
	if err != nil {
		t.Fatal(err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var route string
		if err := rows.Scan(&route); err != nil {
			t.Fatal(err)
		}
		summary.Routes = append(summary.Routes, route)
	}
	return summary
}

// Site health counts requests by route pattern and security events by day,
// with nothing that names a member, a conversation or an address.
func TestSiteHealthCountsWithoutIdentifiers(t *testing.T) {
	a, owner := newTestApp(t, false)
	id := signInTest(t, a, owner, true)
	member := memberClient(t, a, "jules")
	topic, err := a.store.CreateTopic(t.Context(), 1, id, "Secret soup plans", "first post", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []*testClient{owner, member} {
		for _, path := range []string{fmt.Sprintf("/topics/%d", topic), "/recent", fmt.Sprintf("/topics/%d?q=jules", topic), "/topics/9999999"} {
			client.request("GET", path, nil, nil)
		}
	}
	visitor := &testClient{app: a, cookies: map[string]*http.Cookie{}}
	visitor.request("GET", "/login", nil, nil)
	visitor.post("/login", url.Values{"username": {"jules"}, "password": {"not the password at all"}})
	visitor.request("GET", "/reset/"+strings.Repeat("a", 64), nil, nil)
	visitor.request("GET", "/reset/short", nil, nil)
	visitor.post("/join", url.Values{"username": {"robin"}, "password": {"a long enough password"}, "invite": {strings.Repeat("b", 64)}})

	got := activity(t, a)
	if got.Requests < 10 || got.FailedSignIns != 1 || got.RejectedTokens != 3 || got.RateLimited != 0 {
		t.Fatalf("unexpected counts: %+v", got)
	}
	pattern := regexp.MustCompile(`^[A-Z]+ /[a-z0-9/{}.$-]*$`)
	for _, route := range got.Routes {
		if route == "unmatched" {
			continue
		}
		if !pattern.MatchString(route) || strings.Contains(route, fmt.Sprint(topic)) && !strings.Contains(route, "{") {
			t.Errorf("route %q is not a bare route pattern", route)
		}
		for _, secret := range []string{"jules", "alex", "soup", "?", "9999999", strings.Repeat("a", 64)} {
			if strings.Contains(route, secret) {
				t.Errorf("route %q leaks %q", route, secret)
			}
		}
	}
	if tableExists(t, a.store.db, "comfy_analytics_activity") {
		t.Fatal("the per-member activity table still exists")
	}
	body := owner.request("GET", "/admin/metrics", nil, nil).Body.String()
	for _, want := range []string{"Site health", "Security", "Conversations", "Database size"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics page lacks %q", want)
		}
	}
	for _, gone := range []string{"Recent sign-ins", "Popular", "jules", "Secret soup plans"} {
		if strings.Contains(body, gone) {
			t.Errorf("metrics page shows %q", gone)
		}
	}
	if _, err = a.store.db.Exec("UPDATE users SET suspended=1,role='member' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if w := owner.request("GET", "/admin/metrics?role=owner", nil, nil); w.Code == 200 || strings.Contains(w.Body.String(), "Site health") {
		t.Fatal("suspended owner read metrics", w.Code)
	}
}

// Running out of attempts is counted once per refused request, and hostile
// input never becomes a stored route.
func TestSiteHealthCountsRateLimitsAndIgnoresHostileInput(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	visitor := &testClient{app: a, cookies: map[string]*http.Cookie{}}
	visitor.request("GET", "/login", nil, nil)
	for range rateAttempts + 2 {
		visitor.post("/login", url.Values{"username": {"' OR 1=1 --"}, "password": {"<script>alert(1)</script>"}})
	}
	for _, path := range []string{"/topics/1%27%20OR%201=1", "/images/%3Cscript%3E?open=1", "/no/such/page", "/%2e%2e/etc/passwd"} {
		owner.request("GET", path, nil, nil)
	}
	got := activity(t, a)
	if got.RateLimited != 2 || got.FailedSignIns != int64(rateAttempts) {
		t.Fatalf("unexpected security counts: %+v", got)
	}
	for _, route := range got.Routes {
		for _, hostile := range []string{"script", "OR", "passwd", "..", "%"} {
			if strings.Contains(route, hostile) {
				t.Errorf("route %q stores hostile input %q", route, hostile)
			}
		}
	}
}

// Request counts wait in memory until Flush, which the server calls on
// shutdown.
func TestFlushSavesPendingRequestCounts(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	owner.request("GET", "/recent", nil, nil)
	var before int
	if err := a.store.db.QueryRow("SELECT count(*) FROM comfy_analytics_routes").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := a.store.db.QueryRow("SELECT count(*) FROM comfy_analytics_routes WHERE route='GET /recent'").Scan(&after); err != nil || after != 1 {
		t.Fatalf("GET /recent rows after flush: %d %v (before %d)", after, err, before)
	}
}

// Upgrading drops the per-member and per-item table earlier versions kept,
// and leaves everything else in place.
func TestForgetActivityMigrationDropsIdentifyingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "before-forget.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	last := len(migrationFiles) - 1
	for i, name := range migrationFiles[:last] {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body) + fmt.Sprintf("; PRAGMA user_version=%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	// The table as Comfylib 0.5 and 0.6 created it.
	if _, err := db.Exec(`CREATE TABLE comfy_analytics_activity (day INTEGER NOT NULL, kind TEXT NOT NULL, member TEXT NOT NULL, item TEXT NOT NULL, count INTEGER NOT NULL, last_at INTEGER NOT NULL, PRIMARY KEY(day,kind,member,item));
		INSERT INTO comfy_analytics_activity VALUES (1,'login','1','',1,1),(1,'post','1','1',1,1),(1,'image','2','7',1,1);
		INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'member','hash','member',1);
		INSERT INTO topics(id,board_id,author_id,title,created_at,updated_at,audience) VALUES(1,1,1,'Kept conversation',1,1,'members');
		INSERT INTO posts(id,topic_id,author_id,body,created_at) VALUES(1,1,1,'Kept message',1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, s)
	if tableExists(t, s.db, "comfy_analytics_activity") {
		t.Fatal("migration kept the per-member activity table")
	}
	posts, _, err := s.RecentPosts(context.Background(), 20, 0, &User{ID: 1, Role: "member"})
	if err != nil || len(posts) != 1 || posts[0].Body != "Kept message" {
		t.Fatalf("migration changed messages: %+v %v", posts, err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != len(migrationFiles) {
		t.Fatalf("schema=%d err=%v", version, err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), fmt.Sprintf("witmoot-before-v%d-*", last), "witmoot.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("missing pre-migration backup: %v %v", backups, err)
	}
}

// A brand-new database migrates and passes the shared storage checks.
func TestFreshDatabaseHasOnlySharedSiteHealthStorage(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, s)
	if tableExists(t, s.db, "comfy_analytics_activity") {
		t.Fatal("fresh database has the per-member activity table")
	}
	analyticstest.Run(t, s.db)
}
