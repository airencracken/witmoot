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
		reader.Close()
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
