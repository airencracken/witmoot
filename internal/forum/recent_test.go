package forum

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func recentTopic(t *testing.T, s *Store, board, author int64, title, body string, audience Audience) int64 {
	t.Helper()
	id, err := s.CreateTopic(t.Context(), board, author, title, body, audience)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func recentReply(t *testing.T, s *Store, topic, author int64, body string) int64 {
	t.Helper()
	id, _, err := s.Reply(t.Context(), topic, author, body)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRecentPostsOrderPaginationAndPermalinks(t *testing.T) {
	app, _ := newTestApp(t, false)
	s, ctx := app.store, t.Context()
	id := testMember(t, s, "reader")
	user := &User{ID: id, Role: "member"}
	topic := recentTopic(t, s, 1, id, "A busy conversation", "Opening post", AudienceMembers)
	for i := range 43 {
		recentReply(t, s, topic, id, fmt.Sprintf("Reply %02d", i))
	}
	other := recentTopic(t, s, 2, id, "Another conversation", "A different board", AudienceMembers)
	// Ties and timestamps that do not follow ID order exercise both sort keys.
	if _, err := s.db.Exec("UPDATE posts SET created_at = (id % 3) * 1000"); err != nil {
		t.Fatal(err)
	}
	var expected []Post
	for _, topicID := range []int64{topic, other} {
		posts, _, err := s.Posts(ctx, topicID, 100, 0, user)
		if err != nil {
			t.Fatal(err)
		}
		expected = append(expected, posts...)
	}
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].CreatedAt == expected[j].CreatedAt {
			return expected[i].ID > expected[j].ID
		}
		return expected[i].CreatedAt > expected[j].CreatedAt
	})
	for _, size := range []int{1, 2, 7, 20, 45, 46} {
		var got []int64
		for offset := 0; offset <= len(expected); offset += size {
			posts, more, err := s.RecentPosts(ctx, size, offset, user)
			if err != nil || len(posts) != min(size, len(expected)-offset) || more != (offset+size < len(expected)) {
				t.Fatalf("size %d offset %d: posts=%d more=%v err=%v", size, offset, len(posts), more, err)
			}
			for i, p := range posts {
				want := expected[offset+i]
				if p.ID != want.ID || p.Number != want.Number || p.URL() != want.URL() {
					t.Fatalf("message or permalink mismatch: %+v want %+v", p, want)
				}
				if p.Topic.ID != p.TopicID || p.Topic.BoardID == 0 || p.Topic.Title == "" || p.Topic.BoardName == "" {
					t.Fatalf("missing conversation context: %+v", p)
				}
				got = append(got, p.ID)
			}
		}
		if len(got) != len(expected) {
			t.Fatalf("pagination lost posts: got %d want %d", len(got), len(expected))
		}
	}
	client := sessionClient(t, app, user)
	for _, p := range expected {
		target, err := url.Parse(p.URL())
		if err != nil {
			t.Fatal(err)
		}
		page := client.request("GET", target.RequestURI(), nil, nil)
		requireStatus(t, page, 200)
		if !strings.Contains(page.Body.String(), fmt.Sprintf(`id="post-%d"`, p.ID)) || !strings.Contains(page.Body.String(), p.Body) {
			t.Fatalf("permalink did not reach message %d", p.ID)
		}
	}
	// Editing updates text but never bumps a message into a different position.
	before, _, _ := s.RecentPosts(ctx, 100, 0, user)
	if _, err := s.EditPost(ctx, expected[len(expected)-1].ID, id, "An edited message", 0); err != nil {
		t.Fatal(err)
	}
	after, _, err := s.RecentPosts(ctx, 100, 0, user)
	if err != nil || len(before) != len(after) {
		t.Fatal(err)
	}
	for i := range before {
		if before[i].ID != after[i].ID {
			t.Fatal("editing reordered the feed")
		}
	}
}

