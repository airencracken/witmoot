package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchivePreservesAccessAndBlocksWrites(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, context.Background()
	b, err := s.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	ownerPost, _, err := s.Reply(ctx, f.topicID, f.owner.ID, "An owner message")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeBoard(ctx, f.owner.ID, b.ID, b.Revision, "archive", ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		user *User
		read bool
	}{
		{"owner", f.owner, true}, {"writer", f.writer, true}, {"reader", f.reader, true},
		{"outsider", f.outsider, false}, {"guest", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sessionClient(t, f.app, tc.user)
			for _, path := range []string{"/", "/recent", "/archive", "/search?q=Secret"} {
				w := c.request("GET", path, nil, nil)
				requireStatus(t, w, 200)
				want := tc.read && (path == "/archive" || strings.HasPrefix(path, "/search"))
				if strings.Contains(w.Body.String(), b.Name) != want {
					t.Fatalf("archived board visibility at %s: %s", path, w.Body.String())
				}
			}
			for _, path := range []string{fmt.Sprintf("/boards/%d", b.ID), fmt.Sprintf("/topics/%d", f.topicID)} {
				want := 404
				if tc.read {
					want = 200
				}
				w := c.request("GET", path, nil, nil)
				requireStatus(t, w, want)
				if tc.read && !strings.Contains(w.Body.String(), "Archived board.") {
					t.Fatal("missing archive notice")
				}
				for _, unwanted := range []string{"Start a conversation</a>", "Post reply</button>", "Edit message", "Sign in to reply"} {
					if strings.Contains(w.Body.String(), unwanted) {
						t.Fatalf("archived board offered %s", unwanted)
					}
				}
			}
			_, err := s.Attachment(ctx, f.imageID, tc.user)
			if tc.read && err != nil || !tc.read && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("archived image access: %v", err)
			}
		})
	}
	for _, author := range []struct {
		user *User
		post int64
	}{{f.owner, ownerPost}, {f.writer, f.postID}} {
		c := sessionClient(t, f.app, author.user)
		for _, path := range []string{fmt.Sprintf("/boards/%d/new", b.ID), fmt.Sprintf("/posts/%d/edit", author.post)} {
			requireStatus(t, c.request("GET", path, nil, nil), 403)
		}
		for _, path := range []string{fmt.Sprintf("/boards/%d/new", b.ID), fmt.Sprintf("/topics/%d/replies", f.topicID), fmt.Sprintf("/posts/%d/edit", author.post)} {
			requireStatus(t, c.post(path, url.Values{"title": {"Stale draft"}, "body": {"A stale form"}, "revision": {"0"}, "audience": {"members"}}), 403)
		}
		if _, err := s.CreateTopic(ctx, b.ID, author.user.ID, "Blocked topic", "Message", AudienceMembers); !errors.Is(err, errArchived) {
			t.Fatalf("archive allowed new topic: %v", err)
		}
		if _, _, err := s.Reply(ctx, f.topicID, author.user.ID, "Blocked reply"); !errors.Is(err, errArchived) {
			t.Fatalf("archive allowed reply: %v", err)
		}
		if _, err := s.EditPost(ctx, author.post, author.user.ID, "Blocked edit", 0); !errors.Is(err, errArchived) {
			t.Fatalf("archive allowed edit: %v", err)
		}
	}
	if _, err := s.SaveBoard(ctx, f.owner.ID, b, nil, nil); !errors.Is(err, errBoardConflict) {
		t.Fatalf("stale settings survived archiving: %v", err)
	}
	if err := s.ChangeBoard(ctx, f.owner.ID, b.ID, 1, "restore", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EditPost(ctx, f.postID, f.writer.ID, "Back in business", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Reply(ctx, f.topicID, f.reader.ID, "Still read only"); !errors.Is(err, errReadOnly) {
		t.Fatalf("restoring changed member permissions: %v", err)
	}
	if _, err := s.Board(ctx, b.ID, f.outsider); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("restoring exposed board: %v", err)
	}
	contents, err := s.BoardContents(ctx, b.ID)
	if err != nil || contents != (BoardContents{Topics: 1, Posts: 2, Images: 1}) {
		t.Fatalf("archive lost content: %+v %v", contents, err)
	}
}

