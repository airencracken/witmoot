package forum

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"witmoot/internal/imvault"
)

type vaultTransport func(*http.Request) (*http.Response, error)

func (f vaultTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var testPNG = []byte("\x89PNG\r\n\x1a\nfixture")

type vaultFixture struct {
	requests, uploads int
	deleted           []string
	beforeUpload      func()
	unavailable       bool
	// imageBytes is what the fixture serves for image requests. It defaults to
	// testPNG; avatar import needs a real, decodable image.
	imageBytes []byte
}

func imageTestApp(t *testing.T) (*App, *testClient, int64, *vaultFixture) {
	t.Helper()
	a, c := newTestApp(t, false)
	id := signInTest(t, a, c, true)
	a.config.ImageKey = bytes.Repeat([]byte{42}, 32)
	var err error
	a.vault, err = imvault.New("https://vault.example")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &vaultFixture{imageBytes: testPNG}
	a.vault.HTTP.Transport = vaultTransport(func(r *http.Request) (*http.Response, error) {
		fixture.requests++
		w := httptest.NewRecorder()
		if r.URL.Host != "vault.example" || r.Header.Get("Cookie") != "" {
			t.Fatal("untrusted upstream or forwarded cookies")
		}
		if fixture.unavailable {
			w.WriteHeader(503)
			return w.Result(), nil
		}
		token := r.Header.Get("Authorization")
		path := r.URL.Path
		if path == "/f/public/thumb" && token == "" {
			w.Write(fixture.imageBytes)
			return w.Result(), nil
		}
		if token != "Bearer test-key" {
			w.WriteHeader(401)
			return w.Result(), nil
		}
		switch {
		case path == "/api/v1/me":
			io.WriteString(w, `{"username":"alex"}`)
		case path == "/api/v1/files":
			io.WriteString(w, `{"files":[{"id":"own","kind":"image","name":"Family photo.png","visibility":"private"}],"total":1}`)
		case r.Method == "DELETE":
			fixture.deleted = append(fixture.deleted, strings.TrimPrefix(path, "/api/v1/files/"))
			w.WriteHeader(204)
		case path == "/api/v1/upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			if r.PostForm.Get("visibility") != "private" || r.PostForm.Get("metadata") != "hidden" {
				t.Fatal("upload not private")
			}
			fixture.uploads++
			if fixture.beforeUpload != nil {
				fixture.beforeUpload()
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"files": []imvault.File{{ID: fmt.Sprintf("upload%d", fixture.uploads), Name: "New photo.png", Kind: "image", Visibility: "private"}}})
		case strings.HasPrefix(path, "/api/v1/files/"):
			id := strings.TrimPrefix(path, "/api/v1/files/")
			if id != "own" && !strings.HasPrefix(id, "upload") {
				w.WriteHeader(404)
				break
			}
			json.NewEncoder(w).Encode(imvault.File{ID: id, Name: "Family photo.png", Kind: "image", Visibility: "private"})
		case strings.HasPrefix(path, "/f/own/") || strings.HasPrefix(path, "/f/upload"):
			w.Write(fixture.imageBytes)
		default:
			w.WriteHeader(404)
		}
		return w.Result(), nil
	})
	requireStatus(t, c.post("/account/imvault", url.Values{"api_key": {"test-key"}}), 303)
	return a, c, id, fixture
}