func TestRecentPostsAccessIsFilteredBeforePagination(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, t.Context()
	public := recentTopic(t, s, 1, f.writer.ID, "Public conversation", "public message", AudiencePublic)
	if err := s.SetMode(ctx, ModePrivate); err != nil {
		t.Fatal(err)
	}
	members := recentTopic(t, s, 1, f.writer.ID, "Members conversation", "members message", AudienceMembers)
	if err := s.SetMode(ctx, ModePersonal); err != nil {
		t.Fatal(err)
	}
	owners := recentTopic(t, s, 1, f.owner.ID, "Owners conversation", "owners message", AudienceOwners)
	if err := s.SetMode(ctx, ModeOpen); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		recentReply(t, s, f.topicID, f.writer.ID, "A hidden-board reply")
	}
	quiet := &User{ID: testMember(t, s, "quiet"), Role: "member"}
	group := createTestGroup(t, f, "Feed readers", quiet.ID, f.outsider.ID)
	setTestGrants(t, f, map[int64]string{f.writer.ID: "write", f.reader.ID: "read", f.outsider.ID: "none"}, map[int64]string{group.ID: "read"})
	for _, tc := range []struct {
		name   string
		user   *User
		topics []int64
	}{
		{"owner", f.owner, []int64{public, members, owners, f.topicID}},
		{"writer", f.writer, []int64{public, members, f.topicID}},
		{"reader", f.reader, []int64{public, members, f.topicID}},
		{"group reader", quiet, []int64{public, members, f.topicID}},
		{"individual denial", f.outsider, []int64{public, members}},
		{"guest", nil, []int64{public}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantCount := 0
			for _, id := range tc.topics {
				if err := s.db.QueryRow("SELECT ? + count(*) FROM posts WHERE topic_id = ?", wantCount, id).Scan(&wantCount); err != nil {
					t.Fatal(err)
				}
			}
			client := sessionClient(t, f.app, tc.user)
			for offset := 0; offset <= wantCount; offset++ {
				posts, more, err := s.RecentPosts(ctx, 1, offset, tc.user)
				if err != nil || len(posts) != min(1, wantCount-offset) || more != (offset+1 < wantCount) {
					t.Fatalf("hidden messages affected pagination at %d: posts=%v more=%v err=%v", offset, posts, more, err)
				}
				for _, p := range posts {
					if !slices.Contains(tc.topics, p.TopicID) {
						t.Fatalf("hidden message disclosed: %+v", p)
					}
				}
			}
			response := client.request("GET", "/recent", nil, nil)
			requireStatus(t, response, 200)
			body := response.Body.String()
			for topic, text := range map[int64]string{public: "public message", members: "members message", owners: "owners message", f.topicID: "Original message"} {
				if strings.Contains(body, text) != slices.Contains(tc.topics, topic) {
					t.Fatalf("HTTP feed disagrees with permissions for %q", text)
				}
			}
		})
	}
	// Permissions are evaluated again on every request, including stale readers.
	setTestGrants(t, f, map[int64]string{f.writer.ID: "write"}, nil)
	posts, _, err := s.RecentPosts(ctx, 100, 0, quiet)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range posts {
		if p.TopicID == f.topicID {
			t.Fatal("revoked group still appears in the feed")
		}
	}
	if _, err := s.db.Exec("UPDATE users SET suspended=1 WHERE id=?", quiet.ID); err != nil {
		t.Fatal(err)
	}
	if posts, more, err := s.RecentPosts(ctx, 1, 0, quiet); err != nil || len(posts) != 0 || more {
		t.Fatalf("stale suspended reader: %+v %v %v", posts, more, err)
	}
}