func TestBoardActionsRequireOwnerConfirmationAndFreshRevision(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, context.Background()
	owner := sessionClient(t, f.app, f.owner)
	base := fmt.Sprintf("/boards/%d", f.boardID)
	form := url.Values{"revision": {"0"}, "confirmation": {"Hidden planning room"}}
	for _, action := range []string{"archive", "restore", "delete"} {
		for _, user := range []*User{f.writer, f.reader, f.outsider, nil} {
			c := sessionClient(t, f.app, user)
			want := 403
			if user == nil {
				want = 303
			}
			requireStatus(t, c.request("GET", base+"/"+action, nil, nil), want)
			requireStatus(t, c.post(base+"/"+action, maps.Clone(form)), want)
		}
		if err := s.ChangeBoard(ctx, f.writer.ID, f.boardID, 0, action, form.Get("confirmation")); !errors.Is(err, errOwner) {
			t.Fatalf("member performed %s: %v", action, err)
		}
		// Missing CSRF must fail even with the exact confirmation name.
		requireStatus(t, owner.request("POST", base+"/"+action, maps.Clone(form), nil), 403)
	}
	for _, action := range []string{"archive", "delete"} {
		requireStatus(t, owner.request("GET", base+"/"+action, nil, nil), 200)
	}
	b, err := s.Board(ctx, f.boardID, f.owner)
	if err != nil || b.Archived || b.Revision != 0 {
		t.Fatalf("GET mutated board: %+v %v", b, err)
	}
	for _, revision := range []string{"", "-1", "nope", "9223372036854775808"} {
		bad := maps.Clone(form)
		bad.Set("revision", revision)
		requireStatus(t, owner.post(base+"/delete", bad), 400)
	}
	for _, name := range []string{"", "hidden planning room", " Hidden planning room", "Hidden planning room ", "another board", "<script>bad</script>"} {
		bad := maps.Clone(form)
		bad.Set("confirmation", name)
		requireStatus(t, owner.post(base+"/delete", bad), 422)
	}
	requireStatus(t, owner.post(base+"/archive", maps.Clone(form)), 303)
	requireStatus(t, owner.post(base+"/delete", maps.Clone(form)), 409)
	form.Set("revision", "1")
	requireStatus(t, owner.post(base+"/archive", maps.Clone(form)), 409)
	requireStatus(t, owner.request("GET", base+"/restore", nil, nil), 200)
	requireStatus(t, owner.post(base+"/restore", maps.Clone(form)), 303)
	// A renamed board cannot be deleted using an old confirmation, even if the name matched then.
	b, err = s.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	b.Name = "New name & friends"
	if _, err := s.SaveBoard(ctx, f.owner.ID, b, nil, nil); err != nil {
		t.Fatal(err)
	}
	form.Set("revision", "2")
	requireStatus(t, owner.post(base+"/delete", maps.Clone(form)), 409)
	form.Set("revision", "3")
	requireStatus(t, owner.post(base+"/delete", maps.Clone(form)), 422)
	form.Set("confirmation", b.Name)
	w := owner.post(base+"/delete", maps.Clone(form))
	requireStatus(t, w, 303)
	if w.Header().Get("Location") != "/boards/manage?deleted=1" {
		t.Fatal("missing deletion redirect")
	}
	requireStatus(t, owner.post(base+"/delete", maps.Clone(form)), 404)
	for _, path := range []string{base, base + "/settings", base + "/delete", fmt.Sprintf("/topics/%d", f.topicID), fmt.Sprintf("/posts/%d/edit", f.postID), fmt.Sprintf("/images/%d", f.imageID)} {
		requireStatus(t, owner.request("GET", path, nil, nil), 404)
	}
}

func TestBoardDeletionRollsBackAllContentOnFailure(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, context.Background()
	if err := s.ChangeBoard(ctx, f.owner.ID, f.boardID, 0, "archive", ""); err != nil {
		t.Fatal(err)
	}
	before, err := s.BoardContents(ctx, f.boardID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_board_delete BEFORE DELETE ON boards BEGIN SELECT RAISE(ABORT, 'simulated failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeBoard(ctx, f.owner.ID, f.boardID, 1, "delete", "Hidden planning room"); err == nil {
		t.Fatal("simulated failure ignored")
	}
	after, err := s.BoardContents(ctx, f.boardID)
	if err != nil || before != after {
		t.Fatalf("partial deletion: before=%+v after=%+v err=%v", before, after, err)
	}
	if _, err := s.Attachment(ctx, f.imageID, f.reader); err != nil {
		t.Fatalf("failed deletion lost attachments or permissions: %v", err)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_board_delete"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeBoard(ctx, f.owner.ID, f.boardID, 1, "delete", "Hidden planning room"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"topics", "posts", "attachments", "board_members"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("leftover %s: %d %v", table, count, err)
		}
	}
	boards, err := s.Boards(ctx, f.owner)
	if err != nil || len(boards) != 5 {
		t.Fatalf("unrelated boards removed: %+v %v", boards, err)
	}
}

