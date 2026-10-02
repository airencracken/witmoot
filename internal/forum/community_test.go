package forum

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func signedInCommunityMember(t *testing.T, a *App) (*testClient, int64) {
	t.Helper()
	hash, err := HashPassword("a long test password")
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.store.CreateUser(context.Background(), "jules", hash)
	if err != nil {
		t.Fatal(err)
	}
	c := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	c.request("GET", "/login", nil, nil)
	requireStatus(t, c.post("/login", url.Values{"username": {"jules"}, "password": {"a long test password"}}), 303)
	return c, id
}

func TestCommunityRoutesRequireOwnerAndCSRF(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	member, id := signedInCommunityMember(t, a)
	topic, err := a.store.CreateTopic(context.Background(), 1, id, "Hello everyone", "A message", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	var post int64
	if err := a.store.db.QueryRow("SELECT id FROM posts WHERE topic_id = ?", topic).Scan(&post); err != nil {
		t.Fatal(err)
	}
	outsider := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	outsider.request("GET", "/", nil, nil)
	paths := []string{fmt.Sprintf("/members/%d/suspend", id), fmt.Sprintf("/members/%d/restore", id), fmt.Sprintf("/posts/%d/remove", post)}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			requireStatus(t, member.request("GET", path, nil, nil), 403)
			requireStatus(t, member.post(path, url.Values{"revision": {"0"}, "confirmation": {"jules"}}), 403)
			requireStatus(t, outsider.request("GET", path, nil, nil), 303)
			requireStatus(t, outsider.post(path, nil), 303)
			requireStatus(t, owner.request("POST", path, url.Values{"revision": {"0"}}, nil), 403)
			requireStatus(t, owner.request("POST", path, url.Values{"csrf": {owner.cookies[a.cookieName("csrf")].Value}}, map[string]string{"Origin": "https://other.example", "Sec-Fetch-Site": "cross-site"}), 403)
			requireStatus(t, owner.request("PUT", path, nil, nil), 403)
			requireStatus(t, owner.request("PUT", path, url.Values{"csrf": {owner.cookies[a.cookieName("csrf")].Value}}, nil), 405)
		})
	}
	for _, c := range []*testClient{member, outsider} {
		requireStatus(t, c.request("GET", "/moderation", nil, nil), map[bool]int{true: 403, false: 303}[c == member])
	}
	w := member.request("GET", fmt.Sprintf("/topics/%d", topic), nil, nil)
	if strings.Contains(w.Body.String(), "/remove\"") {
		t.Fatal("member saw owner message controls")
	}
	requireStatus(t, owner.request("GET", fmt.Sprintf("/members/%d/suspend", id), nil, nil), 200)
	u, err := a.store.UserByID(context.Background(), id)
	if err != nil || u.Suspended {
		t.Fatal("GET changed member access")
	}
}

func TestSuspensionAndRestorationHTTP(t *testing.T) {
	a, owner := newTestApp(t, false)
	ownerID := signInTest(t, a, owner, true)
	member, id := signedInCommunityMember(t, a)
	ctx := context.Background()
	oldSession := member.cookies[a.cookieName("session")].Value
	token := randomToken()
	if err := a.store.CreateAuthToken(ctx, id, TokenPasswordReset, tokenHash(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/members/%d/suspend", id)
	for _, revision := range []string{"", "-1", "x", "9223372036854775808"} {
		requireStatus(t, owner.post(path, url.Values{"revision": {revision}, "confirmation": {"jules"}}), 400)
	}
	requireStatus(t, owner.post(path, url.Values{"revision": {"0"}, "confirmation": {"wrong"}}), 422)
	requireStatus(t, owner.post(path, url.Values{"revision": {"9"}, "confirmation": {"jules"}}), 409)
	w := owner.post(path, url.Values{"revision": {"0"}, "confirmation": {"jules"}})
	requireStatus(t, w, 303)
	if w.Header().Get("Location") != "/members?saved=suspend" {
		t.Fatal("missing suspension redirect")
	}
	requireStatus(t, member.request("GET", "/account", nil, nil), 303)
	requireStatus(t, member.post("/login", url.Values{"username": {"jules"}, "password": {"wrong"}}), 422)
	w = member.post("/login", url.Values{"username": {"jules"}, "password": {"a long test password"}})
	requireStatus(t, w, 403)
	if !strings.Contains(w.Body.String(), "suspended") || !strings.Contains(w.Body.String(), "House rules &amp; owners") {
		t.Fatal("suspended person cannot find owner details")
	}
	requireStatus(t, member.request("GET", "/reset/"+token, nil, nil), 400)
	requireStatus(t, owner.post(fmt.Sprintf("/members/%d/reset", id), nil), 403)
	requireStatus(t, owner.request("GET", fmt.Sprintf("/members/%d/suspend", ownerID), nil, nil), 403)
	w = owner.request("GET", "/members", nil, nil)
	if !strings.Contains(w.Body.String(), "suspended") || !strings.Contains(w.Body.String(), fmt.Sprintf("/members/%d/restore", id)) {
		t.Fatal("suspension not shown")
	}
	path = fmt.Sprintf("/members/%d/restore", id)
	requireStatus(t, owner.post(path, url.Values{"revision": {"1"}, "confirmation": {"jules"}}), 303)
	if session, err := a.store.Session(ctx, tokenHash(oldSession)); err != nil || session != nil {
		t.Fatal("old session returned")
	}
	requireStatus(t, member.post("/login", url.Values{"username": {"jules"}, "password": {"a long test password"}}), 303)
	w = owner.request("GET", "/moderation", nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Suspended jules") || !strings.Contains(w.Body.String(), "Restored access for jules") {
		t.Fatal("missing owner activity")
	}
}

func TestRemovalHTTPConfirmationHTMXAndExport(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	member, id := signedInCommunityMember(t, a)
	topic, err := a.store.CreateTopic(context.Background(), 1, id, "An unfortunate title", "Erase this unique text", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	var post int64
	if err := a.store.db.QueryRow("SELECT id FROM posts WHERE topic_id = ?", topic).Scan(&post); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/posts/%d/remove", post)
	w := owner.request("GET", path, nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Erase this unique text") {
		t.Fatal("confirmation omitted message preview")
	}
	requireStatus(t, owner.post(path, url.Values{"revision": {"0"}}), 422)
	requireStatus(t, owner.post(path, url.Values{"revision": {"bad"}, "confirmation": {"1"}}), 400)
	requireStatus(t, owner.post(path, url.Values{"revision": {"8"}, "confirmation": {"1"}}), 409)
	w = owner.request("POST", path, url.Values{"revision": {"0"}, "confirmation": {"1"}, "replace_title": {"1"}, "csrf": {owner.cookies[a.cookieName("csrf")].Value}}, map[string]string{"HX-Request": "true"})
	requireStatus(t, w, 200)
	if w.Header().Get("HX-Redirect") != fmt.Sprintf("/topics/%d?page=1#post-%d", topic, post) {
		t.Fatal("HTMX did not navigate to removed message")
	}
	w = member.request("GET", fmt.Sprintf("/topics/%d", topic), nil, nil)
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "Erase this unique text") || strings.Contains(w.Body.String(), "An unfortunate title") || !strings.Contains(w.Body.String(), removedMessage) {
		t.Fatal("removed content still visible")
	}
	requireStatus(t, member.request("GET", fmt.Sprintf("/posts/%d/edit", post), nil, nil), 404)
	w = member.request("GET", "/account/export", nil, nil)
	requireStatus(t, w, 200)
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		closeTest(t, r)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte("Erase this unique text")) || bytes.Contains(content, []byte("An unfortunate title")) {
			t.Fatalf("export retained erased content in %s", file.Name)
		}
	}
}

