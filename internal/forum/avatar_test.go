package forum

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func pngAvatarFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func postAvatarMultipart(t *testing.T, client *testClient, fields url.Values, file []byte) *httptest.ResponseRecorder {
	t.Helper()
	if fields == nil {
		fields = make(url.Values)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if cookie := client.cookies[client.app.cookieName("csrf")]; cookie != nil {
		fields.Set("csrf", cookie.Value)
	}
	for key, values := range fields {
		for _, value := range values {
			if err := form.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if file != nil {
		part, err := form.CreateFormFile("avatar", "avatar.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/account/avatar", &body)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Content-Type", form.FormDataContentType())
	for _, cookie := range client.cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	client.app.ServeHTTP(w, r)
	return w
}

func TestAvatarNormalizationAndStorage(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	user := testMember(t, store, "alex")

	// A portrait source is cropped square and scaled to a fixed size.
	if err := store.SaveAvatar(ctx, user, pngAvatarFixture(t, 300, 500)); err != nil {
		t.Fatal(err)
	}
	data, updated, err := store.Avatar(ctx, user)
	if err != nil || updated <= 0 {
		t.Fatalf("stored avatar: %d bytes, updated=%d, err=%v", len(data), updated, err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != avatarPixels || config.Height != avatarPixels {
		t.Fatalf("avatar is %dx%d: %v", config.Width, config.Height, err)
	}
	if has, err := store.HasAvatar(ctx, user); err != nil || !has {
		t.Fatalf("HasAvatar = %v, %v", has, err)
	}

	if err := store.SaveAvatar(ctx, user, []byte("not an image")); err == nil {
		t.Fatal("a non-image was accepted")
	}
	if err := store.DeleteAvatar(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Avatar(ctx, user); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("avatar survived deletion: %v", err)
	}
	if has, _ := store.HasAvatar(ctx, user); has {
		t.Fatal("HasAvatar is true after deletion")
	}
}

func TestMemberCanUploadAndRemoveAvatar(t *testing.T) {
	app, client := newTestApp(t, false)
	userID := signInTest(t, app, client, false)
	ctx := context.Background()

	requireStatus(t, postAvatarMultipart(t, client, nil, pngAvatarFixture(t, 200, 200)), http.StatusSeeOther)

	account := client.request("GET", "/account", nil, nil).Body.String()
	if !strings.Contains(account, `src="/avatars/`+strconv.FormatInt(userID, 10)+`"`) {
		t.Fatal("the account page does not show the avatar")
	}

	avatarPath := "/avatars/" + strconv.FormatInt(userID, 10)
	w := client.request("GET", avatarPath, nil, nil)
	requireStatus(t, w, http.StatusOK)
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("content type = %q", w.Header().Get("Content-Type"))
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	requireStatus(t, client.request("GET", avatarPath, nil, map[string]string{"If-None-Match": etag}), http.StatusNotModified)

	topicID, err := app.store.CreateTopic(ctx, 1, userID, "Testing avatars", "Hello", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	topic := client.request("GET", "/topics/"+strconv.FormatInt(topicID, 10), nil, nil).Body.String()
	if !strings.Contains(topic, `<img class="avatar" src="/avatars/`+strconv.FormatInt(userID, 10)+`"`) {
		t.Fatal("the avatar is not shown beside a post")
	}

	requireStatus(t, client.post("/account/avatar", url.Values{"remove": {"1"}}), http.StatusSeeOther)
	requireStatus(t, client.request("GET", avatarPath, nil, nil), http.StatusNotFound)
}

func TestAvatarAccessFollowsTheBoard(t *testing.T) {
	app, client := newTestApp(t, false)
	userID := signInTest(t, app, client, false)
	requireStatus(t, postAvatarMultipart(t, client, nil, pngAvatarFixture(t, 64, 64)), http.StatusSeeOther)
	path := "/avatars/" + strconv.FormatInt(userID, 10)

	guest := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	requireStatus(t, guest.request("GET", path, nil, nil), http.StatusSeeOther)

	// An Open board lets visitors read, so the avatar is reachable too.
	if err := app.store.SetMode(context.Background(), ModeOpen); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, guest.request("GET", path, nil, nil), http.StatusOK)
}

func TestOwnerCanRemoveAMemberAvatar(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	ctx := context.Background()
	memberID := testMember(t, app.store, "jules")
	if err := app.store.SaveAvatar(ctx, memberID, pngAvatarFixture(t, 64, 64)); err != nil {
		t.Fatal(err)
	}

	// A member cannot clear somebody else's avatar.
	member := memberClient(t, app, "sam")
	requireStatus(t, member.post("/members/"+strconv.FormatInt(memberID, 10)+"/avatar/remove", nil), http.StatusForbidden)

	requireStatus(t, owner.post("/members/"+strconv.FormatInt(memberID, 10)+"/avatar/remove", nil), http.StatusSeeOther)
	if _, _, err := app.store.Avatar(ctx, memberID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("avatar survived owner removal: %v", err)
	}
}

func TestImportAvatarFromImvault(t *testing.T) {
	app, client, ownerID, fixture := imageTestApp(t)
	// The fixture serves a decodable image for this test.
	fixture.imageBytes = pngAvatarFixture(t, 240, 240)
	ctx := context.Background()

	library := client.request("GET", "/account/avatar/imvault", nil, nil)
	requireStatus(t, library, http.StatusOK)
	if !strings.Contains(library.Body.String(), "Family photo.png") {
		t.Fatal("the imvault library was not listed")
	}

	requireStatus(t, client.post("/account/avatar/imvault", url.Values{"image_id": {"own"}}), http.StatusSeeOther)
	data, _, err := app.store.Avatar(ctx, ownerID)
	if err != nil || len(data) == 0 {
		t.Fatalf("avatar was not imported: %v", err)
	}
	if config, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || config.Width != avatarPixels {
		t.Fatalf("imported avatar is %dx%d: %v", config.Width, config.Height, err)
	}

	// A malformed or foreign ID is refused rather than looked up.
	requireStatus(t, client.post("/account/avatar/imvault", url.Values{"image_id": {"not valid!"}}), http.StatusBadRequest)
}

func TestAvatarExportIncludesTheImage(t *testing.T) {
	app, client := newTestApp(t, false)
	userID := signInTest(t, app, client, false)
	if err := app.store.SaveAvatar(context.Background(), userID, pngAvatarFixture(t, 96, 96)); err != nil {
		t.Fatal(err)
	}

	entries, _, _ := readExport(t, client)
	if _, ok := entries["avatar.png"]; !ok {
		t.Fatalf("the export has no avatar: %v", entries)
	}
	manifest := decodeManifest(t, entries)
	if !manifest.Account.HasAvatar {
		t.Fatal("the manifest does not record the avatar")
	}
}
