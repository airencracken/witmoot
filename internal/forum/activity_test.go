// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/analytics"
)

func activityRows(t *testing.T, s *Store) int {
	t.Helper()
	var rows int
	if err := s.db.QueryRow("SELECT count(*) FROM comfy_analytics_activity").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// Signing in, reading conversations and opening images leave no record of
// who did it or what they read.
func TestActivityRecordsNoMemberOrContentIdentifiers(t *testing.T) {
	a, owner := newTestApp(t, false)
	id := signInTest(t, a, owner, true)
	member := memberClient(t, a, "jules")
	topic, err := a.store.CreateTopic(t.Context(), 1, id, "Activity fixture", "first post", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.store.Reply(t.Context(), topic, id, "second post"); err != nil {
		t.Fatal(err)
	}
	for _, client := range []*testClient{owner, member} {
		for _, path := range []string{fmt.Sprintf("/topics/%d", topic), fmt.Sprintf("/topics/%04d", topic), "/recent", "/images/1?open=1", "/topics/9999999", "/topics/abc"} {
			client.request("GET", path, nil, nil)
		}
		client.request("HEAD", fmt.Sprintf("/topics/%d", topic), nil, nil)
	}
	if err := a.store.NewSession(context.Background(), "rotation", id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := activityRows(t, a.store); n != 0 {
		t.Fatalf("activity table holds %d identifying rows", n)
	}
	w := owner.request("GET", "/admin/metrics", nil, nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "comfy-admin-dashboard") {
		t.Fatal(w.Code, body)
	}
	for _, gone := range []string{"Recent sign-ins", "comfy-activity-title", "Popular", "jules", "Activity fixture"} {
		if strings.Contains(body, gone) {
			t.Errorf("metrics page shows %q", gone)
		}
	}
	for _, kept := range []string{"Members", "Conversations", "Messages", "Database size"} {
		if !strings.Contains(body, kept) {
			t.Errorf("metrics page lost the aggregate %q", kept)
		}
	}
	if _, err = a.store.db.Exec("UPDATE users SET suspended=1,role='member' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if w := owner.request("GET", "/admin/metrics?role=owner", nil, nil); w.Code == 200 || strings.Contains(w.Body.String(), "Instance overview") {
		t.Fatal("suspended owner read metrics", w.Code)
	}
}

// Hostile requests cannot smuggle identifiers into activity either.
func TestActivityIgnoresAdversarialRequests(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	for _, path := range []string{"/topics/1%27%20OR%201=1", "/images/%3Cscript%3E?open=1", "/topics/-1", "/images/99999999999999999999?open=1&width=1"} {
		owner.request("GET", path, nil, nil)
	}
	owner.post("/login", url.Values{"username": {"alex"}, "password": {"wrong password here"}})
	owner.post("/login", url.Values{"username": {"' OR 1=1 --"}, "password": {"a long test password"}})
	if n := activityRows(t, a.store); n != 0 {
		t.Fatalf("adversarial requests stored %d activity rows", n)
	}
}

// Upgrading removes the per-member and per-item rows earlier versions kept,
// and leaves everything else in place.
func TestForgetActivityMigrationRemovesIdentifyingRows(t *testing.T) {
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
	if err := analytics.Initialize(t.Context(), db, time.Now()); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Unix() / 86400
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'member','hash','member',1);
		INSERT INTO topics(id,board_id,author_id,title,created_at,updated_at,audience) VALUES(1,1,1,'Kept conversation',1,1,'members');
		INSERT INTO posts(id,topic_id,author_id,body,created_at) VALUES(1,1,1,'Kept message',1)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{{"login", "1", ""}, {"post", "1", "1"}, {"image", "1", "7"}, {"post", "", "9"}, {"post", "", ""}} {
		if _, err := db.Exec("INSERT INTO comfy_analytics_activity(day,kind,member,item,count,last_at) VALUES(?,?,?,?,1,?)", today, row[0], row[1], row[2], time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, s)
	var identifying, anonymous int
	if err := s.db.QueryRow("SELECT count(*) FILTER (WHERE member<>'' OR item<>''), count(*) FILTER (WHERE member='' AND item='') FROM comfy_analytics_activity").Scan(&identifying, &anonymous); err != nil {
		t.Fatal(err)
	}
	if identifying != 0 || anonymous != 1 {
		t.Fatalf("after migration: %d identifying rows, %d anonymous rows; want 0 and 1", identifying, anonymous)
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

// A brand-new database migrates even though the shared activity table did
// not exist when the schema began.
func TestForgetActivityMigrationOnAFreshDatabase(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, s)
	if n := activityRows(t, s); n != 0 {
		t.Fatalf("fresh database has %d activity rows", n)
	}
}