func TestLifecycleMigrationKeepsDataAndRetiresDeletedURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"001_initial.sql", "002_access_modes.sql", "003_imvault.sql", "004_invitations.sql", "005_board_access_and_edits.sql"} {
		migration, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 5;
		INSERT INTO users(id, username, password_hash, role, created_at) VALUES (1, 'host', 'hash', 'owner', 1);
		INSERT INTO boards(id, category, name, description, position) VALUES (50, 'Old rooms', 'Last summer', 'Memories', 50);
		INSERT INTO topics(id, board_id, author_id, title, created_at, updated_at) VALUES (80, 50, 1, 'Summer memories', 1, 1);
		INSERT INTO posts(id, topic_id, author_id, body, created_at, edited_at, revision) VALUES (90, 80, 1, 'A day at the lake', 1, 2, 1);
		INSERT INTO attachments(id, post_id, server, remote_id, name, rendition) VALUES (100, 90, 'https://images.example', 'lake', 'Lake.png', 'thumb');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, owner := context.Background(), &User{ID: 1, Role: "owner"}
	b, err := s.Board(ctx, 50, owner)
	if err != nil || b.Archived || b.Name != "Last summer" {
		t.Fatalf("migration changed existing board: %+v %v", b, err)
	}
	p, err := readPost(ctx, s.db, 90, owner)
	if err != nil || p.Body != "A day at the lake" || p.EditedAt != 2 || p.Revision != 1 {
		t.Fatalf("migration changed existing post: %+v %v", p, err)
	}
	if err := s.ChangeBoard(ctx, 1, 50, 0, "delete", b.Name); err != nil {
		t.Fatal(err)
	}
	// Reopen to verify retired IDs survive process restarts.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	boardID, err := s.SaveBoard(ctx, 1, Board{Name: "Next summer", Category: "Plans"}, nil, nil)
	if err != nil || boardID <= 50 {
		t.Fatalf("reused board URL: %d %v", boardID, err)
	}
	topicID, err := s.CreateTopic(ctx, boardID, 1, "New memories", "Another day at the lake", AudienceMembers, Attachment{Server: "https://images.example", RemoteID: "next", Name: "Next.png", Rendition: "thumb"})
	if err != nil || topicID <= 80 {
		t.Fatalf("reused topic URL: %d %v", topicID, err)
	}
	posts, _, err := s.Posts(ctx, topicID, 20, 0, owner)
	if err != nil || len(posts) != 1 || posts[0].ID <= 90 {
		t.Fatalf("reused post URL: %+v %v", posts, err)
	}
	if err := s.PostImages(ctx, posts); err != nil || len(posts[0].Images) != 1 || posts[0].Images[0].ID <= 100 {
		t.Fatalf("reused image URL: %+v %v", posts, err)
	}
	if err := s.ChangeBoard(ctx, 1, 50, 0, "delete", b.Name); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale deletion targeted a new board: %v", err)
	}
	if _, err := s.Topic(ctx, 80, owner); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retired conversation link resolves: %v", err)
	}
	if _, err := readPost(ctx, s.db, 90, owner); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retired post link resolves: %v", err)
	}
	if _, err := s.Attachment(ctx, 100, owner); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retired attachment link resolves: %v", err)
	}
}

func TestArchiveRouteRespectsSiteModes(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	if err := f.app.store.ChangeBoard(ctx, f.owner.ID, f.boardID, 0, "archive", ""); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{ModeOpen, ModePrivate, ModePersonal} {
		if err := f.app.store.SetMode(ctx, mode); err != nil {
			t.Fatal(err)
		}
		for _, user := range []*User{f.owner, f.reader, nil} {
			c := sessionClient(t, f.app, user)
			want := 200
			if mode != ModeOpen && user == nil {
				want = 303
			} else if mode == ModePersonal && user.Role != "owner" {
				want = 403
			}
			w := c.request("GET", "/archive", nil, nil)
			requireStatus(t, w, want)
			if strings.Contains(w.Body.String(), "Hidden planning room") != (want == 200 && user != nil) {
				t.Fatalf("archive disclosure in %s: %s", mode, w.Body.String())
			}
		}
	}
}