func TestPrivateBoardImageAccessAndRevocation(t *testing.T) {
	a, _, ownerID, upstream := imageTestApp(t)
	ctx := context.Background()
	reader := &User{ID: testMember(t, a.store, "reader"), Role: "member"}
	outsider := &User{ID: testMember(t, a.store, "outside"), Role: "member"}
	boardID, err := a.store.SaveBoard(ctx, ownerID, Board{Name: "Private photos", Category: "Friends", Restricted: true}, map[int64]string{reader.ID: "read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.CreateTopic(ctx, boardID, ownerID, "Family photos", "Just for this room", AudienceMembers, Attachment{Server: a.vault.Base, RemoteID: "own", CredentialUserID: sql.NullInt64{Int64: ownerID, Valid: true}, Name: "Family photo.png", Rendition: "thumb"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetMode(ctx, ModeOpen); err != nil {
		t.Fatal(err)
	}
	allowed := sessionClient(t, a, reader)
	requireStatus(t, allowed.request("GET", "/images/1", nil, nil), 200)
	for _, user := range []*User{outsider, nil} {
		c := sessionClient(t, a, user)
		before := upstream.requests
		requireStatus(t, c.request("GET", "/images/1", nil, nil), 404)
		if upstream.requests != before {
			t.Fatal("hidden image reached imvault")
		}
	}
	b, err := a.store.Board(ctx, boardID, &User{ID: ownerID, Role: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.SaveBoard(ctx, ownerID, b, nil, nil); err != nil {
		t.Fatal(err)
	}
	before := upstream.requests
	requireStatus(t, allowed.request("GET", "/images/1", nil, nil), 404)
	if upstream.requests != before {
		t.Fatal("revoked image access reached imvault")
	}
}

func TestBoardRevocationDuringUploadRollsBackReplyAndCleansImage(t *testing.T) {
	a, _, ownerID, upstream := imageTestApp(t)
	ctx := context.Background()
	writer := &User{ID: testMember(t, a.store, "writer"), Role: "member"}
	c := sessionClient(t, a, writer)
	requireStatus(t, c.post("/account/imvault", url.Values{"api_key": {"test-key"}}), 303)
	boardID, err := a.store.SaveBoard(ctx, ownerID, Board{Name: "Private uploads", Category: "Friends", Restricted: true}, map[int64]string{writer.ID: "write"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	topicID, err := a.store.CreateTopic(ctx, boardID, ownerID, "Family photos", "Just for this room", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	board, err := a.store.Board(ctx, boardID, &User{ID: ownerID, Role: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	upstream.beforeUpload = func() {
		if _, err := a.store.SaveBoard(ctx, ownerID, board, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	w := multipartPost(t, c, fmt.Sprintf("/topics/%d/replies", topicID), url.Values{"body": {"Permission revoked during upload"}}, [][]byte{testPNG}, true)
	requireStatus(t, w, 404)
	if upstream.uploads != 1 || len(upstream.deleted) != 1 || upstream.deleted[0] != "upload1" {
		t.Fatalf("unattached upload was not cleaned: %+v", upstream)
	}
	stats, err := a.store.Stats(ctx, &User{ID: ownerID, Role: "owner"})
	if err != nil || stats.Posts != 1 {
		t.Fatalf("revoked reply persisted: %+v %v", stats, err)
	}
}

func TestGroupRevocationDuringUploadRollsBackAndProtectsImages(t *testing.T) {
	for _, compose := range []string{"topic", "reply"} {
		t.Run(compose, func(t *testing.T) {
			a, _, ownerID, upstream := imageTestApp(t)
			ctx := context.Background()
			writer := &User{ID: testMember(t, a.store, "writer"), Role: "member"}
			reader := &User{ID: testMember(t, a.store, "reader"), Role: "member"}
			c := sessionClient(t, a, writer)
			requireStatus(t, c.post("/account/imvault", url.Values{"api_key": {"test-key"}}), 303)
			groupID, err := a.store.SaveGroup(ctx, ownerID, Group{Name: "Photo club"}, []int64{writer.ID, reader.ID})
			if err != nil {
				t.Fatal(err)
			}
			boardID, err := a.store.SaveBoard(ctx, ownerID, Board{Name: "Private photos", Category: "Friends", Restricted: true}, nil, map[int64]string{groupID: "write"})
			if err != nil {
				t.Fatal(err)
			}
			topicID, err := a.store.CreateTopic(ctx, boardID, ownerID, "Family photos", "Keep the original", AudienceMembers, Attachment{Server: a.vault.Base, RemoteID: "own", CredentialUserID: sql.NullInt64{Int64: ownerID, Valid: true}, Name: "Family photo.png", Rendition: "thumb"})
			if err != nil {
				t.Fatal(err)
			}
			requireStatus(t, c.request("GET", "/images/1", nil, nil), 200)
			g, err := a.store.Group(ctx, groupID)
			if err != nil {
				t.Fatal(err)
			}
			upstream.beforeUpload = func() {
				if _, err := a.store.SaveGroup(ctx, ownerID, g, []int64{reader.ID}); err != nil {
					t.Fatal(err)
				}
			}
			path := fmt.Sprintf("/boards/%d/new", boardID)
			if compose == "reply" {
				path = fmt.Sprintf("/topics/%d/replies", topicID)
			}
			requireStatus(t, multipartPost(t, c, path, imageForm(), [][]byte{testPNG}, true), 404)
			if upstream.uploads != 1 || len(upstream.deleted) != 1 || upstream.deleted[0] != "upload1" {
				t.Fatalf("unfinished upload was not cleaned: %+v", upstream)
			}
			before := upstream.requests
			requireStatus(t, c.request("GET", "/images/1", nil, nil), 404)
			if upstream.requests != before {
				t.Fatal("revoked group member reached imvault")
			}
			allowed := sessionClient(t, a, reader)
			requireStatus(t, allowed.request("GET", "/images/1", nil, nil), 200)
			stats, err := a.store.Stats(ctx, reader)
			if err != nil || stats.Topics != 1 || stats.Posts != 1 {
				t.Fatalf("stale group permission left content: %+v %v", stats, err)
			}
		})
	}
}

func TestBoardLifecycleDuringUploadCleansOnlyUnattachedImages(t *testing.T) {
	for _, action := range []string{"archive", "delete"} {
		for _, compose := range []string{"topic", "reply"} {
			t.Run(action+"/"+compose, func(t *testing.T) {
				a, c, ownerID, upstream := imageTestApp(t)
				ctx := context.Background()
				img := Attachment{Server: a.vault.Base, RemoteID: "own", CredentialUserID: sql.NullInt64{Int64: ownerID, Valid: true}, Name: "Family photo.png", Rendition: "thumb"}
				topicID, err := a.store.CreateTopic(ctx, 1, ownerID, "Existing photos", "Keep the original", AudienceMembers, img)
				if err != nil {
					t.Fatal(err)
				}
				// An attachment on another board can reference the same imvault original.
				if _, err := a.store.CreateTopic(ctx, 2, ownerID, "Another conversation", "Still using this photo", AudienceMembers, img); err != nil {
					t.Fatal(err)
				}
				upstream.beforeUpload = func() {
					if err := a.store.ChangeBoard(ctx, ownerID, 1, 0, action, "The kitchen table"); err != nil {
						t.Fatal(err)
					}
				}
				path := "/boards/1/new"
				if compose == "reply" {
					path = fmt.Sprintf("/topics/%d/replies", topicID)
				}
				want, posts := 403, 2
				if action == "delete" {
					want, posts = 404, 1
				}
				requireStatus(t, multipartPost(t, c, path, imageForm(), [][]byte{testPNG}, true), want)
				if upstream.uploads != 1 || len(upstream.deleted) != 1 || upstream.deleted[0] != "upload1" {
					t.Fatalf("cleanup must remove only the new unattached upload: %+v", upstream)
				}
				stats, err := a.store.Stats(ctx, &User{ID: ownerID, Role: "owner"})
				if err != nil || stats.Posts != posts || stats.Topics != posts {
					t.Fatalf("unexpected surviving content: %+v %v", stats, err)
				}
				requireStatus(t, c.request("GET", "/images/2", nil, nil), 200)
				before := upstream.requests
				if action == "archive" {
					requireStatus(t, c.request("GET", "/images/1", nil, nil), 200)
				} else {
					requireStatus(t, c.request("GET", "/images/1", nil, nil), 404)
					if upstream.requests != before {
						t.Fatal("deleted image URL reached imvault")
					}
				}
			})
		}
	}
}

func multipartPost(t *testing.T, c *testClient, path string, form url.Values, files [][]byte, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if csrf {
		form.Set("csrf", c.cookies[c.app.cookieName("csrf")].Value)
	}
	for key, values := range form {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, data := range files {
		p, err := writer.CreateFormFile("images", "upload.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.RemoteAddr = "127.0.0.1:1234"
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	c.app.ServeHTTP(w, r)
	return w
}
func imageForm() url.Values {
	return url.Values{"title": {"Shared pictures"}, "body": {"A good day together"}, "audience": {"members"}}
}
func attachmentIDs(t *testing.T, a *App) []int64 {
	t.Helper()
	rows, err := a.store.db.Query("SELECT id FROM attachments ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestImageConnectionsAreEncryptedBoundAndRevocable(t *testing.T) {
	a, c, id, _ := imageTestApp(t)
	ctx := context.Background()
	connection, err := a.store.Connection(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(connection.Token, []byte("test-key")) {
		t.Fatal("plaintext stored")
	}
	if token, err := a.imageToken(ctx, id); err != nil || token != "test-key" {
		t.Fatalf("token read: %v", err)
	}
	w := c.request("GET", "/account/imvault", nil, nil)
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "test-key") {
		t.Fatal("API key leaked into HTML")
	}
	member := memberClient(t, a, "jules")
	u, _, _ := a.store.Credentials(ctx, "jules")
	if err := a.store.ConnectImages(ctx, u.ID, connection); err != nil {
		t.Fatal(err)
	}
	if _, err := a.imageToken(ctx, u.ID); err == nil {
		t.Fatal("token accepted for different user")
	}
	requireStatus(t, member.request("GET", "/imvault/library/own/image", nil, nil), 404)
	connection.Server = "https://different.example"
	if err := a.store.ConnectImages(ctx, id, connection); err != nil {
		t.Fatal(err)
	}
	if _, err := a.imageToken(ctx, id); err == nil {
		t.Fatal("token accepted for different server")
	}
	requireStatus(t, c.post("/account/imvault", url.Values{"api_key": {"test-key"}}), 303)
	selectMode(t, c, ModePersonal)
	requireStatus(t, member.request("GET", "/account/imvault", nil, nil), 200)
	requireStatus(t, member.post("/account/imvault/disconnect", nil), 303)
	if _, err := a.store.Connection(ctx, u.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("disconnect did not remove token")
	}
}

func TestImageKeyPersistenceAndInvalidKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imvault.key")
	first, err := LoadImageKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadImageKey(path)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("key not persistent")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("key permissions")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadImageKey(path); err == nil {
		t.Fatal("silently replaced broken key")
	}
	if _, err := New(testStore(t), Config{ImvaultURL: "https://vault.example"}); err == nil {
		t.Fatal("enabled without encryption key")
	}
}

func TestAllImagePathsAndAudienceGateway(t *testing.T) {
	a, c, _, f := imageTestApp(t)
	form := imageForm()
	form.Set("image_ids", "own")
	form.Set("image_links", "https://vault.example/f/public")
	w := multipartPost(t, c, "/boards/1/new", form, [][]byte{testPNG}, true)
	requireStatus(t, w, 303)
	topicPath := w.Header().Get("Location")
	ids := attachmentIDs(t, a)
	if len(ids) != 3 || f.uploads != 1 {
		t.Fatalf("attachments %v uploads %d", ids, f.uploads)
	}
	requireStatus(t, c.post(topicPath+"/replies", url.Values{"body": {"Here is that photo again"}, "image_links": {"https://vault.example/f/own/raw"}}), 303)
	w = c.request("GET", topicPath, nil, nil)
	requireStatus(t, w, 200)
	if strings.Count(w.Body.String(), `<figure>`) != 4 {
		t.Fatal("post images not rendered")
	}
	member := memberClient(t, a, "jules")
	guest := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	for _, mode := range []Mode{ModePrivate, ModeOpen, ModePersonal} {
		selectMode(t, c, mode)
		for _, id := range ids {
			path := fmt.Sprintf("/images/%d", id)
			w := c.request("GET", path, nil, nil)
			requireStatus(t, w, 200)
			if !bytes.Equal(w.Body.Bytes(), testPNG) || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("image response headers/body")
			}
			guestCode, memberCode := 303, 200
			if mode == ModeOpen {
				guestCode = 404
			}
			if mode == ModePersonal {
				memberCode = 403
			}
			requireStatus(t, guest.request("GET", path, nil, nil), guestCode)
			requireStatus(t, member.request("GET", path, nil, nil), memberCode)
		}
	}
	selectMode(t, c, ModeOpen)
	form = imageForm()
	form.Set("audience", "public")
	form.Set("image_ids", "own")
	requireStatus(t, c.post("/boards/1/new", form), 303)
	allIDs := attachmentIDs(t, a)
	publicPath := fmt.Sprintf("/images/%d", allIDs[len(allIDs)-1])
	requireStatus(t, guest.request("GET", publicPath, nil, nil), 200)
	requireStatus(t, guest.request("HEAD", publicPath, nil, nil), 200)
	requireStatus(t, guest.request("GET", "/imvault/library/own/image", nil, nil), 303)
	requireStatus(t, member.request("GET", "/imvault/library/own/image", nil, nil), 404)
	requireStatus(t, c.post("/account/imvault/disconnect", nil), 303)
	requireStatus(t, guest.request("GET", publicPath, nil, nil), 404)
	requireStatus(t, c.request("GET", fmt.Sprintf("/images/%d", ids[1]), nil, nil), 200) // Public pasted image needs no connection.
}

func TestImageValidationAndCSRFBeforeUpload(t *testing.T) {
	a, c, _, f := imageTestApp(t)
	for _, tc := range []struct {
		name   string
		values url.Values
		files  [][]byte
		csrf   bool
		code   int
	}{
		{"csrf", imageForm(), [][]byte{testPNG}, false, 403},
		{"type", imageForm(), [][]byte{[]byte("<svg></svg>")}, true, 422},
		{"size", imageForm(), [][]byte{append(testPNG, make([]byte, imvault.MaxImageBytes)...)}, true, 422},
		{"too many", imageForm(), [][]byte{testPNG, testPNG, testPNG, testPNG, testPNG}, true, 422},
		{"unowned", url.Values{"title": {"A title"}, "body": {"Message"}, "image_ids": {"someoneelses"}}, nil, true, 422},
		{"foreign origin", url.Values{"title": {"A title"}, "body": {"Message"}, "image_links": {"https://evil.example/f/x"}}, nil, true, 422},
		{"private link", url.Values{"title": {"A title"}, "body": {"Message"}, "image_links": {"https://vault.example/f/someoneelses"}}, nil, true, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireStatus(t, multipartPost(t, c, "/boards/1/new", tc.values, tc.files, tc.csrf), tc.code)
		})
	}
	if f.uploads != 0 || len(attachmentIDs(t, a)) != 0 {
		t.Fatal("invalid submission stored images")
	}
	stats, err := a.store.Stats(context.Background(), testReader)
	if err != nil || stats.Topics != 0 {
		t.Fatal("invalid submission created topics")
	}
	f.unavailable = true
	requireStatus(t, c.post("/account/imvault", url.Values{"api_key": {"invalid-key"}}), 422)
	requireStatus(t, c.request("GET", "/imvault/library", nil, nil), 200) // Actionable upstream error, not a broken forum.
	a.vault = nil
	form := imageForm()
	form.Set("image_links", "https://vault.example/f/public")
	requireStatus(t, c.post("/boards/1/new", form), 422)
}

func TestImageRollbackDeletesOnlyNewUploads(t *testing.T) {
	a, c, _, f := imageTestApp(t)
	if _, err := a.store.db.Exec(`CREATE TRIGGER reject_attachment BEFORE INSERT ON attachments BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	form := imageForm()
	form.Set("image_ids", "own")
	form.Set("image_links", "https://vault.example/f/public")
	requireStatus(t, multipartPost(t, c, "/boards/1/new", form, [][]byte{testPNG}, true), 500)
	if strings.Join(f.deleted, ",") != "upload1" {
		t.Fatalf("wrong cleanup: %v", f.deleted)
	}
	stats, err := a.store.Stats(context.Background(), testReader)
	if err != nil || stats.Topics != 0 || stats.Posts != 0 || len(attachmentIDs(t, a)) != 0 {
		t.Fatal("failed attachment did not roll back topic")
	}
	if _, err := a.store.db.Exec("DROP TRIGGER reject_attachment"); err != nil {
		t.Fatal(err)
	}
	f.beforeUpload = func() {
		if err := a.store.SetMode(context.Background(), ModeOpen); err != nil {
			t.Fatal(err)
		}
	}
	requireStatus(t, multipartPost(t, c, "/boards/1/new", imageForm(), [][]byte{testPNG}, true), 422)
	if strings.Join(f.deleted, ",") != "upload1,upload2" {
		t.Fatalf("stale mode upload not cleaned: %v", f.deleted)
	}
}

func TestLibraryFragmentsPreserveSelection(t *testing.T) {
	a, c, _, _ := imageTestApp(t)
	w := c.request("GET", "/imvault/library", nil, map[string]string{"HX-Request": "true", "HX-Boosted": "true", "HX-Target": "body"})
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "<!doctype html>") {
		t.Fatal("boosted library navigation lost layout")
	}
	w = c.request("GET", "/imvault/library?q=photo&image_ids=upload1&image_ids=own", nil, map[string]string{"HX-Request": "true", "HX-Target": "image-library"})
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "<!doctype html>") || !strings.Contains(w.Body.String(), `value="upload1" checked`) || !strings.Contains(w.Body.String(), `value="own" checked`) {
		t.Fatal("library selection not carried across pages")
	}
	requireStatus(t, c.request("GET", "/imvault/library?offset=-1", nil, nil), 400)
	requireStatus(t, c.request("GET", "/imvault/library?offset=5", nil, nil), 400)
	guest := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	requireStatus(t, guest.request("GET", "/imvault/library", nil, nil), 303)
}

func TestConversationRenderingDoesNotContactImvault(t *testing.T) {
	_, c, _, f := imageTestApp(t)
	w := c.post("/boards/1/new", imageForm())
	requireStatus(t, w, 303)
	topic := w.Header().Get("Location")
	f.unavailable = true
	before := f.requests
	for _, path := range []string{"/boards/1/new", topic} {
		requireStatus(t, c.request("GET", path, nil, nil), 200)
	}
	form := imageForm()
	form.Set("title", "x")
	form.Set("image_ids", "own")
	w = c.post("/boards/1/new", form)
	requireStatus(t, w, 422)
	if !strings.Contains(w.Body.String(), `value="own" checked`) || !strings.Contains(w.Body.String(), "A good day together") {
		t.Fatal("validation lost selected image or text")
	}
	requireStatus(t, c.post(topic+"/replies", url.Values{"body": {"A text-only reply"}}), 303)
	if f.requests != before {
		t.Fatalf("text pages/submissions made %d upstream requests", f.requests-before)
	}
}

func TestLibraryLoadPreservesDraftWithoutPosting(t *testing.T) {
	a, c, _, f := imageTestApp(t)
	form := imageForm()
	form.Set("load_images", "1")
	form.Set("image_ids", "upload1")
	form.Set("image_links", "https://vault.example/f/public")
	w := c.post("/boards/1/new", form)
	requireStatus(t, w, 200)
	for _, text := range []string{"Shared pictures", "A good day together", "Family photo.png", `value="upload1" checked`, "https://vault.example/f/public"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Errorf("library load lost %q", text)
		}
	}
	stats, err := a.store.Stats(context.Background(), testReader)
	if err != nil || stats.Topics != 0 || f.uploads != 0 || len(attachmentIDs(t, a)) != 0 {
		t.Fatal("loading library created content")
	}
	w = c.post("/boards/1/new", imageForm())
	requireStatus(t, w, 303)
	topic := w.Header().Get("Location")
	w = c.post(topic+"/replies", url.Values{"body": {"Still writing this reply"}, "load_images": {"1"}})
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Still writing this reply") || !strings.Contains(w.Body.String(), "Family photo.png") {
		t.Fatal("reply library load lost draft or library")
	}
	stats, err = a.store.Stats(context.Background(), testReader)
	if err != nil || stats.Posts != 1 {
		t.Fatal("loading library submitted reply")
	}
	f.unavailable = true
	w = c.post(topic+"/replies", url.Values{"body": {"Keep this draft"}, "load_images": {"1"}})
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Keep this draft") || !strings.Contains(w.Body.String(), imvault.ErrUnavailable.Error()) {
		t.Fatal("upstream failure lost draft or explanation")
	}
}

func TestOwnerOnlyImagesAndReplyAtomicity(t *testing.T) {
	a, c, _, _ := imageTestApp(t)
	member := memberClient(t, a, "jules")
	selectMode(t, c, ModePersonal)
	form := imageForm()
	form.Set("audience", "owners")
	form["image_ids"] = []string{"own", "own"}
	w := c.post("/boards/1/new", form)
	requireStatus(t, w, 303)
	ids := attachmentIDs(t, a)
	if len(ids) != 1 {
		t.Fatal("duplicate reference was not deduplicated")
	}
	topicPath := w.Header().Get("Location")
	selectMode(t, c, ModeOpen)
	requireStatus(t, member.request("GET", fmt.Sprintf("/images/%d", ids[0]), nil, nil), 404)
	if _, err := a.store.db.Exec(`CREATE TRIGGER reject_reply_attachment BEFORE INSERT ON attachments BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, c.post(topicPath+"/replies", url.Values{"body": {"A failed reply"}, "image_ids": {"own"}}), 500)
	stats, err := a.store.Stats(context.Background(), testReader)
	if err != nil || stats.Posts != 1 || len(attachmentIDs(t, a)) != 1 {
		t.Fatal("failed image did not roll back reply")
	}
}
