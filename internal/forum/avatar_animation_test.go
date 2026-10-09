package forum

import (
	"bytes"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func animatedAvatarFixture(t *testing.T, frames, size int) []byte {
	t.Helper()
	v := &gif.GIF{LoopCount: 0}
	for i := 0; i < frames; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, size, size), color.Palette{color.Black, color.White})
		frame.SetColorIndex(i%size, i%size, 1)
		v.Image = append(v.Image, frame)
		v.Delay = append(v.Delay, 1)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAnimatedAvatarPolicyHTTPExportAndRemoval(t *testing.T) {
	app, client := newTestApp(t, false)
	id := signInTest(t, app, client, false)
	ctx := t.Context()
	path := "/avatars/" + strconv.FormatInt(id, 10)
	requireStatus(t, postAvatarMultipart(t, client, nil, animatedAvatarFixture(t, 3, 32)), 303)
	w := client.request("GET", path, nil, nil)
	requireStatus(t, w, 200)
	if w.Header().Get("Content-Type") != "image/gif" {
		t.Fatal("animation not served")
	}
	animation, err := gif.DecodeAll(bytes.NewReader(w.Body.Bytes()))
	if err != nil || len(animation.Image) != 3 {
		t.Fatal("animation lost", err)
	}
	for _, delay := range animation.Delay {
		if delay < 10 {
			t.Fatal("unbounded frame rate")
		}
	}
	animatedETag := w.Header().Get("ETag")
	w = client.request("GET", path+"?still=1", nil, map[string]string{"If-None-Match": animatedETag})
	requireStatus(t, w, 200)
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("still not served")
	}
	stillETag := w.Header().Get("ETag")
	head := client.request("HEAD", path, nil, nil)
	requireStatus(t, head, 200)
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD body contract")
	}
	requireStatus(t, client.request("GET", path, nil, map[string]string{"If-None-Match": animatedETag}), 304)
	requireStatus(t, client.post("/account/avatar-preference", nil), 303)
	w = client.request("GET", path, nil, map[string]string{"If-None-Match": animatedETag})
	requireStatus(t, w, 200)
	if w.Header().Get("ETag") != stillETag {
		t.Fatal("viewer preference ignored")
	}
	entries, _, _ := readExport(t, client)
	manifest := decodeManifest(t, entries)
	if len(entries["avatar.gif"]) == 0 || len(entries["avatar.png"]) == 0 || !manifest.Account.HasAnimatedAvatar || manifest.Account.AnimateAvatars == nil || *manifest.Account.AnimateAvatars {
		t.Fatal("export lost profile")
	}
	if !strings.Contains(string(entries["archive.html"]), `src="avatar.png"`) {
		t.Fatal("offline page should use still")
	}
	requireStatus(t, client.post("/account/avatar-preference", url.Values{"animate": {"1", "0"}}), 422)
	requireStatus(t, client.request("GET", "/account/avatar-preference", nil, nil), 405)
	requireStatus(t, client.request("POST", "/account/avatar-preference", url.Values{"animate": {"1"}}, nil), 403)
	if on, _ := app.store.AnimateAvatars(ctx, id); on {
		t.Fatal("invalid input changed preference")
	}
	requireStatus(t, postAvatarMultipart(t, client, url.Values{"remove": {"1"}}, animatedAvatarFixture(t, 2, 8)), 422)
	if data, _ := app.store.AvatarAnimation(ctx, id); len(data) == 0 {
		t.Fatal("ambiguous removal changed image")
	}
	account := client.request("GET", "/account", nil, nil).Body.String()
	if !strings.Contains(account, `media="(prefers-reduced-motion: reduce)"`) {
		t.Fatal("reduced motion source missing")
	}
	if err := app.store.SetMode(ctx, ModeOpen); err != nil {
		t.Fatal(err)
	}
	guest := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	w = guest.request("GET", path, nil, nil)
	requireStatus(t, w, 200)
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("visitor animation")
	}
	requireStatus(t, client.post("/account/avatar", url.Values{"remove": {"1"}}), 303)
	requireStatus(t, client.request("GET", path, nil, nil), 404)
	if on, _ := app.store.AnimateAvatars(ctx, id); on {
		t.Fatal("removal reset viewer preference")
	}
}