func TestRecentPostsRemovalArchivesAndAtomicWrites(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, t.Context()
	// A failed topic/attachment write must not leave a feed item behind.
	before, _, err := s.RecentPosts(ctx, 100, 0, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTopic(ctx, f.boardID, f.writer.ID, "Must roll back", "partial message", AudienceMembers, Attachment{CredentialUserID: sql.NullInt64{Int64: 999999, Valid: true}}); err == nil {
		t.Fatal("invalid attachment accepted")
	}
	after, _, err := s.RecentPosts(ctx, 100, 0, f.owner)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed transaction changed the feed", err)
	}
	for range 21 {
		recentReply(t, s, f.topicID, f.writer.ID, "Another reply")
	}
	if _, err := s.RemovePost(ctx, f.owner.ID, f.postID, 0, false); err != nil {
		t.Fatal(err)
	}
	posts, more, err := s.RecentPosts(ctx, 100, 0, f.owner)
	if err != nil || len(posts) != 21 || more {
		t.Fatalf("removed post counted: %+v %v %v", posts, more, err)
	}
	for _, p := range posts {
		if p.ID == f.postID || p.Number < 2 || p.Removed {
			t.Fatal("removed post surfaced or changed permalink numbering")
		}
	}
	board, err := s.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeBoard(ctx, f.owner.ID, board.ID, board.Revision, "archive", ""); err != nil {
		t.Fatal(err)
	}
	if posts, more, err := s.RecentPosts(ctx, 1, 0, f.owner); err != nil || len(posts) != 0 || more {
		t.Fatalf("archived posts surfaced: %+v %v %v", posts, more, err)
	}
	if err := s.ChangeBoard(ctx, f.owner.ID, board.ID, board.Revision+1, "restore", ""); err != nil {
		t.Fatal(err)
	}
	if posts, more, err := s.RecentPosts(ctx, 20, 0, f.owner); err != nil || len(posts) != 20 || !more {
		t.Fatalf("restored posts missing: %+v %v %v", posts, more, err)
	}
}

func TestRecentPostsHTTPRenderingPaginationAndAdversarialInputs(t *testing.T) {
	app, _ := newTestApp(t, false)
	s, ctx := app.store, t.Context()
	id := testMember(t, s, "author")
	user := &User{ID: id, Role: "member"}
	topic := recentTopic(t, s, 1, id, `A <script>conversation</script>`, `<img src=x onerror=alert(1)>`, AudienceMembers)
	for i := range 20 {
		recentReply(t, s, topic, id, fmt.Sprintf("Message %d https://music.example/recommendations/42", i))
	}
	if err := s.SaveAvatar(ctx, id, pngAvatarFixture(t, 32, 32)); err != nil {
		t.Fatal(err)
	}
	c := sessionClient(t, app, user)
	for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
		first := c.request("GET", "/recent", nil, headers)
		requireStatus(t, first, 200)
		if !strings.HasPrefix(first.Header().Get("Content-Type"), "text/html") {
			t.Fatal("incorrect content type")
		}
		body := first.Body.String()
		for _, text := range []string{"<h1>Recent posts</h1>", "Message 19", "Songstead recommendation", fmt.Sprintf(`/avatars/%d`, id), `href="/recent?page=2"`, `aria-current="page">Recent`} {
			if !strings.Contains(body, text) {
				t.Fatalf("missing feed content %q", text)
			}
		}
		if !strings.Contains(body, `hx-boost="false">Read in conversation</a>`) {
			t.Fatal("message permalinks must use native fragment navigation")
		}
		if strings.Count(body, `class="paper recent-post"`) != pageSize {
			t.Fatal("feed does not contain exactly one page of messages")
		}
		second := c.request("GET", "/recent?page=2", nil, headers)
		requireStatus(t, second, 200)
		body = second.Body.String()
		if strings.Count(body, `class="paper recent-post"`) != 1 || strings.Contains(body, `href="/recent?page=3"`) || !strings.Contains(body, `href="/recent?page=1"`) {
			t.Fatal("wrong last-page pagination")
		}
		if strings.Contains(body, "<script>conversation") || strings.Contains(body, "<img src=x") || !strings.Contains(body, "&lt;img") || !strings.Contains(body, "&lt;script&gt;") {
			t.Fatal("unescaped feed content")
		}
	}
	for _, value := range []string{"0", "-1", "100001", "999999999999999999999", "1.2", "' OR 1=1 --", "<script>", "\x00"} {
		response := c.request("GET", "/recent?page="+url.QueryEscape(value), nil, nil)
		requireStatus(t, response, http.StatusBadRequest)
		if strings.Contains(response.Body.String(), "Message 19") {
			t.Fatal("invalid page returned feed content")
		}
	}
	requireStatus(t, c.request("GET", "/recent?page=100000", nil, nil), 200)
	requireStatus(t, c.post("/recent", nil), http.StatusMethodNotAllowed)
	if _, err := s.db.Exec("DELETE FROM posts; DELETE FROM topics"); err != nil {
		t.Fatal(err)
	}
	empty := c.request("GET", "/recent", nil, nil)
	requireStatus(t, empty, 200)
	if !strings.Contains(empty.Body.String(), "No posts yet.") || strings.Contains(empty.Body.String(), `class="pagination"`) {
		t.Fatal("empty feed is not bounded")
	}
}

