package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTimezoneValidationAndCalendarBoundaries(t *testing.T) {
	for _, name := range []string{"", "Local", "PST", "../UTC", "/etc/passwd", "America/../UTC", "America/Nowhere", "America/New_York\x00", strings.Repeat("x", 65)} {
		if _, err := parseTimezone(name); !errors.Is(err, errTimezone) {
			t.Errorf("accepted %q: %v", name, err)
		}
	}
	for _, tc := range []struct{ zone, instant, stamp, date string }{
		{"UTC", "2026-01-01T00:15:00Z", "Jan 1, 2026 · 00:15 UTC", "Jan 1, 2026"},
		{"America/Los_Angeles", "2026-01-01T00:15:00Z", "Dec 31, 2025 · 16:15 PST", "Dec 31, 2025"},
		{"America/Los_Angeles", "2026-03-08T09:59:00Z", "Mar 8, 2026 · 01:59 PST", "Mar 8, 2026"},
		{"America/Los_Angeles", "2026-03-08T10:00:00Z", "Mar 8, 2026 · 03:00 PDT", "Mar 8, 2026"},
		{"Asia/Kathmandu", "2026-01-01T00:15:00Z", "Jan 1, 2026 · 06:00 +0545", "Jan 1, 2026"},
	} {
		instant, err := time.Parse(time.RFC3339, tc.instant)
		if err != nil {
			t.Fatal(err)
		}
		p := Page{timezone: displayTimezone(tc.zone)}
		if got := p.Stamp(instant.Unix()); got != tc.stamp {
			t.Errorf("%s: %s", tc.zone, got)
		}
		if got := p.Date(instant.Unix()); got != tc.date {
			t.Errorf("%s: %s", tc.zone, got)
		}
	}
	if displayTimezone("invalid") != time.UTC {
		t.Fatal("invalid stored zone did not fall back to UTC")
	}
	for _, zone := range timezoneSuggestions {
		loc, err := parseTimezone(zone)
		if err != nil {
			t.Fatal(zone, err)
		}
		// Across seasons and date boundaries, both renderers use the same local calendar.
		for day := 0; day < 365; day++ {
			instant := time.Date(2026, 1, 1+day, 0, 15, 0, 0, time.UTC)
			p := Page{timezone: loc}
			if !strings.HasPrefix(p.Stamp(instant.Unix()), p.Date(instant.Unix())+" · ") {
				t.Fatalf("date/stamp disagree: %s %s", zone, instant)
			}
		}
	}
}

func TestTimezoneAccountRoutePersistsAndIsIsolated(t *testing.T) {
	app, client := newTestApp(t, false)
	id := signInTest(t, app, client, true)
	other := testMember(t, app.store, "sam")
	requireStatus(t, client.request("GET", "/account", nil, nil), 200)
	user, err := app.store.UserByID(context.Background(), id)
	if err != nil || user.Timezone != "UTC" {
		t.Fatalf("default timezone: %+v %v", user, err)
	}
	requireStatus(t, client.request("POST", "/account/timezone", url.Values{"timezone": {"Asia/Kolkata"}}, nil), 403)
	w := client.post("/account/timezone", url.Values{"timezone": {" Asia/Kolkata "}, "user_id": {strconv.FormatInt(other, 10)}})
	requireStatus(t, w, 303)
	if w.Header().Get("Location") != "/account?saved=timezone" {
		t.Fatal(w.Header())
	}
	page := client.request("GET", "/account", nil, nil)
	if !regexp.MustCompile(`name="timezone"[^>]*value="Asia/Kolkata"`).MatchString(page.Body.String()) {
		t.Fatal("preference missing after session reload")
	}
	user, _, err = app.store.Credentials(context.Background(), "alex")
	if err != nil || user.Timezone != "Asia/Kolkata" {
		t.Fatalf("credential query: %+v %v", user, err)
	}
	untouched, err := app.store.UserByID(context.Background(), other)
	if err != nil || untouched.Timezone != "UTC" {
		t.Fatal("changed another member's timezone")
	}
	for _, name := range []string{"../UTC", "Local", "<script>alert(1)</script>"} {
		bad := client.post("/account/timezone", url.Values{"timezone": {name}})
		requireStatus(t, bad, 422)
		if strings.Contains(bad.Body.String(), "<script>alert(1)</script>") {
			t.Fatal("unescaped invalid zone")
		}
		user, _ = app.store.UserByID(context.Background(), id)
		if user.Timezone != "Asia/Kolkata" {
			t.Fatal("invalid input changed saved preference")
		}
	}
	anonymous := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	anonymous.request("GET", "/login", nil, nil)
	requireStatus(t, anonymous.post("/account/timezone", url.Values{"timezone": {"UTC"}}), 303)
}

