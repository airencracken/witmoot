package forum

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// readExport downloads the signed-in member's archive and unpacks it.
func readExport(t *testing.T, client *testClient) (map[string][]byte, []byte, *httptest.ResponseRecorder) {
	t.Helper()
	w := client.request("GET", "/account/export", nil, nil)
	requireStatus(t, w, 200)
	raw := w.Body.Bytes()
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("the export is not a readable zip: %v", err)
	}
	entries := map[string][]byte{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		content, err := io.ReadAll(reader)
		closeTest(t, reader)
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		entries[file.Name] = content
	}
	return entries, raw, w
}

// addAttachment records a shared imvault preview against one post, the way an
// upload would, without needing a live imvault.
func addAttachment(t *testing.T, app *App, postID, userID int64, name string) {
	t.Helper()
	if _, err := app.store.db.Exec(`INSERT INTO attachments(id, post_id, server, remote_id, credential_user_id, name, rendition)
		VALUES ((SELECT last_id + 1 FROM object_sequences WHERE kind = 'attachments'), ?, ?, ?, ?, ?, ?)`,
		postID, "https://images.example.org", "remote-1", userID, name, "preview"); err != nil {
		t.Fatal(err)
	}
}

func decodeManifest(t *testing.T, entries map[string][]byte) exportManifest {
	t.Helper()
	raw, ok := entries["manifest.json"]
	if !ok {
		t.Fatalf("no manifest in the archive: %v", entries)
	}
	var manifest exportManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}
	return manifest
}

func TestAccountExportContainsTheMembersPosts(t *testing.T) {
	app, client := newTestApp(t, false)
	userID := signInTest(t, app, client, false)
	ctx := context.Background()

	topicID, err := app.store.CreateTopic(ctx, 1, userID, "Our weekend", "First post <script>alert(1)</script>", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	replyID, _, err := app.store.Reply(ctx, topicID, userID, "A reply")
	if err != nil {
		t.Fatal(err)
	}
	var topicStartID int64
	if err := app.store.db.QueryRow("SELECT id FROM posts WHERE topic_id = ? ORDER BY id LIMIT 1", topicID).Scan(&topicStartID); err != nil {
		t.Fatal(err)
	}
	addAttachment(t, app, replyID, userID, "photo.png")

	entries, raw, w := readExport(t, client)
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type = %q, want application/zip", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "witmoot-alex-") {
		t.Errorf("content disposition = %q", cd)
	}

	manifest := decodeManifest(t, entries)
	if manifest.Format != exportFormat || manifest.Version != exportVersion {
		t.Errorf("format = %q v%d", manifest.Format, manifest.Version)
	}
	if manifest.Account.Username != "alex" || manifest.Account.Role != "member" {
		t.Errorf("account = %+v", manifest.Account)
	}
	if len(manifest.Boards) != 1 || manifest.Boards[0].Name != "The kitchen table" {
		t.Errorf("boards = %+v", manifest.Boards)
	}
	if len(manifest.Topics) != 1 || manifest.Topics[0].Title != "Our weekend" || manifest.Topics[0].Audience != "members" || manifest.Topics[0].Posts != 2 {
		t.Errorf("topics = %+v", manifest.Topics)
	}
	if len(manifest.Posts) != 2 {
		t.Fatalf("posts = %d, want the member's 2 messages", len(manifest.Posts))
	}
	if manifest.Posts[0].ID != topicStartID || manifest.Posts[0].Number != 1 || manifest.Posts[1].Number != 2 {
		t.Errorf("post ordering = %+v", manifest.Posts)
	}
	if got := manifest.Posts[1].Attachments; len(got) != 1 || got[0].Name != "photo.png" || got[0].Rendition != "preview" {
		t.Errorf("attachments = %+v", got)
	}

	// The readable page is present, escapes user text, and needs no server.
	page, ok := entries["archive.html"]
	if !ok {
		t.Fatal("no archive.html in the archive")
	}
	html := string(page)
	if !strings.Contains(html, "Our weekend") || !strings.Contains(html, "A reply") || !strings.Contains(html, "photo.png") {
		t.Error("the archive page is missing exported material")
	}
	if strings.Contains(html, "<script>alert") || !strings.Contains(html, "&lt;script&gt;") {
		t.Error("user text is not escaped in the archive page")
	}
	if bytes.Contains(raw, []byte("test-hash")) {
		t.Error("the archive contains a password hash")
	}
}