func TestRecentPostsSharedImagesAndReferencesRemainLocal(t *testing.T) {
	app, client, owner, vault := imageTestApp(t)
	ctx := t.Context()
	topic, err := app.store.CreateTopic(ctx, 1, owner, "Shared pictures", "https://music.example/recommendations/42 https://vault.example/a/summer", AudienceMembers,
		Attachment{Server: "https://vault.example", RemoteID: "public", Name: "Family <photo>.png", Rendition: "thumb"})
	if err != nil {
		t.Fatal(err)
	}
	var imageID, postID int64
	if err := app.store.db.QueryRow("SELECT a.id,a.post_id FROM attachments a JOIN posts p ON p.id=a.post_id WHERE p.topic_id=?", topic).Scan(&imageID, &postID); err != nil {
		t.Fatal(err)
	}
	requestsBeforeFeed := vault.requests
	response := client.request("GET", "/recent", nil, nil)
	requireStatus(t, response, 200)
	body := response.Body.String()
	for _, text := range []string{fmt.Sprintf(`src="/images/%d"`, imageID), "Family &lt;photo&gt;.png", "Songstead recommendation", "imvault album", "View current public album preview"} {
		if !strings.Contains(body, text) {
			t.Fatalf("feed lost shared content: %s", text)
		}
	}
	if vault.requests != requestsBeforeFeed {
		t.Fatal("opening Recent fetched provider content")
	}
	requireStatus(t, client.request("GET", fmt.Sprintf("/images/%d", imageID), nil, nil), 200)
	if vault.requests != requestsBeforeFeed+1 {
		t.Fatal("image was not loaded through the existing proxy")
	}
	if _, err := app.store.RemovePost(ctx, owner, postID, 0, false); err != nil {
		t.Fatal(err)
	}
	response = client.request("GET", "/recent", nil, nil)
	requireStatus(t, response, 200)
	if strings.Contains(response.Body.String(), "Shared pictures") || strings.Contains(response.Body.String(), fmt.Sprintf("/images/%d", imageID)) {
		t.Fatal("removed post retained a feed item or image")
	}
	requireStatus(t, client.request("GET", fmt.Sprintf("/images/%d", imageID), nil, nil), 404)
}

func TestRecentPostsMigrationPreservesMessagesAndCreatesIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "before-recent.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range migrationFiles[:19] {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body) + fmt.Sprintf("; PRAGMA user_version=%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'member','hash','member',1);
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
	posts, more, err := s.RecentPosts(context.Background(), 20, 0, &User{ID: 1, Role: "member"})
	if err != nil || more || len(posts) != 1 || posts[0].Body != "Kept message" {
		t.Fatalf("migration changed messages: %+v %v %v", posts, more, err)
	}
	var definition string
	if err := s.db.QueryRow("SELECT sql FROM sqlite_master WHERE type='index' AND name='posts_recent'").Scan(&definition); err != nil || !strings.Contains(definition, "created_at DESC, id DESC") || !strings.Contains(definition, "WHERE removed = 0") {
		t.Fatalf("missing feed index: %s %v", definition, err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 20 {
		t.Fatalf("schema=%d err=%v", version, err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "witmoot-before-v19-*", "witmoot.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("missing pre-migration backup: %v %v", backups, err)
	}
}
