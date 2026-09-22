package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOwnerCanSelectAccessModes(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	w := owner.request("GET", "/settings", nil, nil)
	requireStatus(t, w, 200)
	for _, label := range []string{"Personal", "Private", "Open"} {
		if !strings.Contains(w.Body.String(), label) {
			t.Fatalf("settings missing %s", label)
		}
	}
	for _, mode := range []string{"personal", "open", "private"} {
		w = owner.post("/settings", url.Values{"mode": {mode}})
		requireStatus(t, w, 303)
		if w.Header().Get("Location") != "/settings?saved=1" {
			t.Fatalf("settings redirect: %s", w.Header().Get("Location"))
		}
	}
}

func TestSettingsRequireOwnerAndCSRF(t *testing.T) {
	app, member := newTestApp(t, false)
	member.request("GET", "/login", nil, nil)
	requireStatus(t, member.request("GET", "/settings", nil, nil), 303)
	signInTest(t, app, member, false)
	requireStatus(t, member.request("GET", "/settings", nil, nil), 403)
	requireStatus(t, member.post("/settings", url.Values{"mode": {"open"}}), 403)
}

func memberClient(t *testing.T, app *App, name string) *testClient {
	t.Helper()
	_, hash, err := app.store.Credentials(context.Background(), "alex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.CreateUser(context.Background(), name, hash); err != nil {
		t.Fatal(err)
	}
	client := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	client.request("GET", "/login", nil, nil)
	requireStatus(t, client.post("/login", url.Values{"username": {name}, "password": {"a long test password"}}), 303)
	return client
}

func selectMode(t *testing.T, owner *testClient, mode Mode) {
	t.Helper()
	requireStatus(t, owner.post("/settings", url.Values{"mode": {string(mode)}}), 303)
}