func TestAboutPublishesRulesAndOwnersWithoutPrivateAccountDetails(t *testing.T) {
	a, owner := newTestApp(t, false)
	ownerID := signInTest(t, a, owner, true)
	if err := a.store.SetEmail(context.Background(), ownerID, "private@example.org"); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"mode": {"private"}, "site_name": {"Our table"}, "source_url": {""}, "welcome_title": {"Welcome"}, "welcome_text": {"A place for friends"}, "house_rules": {"Ask before sharing.\n<script>alert(1)</script>"}, "owner_contact": {"Find alex at https://example.org/contact"}}
	requireStatus(t, owner.post("/settings", form), 303)
	outsider := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	for _, mode := range []Mode{ModePersonal, ModePrivate, ModeOpen} {
		if err := a.store.SetMode(context.Background(), mode); err != nil {
			t.Fatal(err)
		}
		w := outsider.request("GET", "/about", nil, nil)
		requireStatus(t, w, 200)
		body := w.Body.String()
		for _, want := range []string{"Ask before sharing.", "&lt;script&gt;", "<strong>alex</strong>", "https://example.org/contact"} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q", want)
			}
		}
		for _, unwanted := range []string{"private@example.org", "<script>alert", "password_hash", "witmoot-account-export"} {
			if strings.Contains(body, unwanted) {
				t.Fatalf("private/unsafe data published: %s", unwanted)
			}
		}
		if strings.Contains(body, "Edit rules and contact details") {
			t.Fatal("outsider saw edit controls")
		}
	}
	form.Set("house_rules", strings.Repeat("a", 5001))
	w := owner.post("/settings", form)
	requireStatus(t, w, 422)
	if !strings.Contains(w.Body.String(), strings.Repeat("a", 5001)) {
		t.Fatal("validation discarded rules draft")
	}
	brand, err := a.store.LoadBranding(context.Background(), SiteBranding{})
	if err != nil || brand.HouseRules != "Ask before sharing.\n<script>alert(1)</script>" {
		t.Fatal("invalid rules saved")
	}
}

func TestCommunityTextLimitsAndAdversarialInput(t *testing.T) {
	for _, tc := range []struct {
		name, rules, contact string
		ok                   bool
	}{
		{"blank", "", "", true}, {"unicode boundary", strings.Repeat("界", 5000), strings.Repeat("界", 500), true},
		{"rules too long", strings.Repeat("a", 5001), "", false}, {"contact too long", "", strings.Repeat("a", 501), false},
		{"null", "hello\x00world", "", false}, {"control", "", "bad\x1bcontact", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cleanBranding(SiteBranding{Name: "Our board", HouseRules: tc.rules, OwnerContact: tc.contact})
			if (err == nil) != tc.ok {
				t.Fatalf("valid=%v err=%v", tc.ok, err)
			}
		})
	}
}