func TestAccountExportIsScopedToItsOwner(t *testing.T) {
	app, client := newTestApp(t, false)
	userID := signInTest(t, app, client, false)
	ctx := context.Background()
	topicID, err := app.store.CreateTopic(ctx, 1, userID, "Shared plans", "My message", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	jules := testMember(t, app.store, "jules")
	if _, _, err := app.store.Reply(ctx, topicID, jules, "jules private words"); err != nil {
		t.Fatal(err)
	}

	entries, raw, _ := readExport(t, client)
	manifest := decodeManifest(t, entries)
	if len(manifest.Posts) != 1 || manifest.Posts[0].Body != "My message" {
		t.Fatalf("export should hold only the member's own posts: %+v", manifest.Posts)
	}
	// The other member's words must not appear anywhere in the archive.
	if bytes.Contains(raw, []byte("jules private words")) {
		t.Error("the export includes another member's message")
	}
}

func TestAccountExportNeverCarriesCredentials(t *testing.T) {
	app, client := newTestApp(t, false)
	userID := signInTest(t, app, client, false)
	ctx := context.Background()
	_, hash, err := app.store.Credentials(ctx, "alex")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetEmail(ctx, userID, "alex@example.org"); err != nil {
		t.Fatal(err)
	}
	// A connected image account carries an encrypted API token that must stay put.
	if err := app.store.ConnectImages(ctx, userID, Connection{Server: "https://images.example.org", Username: "alex", Token: []byte("SECRET-TOKEN")}); err != nil {
		t.Fatal(err)
	}

	entries, raw, _ := readExport(t, client)
	manifest := decodeManifest(t, entries)
	if manifest.Account.Email != "alex@example.org" {
		t.Errorf("the member's own email should be exported: %+v", manifest.Account)
	}
	for label, value := range map[string]string{"password hash": hash, "image token": "SECRET-TOKEN"} {
		if value == "" {
			continue
		}
		if bytes.Contains(raw, []byte(value)) {
			t.Errorf("the archive contains the %s", label)
		}
	}
	if bytes.Contains(entries["manifest.json"], []byte("SECRET-TOKEN")) {
		t.Error("the manifest mentions an image token")
	}
}

func TestAccountExportOfAnEmptyAccount(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	entries, _, _ := readExport(t, client)
	manifest := decodeManifest(t, entries)
	if len(manifest.Posts) != 0 || len(manifest.Topics) != 0 {
		t.Fatalf("empty account manifest: %+v", manifest)
	}
	if page, ok := entries["archive.html"]; !ok || !strings.Contains(string(page), "not posted") {
		t.Fatal("an empty account should still get a readable page")
	}
}

func TestAccountExportRequiresSignIn(t *testing.T) {
	app, _ := newTestApp(t, false)
	anonymous := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	w := anonymous.request("GET", "/account/export", nil, nil)
	requireStatus(t, w, 303)
	if w.Header().Get("Location") != "/login" {
		t.Fatalf("redirect = %q", w.Header().Get("Location"))
	}
}

func TestExportWithholdsNamesTheMemberCanNoLongerRead(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	writer := sessionClient(t, f.app, f.writer)
	unpacked := func(entries map[string][]byte) []byte {
		return append(entries["manifest.json"], entries["archive.html"]...)
	}
	entries, _, _ := readExport(t, writer)
	if text := unpacked(entries); !bytes.Contains(text, []byte("Secret party plans")) || !bytes.Contains(text, []byte("Hidden planning room")) {
		t.Fatal("a member with access lost the names in their export")
	}
	if _, _, err := f.app.store.Reply(ctx, f.topicID, f.owner.ID, "Owner reply after access changed"); err != nil {
		t.Fatal(err)
	}
	b, err := f.app.store.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.store.SaveBoard(ctx, f.owner.ID, b, map[int64]string{f.reader.ID: "read"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.store.db.Exec("UPDATE boards SET name = 'Renamed secret room' WHERE id = ?", f.boardID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.store.db.Exec("UPDATE topics SET title = 'Renamed secret plans' WHERE id = ?", f.topicID); err != nil {
		t.Fatal(err)
	}

	if _, err := f.app.store.db.Exec(`INSERT INTO attachments(id,post_id,server,remote_id,credential_user_id,name,rendition) VALUES((SELECT last_id+1 FROM object_sequences WHERE kind='attachments'),(SELECT id FROM posts WHERE author_id=? AND topic_id=? LIMIT 1),'https://vault.example','private-secret',?,'secret image.png','preview')`, f.writer.ID, f.topicID, f.writer.ID); err != nil {
		t.Fatal(err)
	}
	entries, _, _ = readExport(t, writer)
	for _, hidden := range []string{"Secret party plans", "Hidden planning room", "Renamed secret room", "Renamed secret plans", "Private corners", "Owner reply", "private-secret", "secret image.png"} {
		if bytes.Contains(unpacked(entries), []byte(hidden)) {
			t.Errorf("export reveals %q after access was removed", hidden)
		}
	}
	manifest := decodeManifest(t, entries)
	if len(manifest.Posts) != 1 || manifest.Posts[0].Body != "Original message" || manifest.Posts[0].TopicTitle != "" || manifest.Posts[0].BoardName != "" {
		t.Fatalf("the member's own words must stay, without current names: %+v", manifest.Posts)
	}
	if len(manifest.Topics) != 1 || manifest.Topics[0].Title != "" || manifest.Topics[0].Posts != 1 || manifest.Topics[0].UpdatedAt != manifest.Topics[0].CreatedAt {
		t.Fatalf("hidden conversation reveals its activity: %+v", manifest.Topics)
	}
	if len(manifest.Boards) != 1 || manifest.Boards[0].Name != "" || manifest.Boards[0].Category != "" || manifest.Boards[0].ID != f.boardID {
		t.Fatalf("hidden board reveals its name: %+v", manifest.Boards)
	}
	page := string(entries["archive.html"])
	if !strings.Contains(page, "A conversation you can no longer read") || !strings.Contains(page, "A board you can no longer read") {
		t.Fatal("archive page does not explain the missing names")
	}
}

// failingWriter accepts a few bytes of the response and then fails, like a
// connection that drops partway through a download.
type failingWriter struct {
	header  http.Header
	status  int
	limit   int
	written int
}

func (w *failingWriter) Header() http.Header { return w.header }
func (w *failingWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *failingWriter) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.written+len(p) > w.limit {
		return 0, io.ErrClosedPipe
	}
	w.written += len(p)
	return len(p), nil
}

func TestFailedExportAbortsInsteadOfEndingCleanly(t *testing.T) {
	app, client := newTestApp(t, false)
	userID := signInTest(t, app, client, false)
	if _, err := app.store.CreateTopic(context.Background(), 1, userID, "Our weekend", strings.Repeat("Words worth keeping. ", 500), AudienceMembers); err != nil {
		t.Fatal(err)
	}
	_, raw, _ := readExport(t, client)
	for _, limit := range []int{0, 100, len(raw) / 2, len(raw) - 30} {
		r := httptest.NewRequest("GET", "/account/export", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		for _, cookie := range client.cookies {
			r.AddCookie(cookie)
		}
		w := &failingWriter{header: http.Header{}, limit: limit}
		aborted := func() (recovered any) {
			defer func() { recovered = recover() }()
			app.ServeHTTP(w, r)
			return nil
		}()
		if aborted != http.ErrAbortHandler {
			t.Fatalf("export cut off after %d bytes finished with %v instead of aborting", limit, aborted)
		}
	}
}