func TestModeAccessMatrixAndAudienceFiltering(t *testing.T) {
	app, owner := newTestApp(t, false)
	ownerID := signInTest(t, app, owner, true)
	member := memberClient(t, app, "jules")
	ctx := context.Background()
	create := func(title string, audience Audience) int64 {
		t.Helper()
		id, err := app.store.CreateTopic(ctx, 1, ownerID, title, "Message for "+title, audience)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	privateID := create("Family address", AudienceMembers)
	selectMode(t, owner, ModePersonal)
	personalID := create("My notebook", AudienceOwners)
	selectMode(t, owner, ModeOpen)
	publicID := create("Public picnic", AudiencePublic)
	if _, _, err := app.store.Reply(ctx, publicID, ownerID, "Everyone is welcome"); err != nil {
		t.Fatal(err)
	}
	guest := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	for _, path := range []string{"/", "/boards/1", "/recent", "/search?q=Message", fmt.Sprintf("/topics/%d", publicID)} {
		w := guest.request("GET", path, nil, nil)
		requireStatus(t, w, 200)
		if strings.Contains(w.Body.String(), "Family address") || strings.Contains(w.Body.String(), "My notebook") {
			t.Fatalf("private title leaked on %s", path)
		}
		if !strings.Contains(w.Body.String(), "Public picnic") {
			t.Fatalf("public conversation missing on %s", path)
		}
	}
	for _, id := range []int64{privateID, personalID} {
		requireStatus(t, guest.request("GET", fmt.Sprintf("/topics/%d", id), nil, nil), 404)
	}
	requireStatus(t, guest.post(fmt.Sprintf("/topics/%d/replies", publicID), url.Values{"body": {"Anonymous reply"}}), 303)
	requireStatus(t, guest.post("/boards/1/new", url.Values{"title": {"Anonymous topic"}, "body": {"Nope"}, "audience": {"public"}}), 303)
	w := guest.request("GET", fmt.Sprintf("/topics/%d", publicID), nil, nil)
	if strings.Contains(w.Body.String(), `name="body"`) || !strings.Contains(w.Body.String(), "Sign in to reply") {
		t.Fatal("guest reply UI does not match posting policy")
	}
	boards, err := app.store.Boards(ctx, nil)
	if err != nil || boards[0].Topics != 1 || boards[0].Posts != 2 || boards[0].LastTopicID != publicID {
		t.Fatalf("guest board aggregates: %+v %v", boards, err)
	}
	stats, err := app.store.Stats(ctx, nil)
	if err != nil || stats.Topics != 1 || stats.Posts != 2 {
		t.Fatalf("guest stats: %+v %v", stats, err)
	}
	posts, _, err := app.store.Posts(ctx, privateID, 20, 0, nil)
	if err != nil || len(posts) != 0 {
		t.Fatalf("private messages leaked: %+v %v", posts, err)
	}
	requireStatus(t, member.request("GET", fmt.Sprintf("/topics/%d", personalID), nil, nil), 404)
	requireStatus(t, member.post(fmt.Sprintf("/topics/%d/replies", personalID), url.Values{"body": {"Not allowed"}}), 404)
	for _, mode := range []Mode{ModePrivate, ModePersonal, ModeOpen} {
		selectMode(t, owner, mode)
		for _, path := range []string{"/boards/1", "/recent", "/search?q=picnic", fmt.Sprintf("/topics/%d", publicID)} {
			guestCode, memberCode := 303, 200
			if mode == ModeOpen {
				guestCode = 200
			}
			if mode == ModePersonal {
				memberCode = 403
			}
			requireStatus(t, guest.request("GET", path, nil, nil), guestCode)
			requireStatus(t, member.request("GET", path, nil, nil), memberCode)
			requireStatus(t, owner.request("GET", path, nil, nil), 200)
		}
	}
}

func TestJoiningAcrossModesAndExistingInvitations(t *testing.T) {
	app, owner := newTestApp(t, false)
	ownerID := signInTest(t, app, owner, true)
	member := memberClient(t, app, "jules")
	invite := randomToken()
	if err := app.store.Invite(context.Background(), ownerID, tokenHash(invite)); err != nil {
		t.Fatal(err)
	}
	guest := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	guest.request("GET", "/join", nil, nil)
	form := url.Values{"username": {"newfriend"}, "password": {"a long test password"}, "role": {"owner"}}
	requireStatus(t, guest.post("/join", form), 422)
	selectMode(t, owner, ModePersonal)
	requireStatus(t, guest.request("GET", "/join?invite="+invite, nil, nil), 403)
	form.Set("invite", invite)
	requireStatus(t, guest.post("/join", form), 403)
	requireStatus(t, owner.post("/invites", nil), 403)
	requireStatus(t, owner.request("GET", "/invites", nil, nil), 403)
	requireStatus(t, member.request("GET", "/", nil, nil), 403)
	requireStatus(t, member.post("/boards/1/new", url.Values{"title": {"Old session"}, "body": {"Nope"}, "audience": {"owners"}}), 403)
	requireStatus(t, member.post("/logout", nil), 303)
	requireStatus(t, member.post("/login", url.Values{"username": {"jules"}, "password": {"a long test password"}}), 403)
	selectMode(t, owner, ModePrivate)
	requireStatus(t, guest.post("/join", form), 303) // The invitation was not consumed while Personal.
	u, _, err := app.store.Credentials(context.Background(), "newfriend")
	if err != nil || u.Role != "member" {
		t.Fatalf("invited role: %+v %v", u, err)
	}
	selectMode(t, owner, ModeOpen)
	visitor := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	w := visitor.request("GET", "/join", nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `name="username"`) {
		t.Fatal("Open mode has no signup form")
	}
	form.Set("username", "visitor")
	form.Set("invite", invite)
	requireStatus(t, visitor.post("/join", form), 422) // A supplied, spent invite must not be ignored.
	form.Del("invite")
	requireStatus(t, visitor.post("/join", form), 303)
	u, _, err = app.store.Credentials(context.Background(), "visitor")
	if err != nil || u.Role != "member" {
		t.Fatalf("open signup role: %+v %v", u, err)
	}
	requireStatus(t, visitor.post("/settings", url.Values{"mode": {"personal"}}), 403)
}

func TestModeChangesRejectStaleComposersAndInvalidSettings(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	form := url.Values{"title": {"Keep this draft"}, "body": {"My private message"}, "audience": {"members"}}
	selectMode(t, owner, ModeOpen)
	w := owner.post("/boards/1/new", form)
	requireStatus(t, w, 422)
	if !strings.Contains(w.Body.String(), "My private message") || !strings.Contains(w.Body.String(), "Public") {
		t.Fatal("stale form did not preserve draft and explain the new audience")
	}
	stats, err := app.store.Stats(context.Background(), testReader)
	if err != nil || stats.Topics != 0 {
		t.Fatalf("stale form was published: %+v %v", stats, err)
	}
	form.Set("audience", "public")
	requireStatus(t, owner.post("/boards/1/new", form), 303)
	for _, value := range []string{"", "invalid", "OPEN", "open'; DELETE FROM users; --"} {
		requireStatus(t, owner.post("/settings", url.Values{"mode": {value}}), 422)
	}
	requireStatus(t, owner.request("POST", "/settings", url.Values{"mode": {"personal"}}, nil), 403)
	csrf := owner.cookies[app.cookieName("csrf")].Value
	requireStatus(t, owner.request("POST", "/settings", url.Values{"mode": {"personal"}, "csrf": {csrf}}, map[string]string{"Origin": "https://unrelated.example"}), 403)
	mode, err := app.store.Mode(context.Background())
	if err != nil || mode != ModeOpen {
		t.Fatalf("invalid settings changed policy: %s %v", mode, err)
	}
	w = owner.request("POST", "/settings", url.Values{"mode": {"private"}, "csrf": {csrf}}, map[string]string{"HX-Request": "true"})
	requireStatus(t, w, 200)
	if w.Header().Get("HX-Redirect") != "/settings?saved=1" {
		t.Fatal("settings did not redirect HTMX")
	}
	form.Set("audience", "public")
	requireStatus(t, owner.post("/boards/1/new", form), 422)
}

func TestOpenSignupRechecksPolicyAtWrite(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	selectMode(t, owner, ModeOpen)
	guest := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	guest.request("GET", "/join", nil, nil)
	for _, mode := range []Mode{ModePrivate, ModePersonal} {
		selectMode(t, owner, mode)
		// Model a request admitted while Open but delayed during password hashing.
		values := url.Values{"username": {"latevisitor"}, "password": {"a long test password"}}
		r := httptest.NewRequest("POST", "/join", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "127.0.0.1:1234"
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		r = r.WithContext(context.WithValue(r.Context(), stateKey{}, requestState{Mode: ModeOpen}))
		w := httptest.NewRecorder()
		app.join(w, r)
		if w.Code != 422 && w.Code != 403 {
			t.Fatalf("late signup status: %d", w.Code)
		}
		if _, _, err := app.store.Credentials(context.Background(), "latevisitor"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("late signup created an account: %v", err)
		}
	}
}
