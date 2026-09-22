package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type accessFixture struct {
	app                               *App
	owner, writer, reader, outsider   *User
	boardID, topicID, postID, imageID int64
}

func privateBoardFixture(t *testing.T) accessFixture {
	t.Helper()
	ctx := context.Background()
	app, _ := newTestApp(t, false)
	s := app.store
	if err := s.SetMode(ctx, ModeOpen); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOwner(ctx, "host", "unused-hash"); err != nil {
		t.Fatal(err)
	}
	owner, _, err := s.Credentials(ctx, "host")
	if err != nil {
		t.Fatal(err)
	}
	f := accessFixture{app: app, owner: &owner}
	f.writer = &User{ID: testMember(t, s, "writer"), Role: "member"}
	f.reader = &User{ID: testMember(t, s, "reader"), Role: "member"}
	f.outsider = &User{ID: testMember(t, s, "outsider"), Role: "member"}
	f.boardID, err = s.SaveBoard(ctx, owner.ID, Board{Name: "Hidden planning room", Category: "Private corners", Description: "A secret description", Restricted: true}, map[int64]string{f.writer.ID: "write", f.reader.ID: "read"})
	if err != nil {
		t.Fatal(err)
	}
	f.topicID, err = s.CreateTopic(ctx, f.boardID, f.writer.ID, "Secret party plans", "Original message", AudienceMembers, Attachment{Server: "https://images.example", RemoteID: "example", Name: "secret.png", Rendition: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT id FROM posts WHERE topic_id = ?", f.topicID).Scan(&f.postID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT id FROM attachments WHERE post_id = ?", f.postID).Scan(&f.imageID); err != nil {
		t.Fatal(err)
	}
	return f
}

func sessionClient(t *testing.T, app *App, user *User) *testClient {
	t.Helper()
	c := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	if user != nil {
		token := randomToken()
		if err := app.store.NewSession(context.Background(), tokenHash(token), user.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		c.cookies[app.cookieName("session")] = &http.Cookie{Name: app.cookieName("session"), Value: token}
	}
	c.request("GET", "/", nil, nil)
	return c
}

func TestPrivateBoardReadAndWriteMatrix(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name           string
		user           *User
		visible, write bool
	}{
		{"owner", f.owner, true, true}, {"writer", f.writer, true, true},
		{"reader", f.reader, true, false}, {"outsider", f.outsider, false, false}, {"guest", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sessionClient(t, f.app, tc.user)
			for _, path := range []string{"/", "/recent", "/search?q=Secret"} {
				w := c.request("GET", path, nil, nil)
				requireStatus(t, w, 200)
				needle := "Secret party plans"
				if path == "/" {
					needle = "Hidden planning room"
				}
				if strings.Contains(w.Body.String(), needle) != tc.visible {
					t.Fatalf("visibility at %s: %s", path, w.Body.String())
				}
			}
			for _, path := range []string{fmt.Sprintf("/boards/%d", f.boardID), fmt.Sprintf("/topics/%d", f.topicID)} {
				want := 404
				if tc.visible {
					want = 200
				}
				w := c.request("GET", path, nil, nil)
				requireStatus(t, w, want)
				if tc.user != nil && !tc.write && strings.Contains(w.Body.String(), "Start a conversation</a>") {
					t.Fatal("write link shown to read-only member")
				}
				if tc.user != nil && !tc.write && strings.Contains(w.Body.String(), "Post reply</button>") {
					t.Fatal("reply form shown without write permission")
				}
			}
			_, err := f.app.store.Attachment(ctx, f.imageID, tc.user)
			if tc.visible && err != nil || !tc.visible && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("attachment access: %v", err)
			}
			stats, err := f.app.store.Stats(ctx, tc.user)
			if err != nil {
				t.Fatal(err)
			}
			if (stats.Topics > 0) != tc.visible || (stats.Posts > 0) != tc.visible {
				t.Fatalf("counts disclose hidden content: %+v", stats)
			}
			if tc.user == nil {
				return
			}
			want := 404
			if tc.visible {
				want = 403
			}
			if tc.write {
				want = 303
			}
			requireStatus(t, c.post(fmt.Sprintf("/boards/%d/new", f.boardID), url.Values{"title": {"More secret plans"}, "body": {"New message"}, "audience": {"members"}}), want)
			requireStatus(t, c.post(fmt.Sprintf("/topics/%d/replies", f.topicID), url.Values{"body": {"A reply"}}), want)
		})
	}
}