func TestAnimatedAvatarBoundsAtomicityAndSchema(t *testing.T) {
	s := testStore(t)
	id := testMember(t, s, "avatar-member")
	ctx := t.Context()
	valid := animatedAvatarFixture(t, 2, 8)
	if err := s.SaveAvatar(ctx, id, valid); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.Avatar(ctx, id)
	beforeAnimation, _ := s.AvatarAnimation(ctx, id)
	for _, data := range [][]byte{nil, []byte("<svg onload='bad'>"), animatedAvatarFixture(t, 65, 8), animatedAvatarFixture(t, 2, 513), append(append([]byte{}, valid...), 0), valid[:len(valid)-1], make([]byte, (2<<20)+1)} {
		if err := s.SaveAvatar(ctx, id, data); err == nil {
			t.Fatal("adversarial avatar accepted")
		}
		after, _, _ := s.Avatar(ctx, id)
		animation, _ := s.AvatarAnimation(ctx, id)
		if !bytes.Equal(before, after) || !bytes.Equal(beforeAnimation, animation) {
			t.Fatal("failed replacement changed avatar")
		}
	}
	for _, value := range []int{-1, 2, 99} {
		if _, err := s.db.Exec("INSERT INTO avatar_preferences(user_id,animate) VALUES(?,?)", id, value); err == nil {
			t.Fatal("schema accepted invalid preference")
		}
	}
	if _, err := s.db.Exec("UPDATE user_avatars SET animation=X'00' WHERE user_id=?", id); err == nil {
		t.Fatal("schema accepted invalid animation")
	}
	// Vary valid frame counts and preferences; replacements must preserve preference.
	for frames := 1; frames <= 64; frames++ {
		on := frames%2 == 0
		if err := s.SetAnimateAvatars(ctx, id, on); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveAvatar(ctx, id, animatedAvatarFixture(t, frames, 8)); err != nil {
			t.Fatal(frames, err)
		}
		animation, err := s.AvatarAnimation(ctx, id)
		if err != nil || (len(animation) > 0) != (frames > 1) {
			t.Fatal("frame-count property", frames, err)
		}
		if actual, err := s.AnimateAvatars(ctx, id); err != nil || actual != on {
			t.Fatal("preference property", frames, err)
		}
	}
	if err := s.SaveAvatar(ctx, id, pngAvatarFixture(t, 32, 32)); err != nil {
		t.Fatal(err)
	}
	if animation, _ := s.AvatarAnimation(ctx, id); len(animation) != 0 {
		t.Fatal("static replacement retained animation")
	}
	user, hash, err := s.Credentials(ctx, "avatar-member")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, id, user.Username, hash); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAvatar(ctx, id, valid); err == nil {
		t.Fatal("deleted account replaced avatar")
	}
	if err := s.SetAnimateAvatars(ctx, id, true); err == nil {
		t.Fatal("deleted account replaced preferences")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM avatar_preferences WHERE user_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatal("deletion retained preference", err)
	}
}

func TestAvatarAnimationMigrationPreservesExistingPicture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "witmoot.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range migrationFiles[:17] {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body) + fmt.Sprintf("; PRAGMA user_version=%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	picture := pngAvatarFixture(t, 32, 32)
	if _, err := db.Exec("INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'alex','hash','owner',123)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO user_avatars(user_id,content,updated_at) VALUES(1,?,123)", picture); err != nil {
		t.Fatal(err)
	}
	closeTest(t, db)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, s)
	actual, updated, err := s.Avatar(t.Context(), 1)
	if err != nil || updated != 123 || !bytes.Equal(actual, picture) {
		t.Fatal("migration changed avatar", err)
	}
	if data, err := s.AvatarAnimation(t.Context(), 1); err != nil || len(data) != 0 {
		t.Fatal("migration invented animation", err)
	}
	if animate, err := s.AnimateAvatars(t.Context(), 1); err != nil || !animate {
		t.Fatal("preference default", err)
	}
}