func TestTimezoneWritesAreAtomicAndRejectInactiveAccounts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := testMember(t, s, "alex")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_timezone BEFORE UPDATE OF timezone ON users BEGIN SELECT RAISE(ABORT, 'failed write'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTimezone(ctx, id, "Asia/Tokyo"); err == nil {
		t.Fatal("write failure ignored")
	}
	u, err := s.UserByID(ctx, id)
	if err != nil || u.Timezone != "UTC" {
		t.Fatal("failed write was not atomic", err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_timezone`); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"suspended", "deleted"} {
		if _, err := s.db.Exec("UPDATE users SET "+field+"=1, suspended=1, password_hash='', can_invite=0 WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		if err := s.SetTimezone(ctx, id, "Asia/Tokyo"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("changed %s account: %v", field, err)
		}
		if _, err := s.db.Exec("UPDATE users SET "+field+"=0 WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"", strings.Repeat("x", 65)} {
		if _, err := s.db.Exec("UPDATE users SET timezone=? WHERE id=?", value, id); err == nil {
			t.Fatal("schema accepted invalid zone length")
		}
	}
}

func TestTimezoneRendersDatesWithoutJavaScript(t *testing.T) {
	app, client := newTestApp(t, false)
	id := signInTest(t, app, client, true)
	ctx := context.Background()
	topic, err := app.store.CreateTopic(ctx, 1, id, "New year", "Happy new year", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 1, 1, 0, 15, 0, 0, time.UTC).Unix()
	for _, table := range []string{"topics", "posts"} {
		if _, err := app.store.db.Exec("UPDATE "+table+" SET created_at=?", instant); err != nil {
			t.Fatal(err)
		}
	}
	requireStatus(t, client.post("/account/timezone", url.Values{"timezone": {"America/Los_Angeles"}}), 303)
	page := client.request("GET", "/topics/"+strconv.FormatInt(topic, 10), nil, nil)
	requireStatus(t, page, 200)
	body := page.Body.String()
	if !strings.Contains(body, "Dec 31, 2025 · 16:15 PST") || !strings.Contains(body, `datetime="2026-01-01T00:15:00Z"`) || strings.Contains(body, "data-local-time") {
		t.Fatal("server did not render saved zone with stable UTC datetime")
	}
	requireStatus(t, client.post("/account/timezone", url.Values{"timezone": {"UTC"}}), 303)
	if !strings.Contains(client.request("GET", "/topics/"+strconv.FormatInt(topic, 10), nil, nil).Body.String(), "Jan 1, 2026 · 00:15 UTC") {
		t.Fatal("UTC reset failed")
	}
}

func TestTimezoneMigrationBackfillsExistingAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "witmoot.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range migrationFiles[:15] {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.Exec(string(body) + fmt.Sprintf("; PRAGMA user_version=%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Exec(`INSERT INTO users(username,password_hash,role,created_at) VALUES('alex','hash','owner',123)`); err != nil {
		t.Fatal(err)
	}
	closeTest(t, old)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, s)
	u, _, err := s.Credentials(t.Context(), "alex")
	if err != nil || u.Timezone != "UTC" || u.CreatedAt != 123 {
		t.Fatal("migration changed account", u, err)
	}
	if err := s.SetTimezone(t.Context(), u.ID, "Asia/Tokyo"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	u, err = s.UserByID(t.Context(), u.ID)
	if err != nil || u.Timezone != "Asia/Tokyo" {
		t.Fatal("repeated migration erased preference", err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "witmoot-before-v15-*", "witmoot.db"))
	if err != nil || len(backups) != 1 {
		t.Fatal("missing pre-timezone snapshot", backups, err)
	}
}

func TestTimezoneExportRetainsPreferenceAndUTCData(t *testing.T) {
	app, client := newTestApp(t, false)
	id := signInTest(t, app, client, true)
	if _, err := app.store.CreateTopic(t.Context(), 1, id, "New year", "Happy new year", AudienceMembers); err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 1, 1, 0, 15, 0, 0, time.UTC).Unix()
	if _, err := app.store.db.Exec("UPDATE posts SET created_at=?", instant); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, client.post("/account/timezone", url.Values{"timezone": {"America/Los_Angeles"}}), 303)
	entries, _, _ := readExport(t, client)
	manifest := decodeManifest(t, entries)
	if manifest.Account.Timezone != "America/Los_Angeles" || manifest.Posts[0].CreatedAt != "2026-01-01T00:15:00Z" {
		t.Fatal("export lost timezone or changed UTC data")
	}
	if !strings.Contains(string(entries["archive.html"]), "Dec 31, 2025 · 16:15 PST") {
		t.Fatal("offline archive ignored display timezone")
	}
}