func TestRevokedBoardAccessBlocksStaleFormsAndImages(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	c := sessionClient(t, f.app, f.writer)
	edit := fmt.Sprintf("/posts/%d/edit", f.postID)
	requireStatus(t, c.request("GET", edit, nil, nil), 200)
	b, err := f.app.store.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"read", "none"} {
		if _, err := f.app.store.SaveBoard(ctx, f.owner.ID, b, map[int64]string{f.writer.ID: level}); err != nil {
			t.Fatal(err)
		}
		b.Revision++
		want := 403
		if level == "none" {
			want = 404
		}
		requireStatus(t, c.post(edit, url.Values{"body": {"Stale edit"}, "revision": {"0"}}), want)
		requireStatus(t, c.post(fmt.Sprintf("/topics/%d/replies", f.topicID), url.Values{"body": {"Stale reply"}}), want)
		requireStatus(t, c.post(fmt.Sprintf("/boards/%d/new", f.boardID), url.Values{"title": {"Stale topic"}, "body": {"Stale message"}, "audience": {"members"}}), want)
		if _, _, err := f.app.store.Reply(ctx, f.topicID, f.writer.ID, "Direct store write"); err == nil {
			t.Fatal("store accepted revoked permission")
		}
		if _, err := f.app.store.EditPost(ctx, f.postID, f.writer.ID, "Direct store edit", 0); err == nil {
			t.Fatal("store edit accepted revoked permission")
		}
	}
	if _, err := f.app.store.Attachment(ctx, f.imageID, f.writer); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked image access: %v", err)
	}
	p, err := readPost(ctx, f.app.store.db, f.postID, f.owner)
	if err != nil || p.Body != "Original message" || p.Revision != 0 {
		t.Fatalf("stale form changed post: %+v, %v", p, err)
	}
	stats, err := f.app.store.Stats(ctx, f.owner)
	if err != nil || stats.Posts != 1 || stats.Topics != 1 {
		t.Fatalf("stale form left content: %+v %v", stats, err)
	}
}

func TestBoardSettingsAreOwnerOnlyAtomicAndConflictChecked(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	c := sessionClient(t, f.app, f.writer)
	path := fmt.Sprintf("/boards/%d/settings", f.boardID)
	for _, route := range []string{"/boards/manage", "/boards/new", path} {
		requireStatus(t, c.request("GET", route, nil, nil), 403)
	}
	requireStatus(t, c.post(path, url.Values{"name": {"Stolen"}}), 403)
	b, err := f.app.store.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.store.SaveBoard(ctx, f.writer.ID, b, nil); !errors.Is(err, errOwner) {
		t.Fatalf("member saved board: %v", err)
	}
	changed := b
	changed.Name, changed.Restricted = "Leaked name", false
	for _, access := range []map[int64]string{{f.writer.ID: "admin"}, {99999: "write"}, {f.owner.ID: "none"}} {
		if _, err := f.app.store.SaveBoard(ctx, f.owner.ID, changed, access); !errors.Is(err, errBoardAccess) {
			t.Fatalf("bad permissions accepted: %v", err)
		}
		actual, err := f.app.store.Board(ctx, b.ID, f.writer)
		if err != nil || actual.Name != b.Name || !actual.Restricted || actual.Access != "write" || actual.Revision != 0 {
			t.Fatalf("partial board update: %+v %v", actual, err)
		}
	}
	if _, err := f.app.store.SaveBoard(ctx, f.owner.ID, b, map[int64]string{f.writer.ID: "read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.store.SaveBoard(ctx, f.owner.ID, b, map[int64]string{f.writer.ID: "write"}); !errors.Is(err, errBoardConflict) {
		t.Fatalf("stale settings accepted: %v", err)
	}
	actual, err := f.app.store.Board(ctx, b.ID, f.writer)
	if err != nil || actual.Access != "read" {
		t.Fatalf("stale settings changed grant: %+v %v", actual, err)
	}
	owner := sessionClient(t, f.app, f.owner)
	requireStatus(t, owner.request("POST", path, url.Values{"name": {"Forged"}}, nil), 403)
	form := url.Values{"name": {"New private room"}, "category": {"Friends"}, "description": {"Keep it small"}, "visibility": {"selected"}, "revision": {"0"}, fmt.Sprintf("access_%d", f.reader.ID): {"read"}}
	w := owner.post("/boards/new", form)
	requireStatus(t, w, 303)
	if !strings.Contains(w.Header().Get("Location"), "/settings?saved=1") {
		t.Fatal("new board did not reach settings")
	}
	requireStatus(t, owner.request("GET", w.Header().Get("Location"), nil, nil), 200)
}

func TestRestrictingExistingBoardFiltersEveryReadAndOldAudience(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	id, err := f.app.store.CreateTopic(ctx, 1, f.writer.ID, "Once public", "Public before restriction", AudiencePublic)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.app.store.Board(ctx, 1, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	b.Restricted = true
	if _, err := f.app.store.SaveBoard(ctx, f.owner.ID, b, map[int64]string{f.writer.ID: "write"}); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []*User{nil, f.reader, f.outsider} {
		if _, err := f.app.store.Topic(ctx, id, reader); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("old public topic leaks: %v", err)
		}
		boards, err := f.app.store.Boards(ctx, reader)
		if err != nil {
			t.Fatal(err)
		}
		for _, board := range boards {
			if board.ID == 1 {
				t.Fatal("restricted board listed")
			}
		}
	}
	if _, err := f.app.store.CreateTopic(ctx, 1, f.writer.ID, "Stale public form", "Must review audience", AudiencePublic); !errors.Is(err, errAudienceChanged) {
		t.Fatalf("stale audience accepted: %v", err)
	}
	if err := f.app.store.SetMode(ctx, ModePersonal); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.app.store.Reply(ctx, f.topicID, f.writer.ID, "Personal bypass"); !errors.Is(err, errPersonal) {
		t.Fatalf("board grant bypasses site mode: %v", err)
	}
}
