package forum

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

type testClient struct {
	app     *App
	cookies map[string]*http.Cookie
}

func newTestApp(t *testing.T, secure bool) (*App, *testClient) {
	t.Helper()
	app, err := New(testStore(t), Config{Name: "Our little board", SecureCookies: secure})
	if err != nil {
		t.Fatal(err)
	}
	return app, &testClient{app: app, cookies: make(map[string]*http.Cookie)}
}

func (c *testClient) request(method, path string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	body := ""
	if form != nil {
		body = form.Encode()
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	c.app.ServeHTTP(w, r)
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	return w
}

func (c *testClient) post(path string, form url.Values) *httptest.ResponseRecorder {
	if form == nil {
		form = make(url.Values)
	}
	if cookie := c.cookies[c.app.cookieName("csrf")]; cookie != nil {
		form.Set("csrf", cookie.Value)
	}
	return c.request("POST", path, form, nil)
}

func signInTest(t *testing.T, app *App, client *testClient, owner bool) int64 {
	t.Helper()
	hash, err := HashPassword("a long test password")
	if err != nil {
		t.Fatal(err)
	}
	if owner {
		err = app.store.CreateOwner(context.Background(), "alex", hash)
	} else {
		_, err = app.store.CreateUser(context.Background(), "alex", hash)
	}
	if err != nil {
		t.Fatal(err)
	}
	client.request("GET", "/login", nil, nil)
	w := client.post("/login", url.Values{"username": {"alex"}, "password": {"a long test password"}})
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	user, _, err := app.store.Credentials(context.Background(), "alex")
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func requireStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status=%d want=%d body=%s", w.Code, code, w.Body.String())
	}
}

func TestPrivateRoutesAndPublicLanding(t *testing.T) {
	app, client := newTestApp(t, false)
	user := testMember(t, app.store, "alex")
	if _, err := app.store.CreateTopic(context.Background(), 1, user, "Secret family plans", "Secret address"); err != nil {
		t.Fatal(err)
	}
	w := client.request("GET", "/", nil, nil)
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "Secret") || strings.Contains(w.Body.String(), "The kitchen table") {
		t.Fatal("private data on landing page")
	}
	for _, path := range []string{"/boards/1", "/boards/1/new", "/topics/1", "/recent", "/search?q=Secret", "/invites"} {
		t.Run(path, func(t *testing.T) {
			w := client.request("GET", path, nil, nil)
			requireStatus(t, w, 303)
			if w.Header().Get("Location") != "/login" || strings.Contains(w.Body.String(), "Secret") {
				t.Fatal("private route not protected")
			}
		})
	}
	for _, path := range []string{"/boards/1/new", "/topics/1/replies", "/invites"} {
		requireStatus(t, client.post(path, url.Values{"title": {"Intruder"}, "body": {"Nope"}}), 303)
	}
	w = client.request("GET", "/topics/1", nil, map[string]string{"HX-Request": "true"})
	if w.Header().Get("HX-Redirect") != "/login" {
		t.Fatal("HTMX must redirect to login")
	}
}

func TestInvitationsEndToEnd(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	w := owner.post("/invites", nil)
	requireStatus(t, w, 200)
	match := regexp.MustCompile(`/join\?invite=([a-f0-9]{64})`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatal("no invitation link")
	}
	invite := match[1]
	var stored string
	if err := app.store.db.QueryRow("SELECT token_hash FROM invitations").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == invite || stored != tokenHash(invite) {
		t.Fatal("invitation should be stored only as a hash")
	}
	friend := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	requireStatus(t, friend.request("GET", "/join?invite="+invite, nil, nil), 200)
	form := url.Values{"username": {"jules"}, "password": {"another long password"}, "invite": {invite}, "role": {"owner"}}
	requireStatus(t, friend.post("/join", form), 303)
	user, _, err := app.store.Credentials(context.Background(), "jules")
	if err != nil || user.Role != "member" {
		t.Fatalf("role escalation: %+v %v", user, err)
	}
	requireStatus(t, friend.request("GET", "/", nil, nil), 200)
	requireStatus(t, friend.request("GET", "/invites", nil, nil), 403)
	requireStatus(t, friend.post("/invites", nil), 403)
	outsider := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	outsider.request("GET", "/join", nil, nil)
	form.Set("username", "outsider")
	requireStatus(t, outsider.post("/join", form), 422)
	form.Set("invite", "")
	requireStatus(t, outsider.post("/join", form), 422)
}

func TestPostingSearchEscapingAndHTMX(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	for _, path := range []string{"/", "/boards/1", "/boards/1/new", "/recent", "/search", "/search?q=recipe"} {
		requireStatus(t, client.request("GET", path, nil, nil), 200)
	}
	title := `Dinner <script>alert("x")</script>`
	body := "First line\nSecond line <img src=x onerror=alert(1)>"
	form := url.Values{"title": {title}, "body": {body}, "csrf": {client.cookies[app.cookieName("csrf")].Value}}
	w := client.request("POST", "/boards/1/new", form, map[string]string{"HX-Request": "true"})
	requireStatus(t, w, 200)
	path := w.Header().Get("HX-Redirect")
	if path != "/topics/1" {
		t.Fatalf("wrong HTMX redirect: %q", path)
	}
	w = client.request("GET", path, nil, nil)
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), `<script>alert`) || strings.Contains(w.Body.String(), `<img src=x`) || !strings.Contains(w.Body.String(), "&lt;img") {
		t.Fatal("user content is not escaped")
	}
	w = client.post(path+"/replies", url.Values{"body": {"I will bring the soup."}})
	requireStatus(t, w, 303)
	if w.Header().Get("Location") != "/topics/1?page=1#post-2" {
		t.Fatalf("reply redirect=%q", w.Header().Get("Location"))
	}
	w = client.request("GET", "/search?q=soup", nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Dinner &lt;script&gt;") {
		t.Fatal("reply text not searchable")
	}
	w = client.post("/boards/1/new", url.Values{"title": {"x"}, "body": {"Keep my draft"}})
	requireStatus(t, w, 422)
	if !strings.Contains(w.Body.String(), "Keep my draft") {
		t.Fatal("validation lost draft")
	}
	requireStatus(t, client.post("/topics/1/replies", url.Values{"body": {" \n "}}), 422)
	requireStatus(t, client.post("/topics/1/replies", url.Values{"body": {strings.Repeat("a", 20001)}}), 422)
	stats, err := app.store.Stats(context.Background())
	if err != nil || stats.Topics != 1 || stats.Posts != 2 {
		t.Fatalf("invalid posts persisted: %+v %v", stats, err)
	}
}

func TestCSRFAndCrossOriginProtection(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	for _, path := range []string{"/login", "/join", "/logout", "/invites", "/boards/1/new", "/topics/1/replies"} {
		t.Run(path, func(t *testing.T) {
			requireStatus(t, client.request("POST", path, url.Values{}, nil), 403)
			requireStatus(t, client.request("POST", path+"?csrf="+client.cookies[app.cookieName("csrf")].Value, url.Values{}, nil), 403)
		})
	}
	form := url.Values{"csrf": {client.cookies[app.cookieName("csrf")].Value}}
	requireStatus(t, client.request("POST", "/invites", form, map[string]string{"Origin": "https://evil.example"}), 403)
	requireStatus(t, client.request("POST", "/invites", form, map[string]string{"Sec-Fetch-Site": "cross-site"}), 403)
	var invites int
	if err := app.store.db.QueryRow("SELECT count(*) FROM invitations").Scan(&invites); err != nil || invites != 0 {
		t.Fatalf("CSRF side effect: %d %v", invites, err)
	}
}

func TestCookieSecurityAndLogout(t *testing.T) {
	app, client := newTestApp(t, true)
	signInTest(t, app, client, false)
	session := client.cookies["__Host-witmoot_session"]
	if session == nil || !session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" || session.Domain != "" {
		t.Fatalf("insecure session cookie: %+v", session)
	}
	var stored string
	if err := app.store.db.QueryRow("SELECT token_hash FROM sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != tokenHash(session.Value) {
		t.Fatal("raw session stored")
	}
	w := client.request("GET", "/", nil, nil)
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") == "" || !strings.Contains(w.Body.String(), `hx-history="false"`) {
		t.Fatal("private pages can be cached or lack CSP")
	}
	requireStatus(t, client.post("/logout", nil), 303)
	client.cookies[session.Name] = session // Replaying the old cookie must fail.
	w = client.request("GET", "/boards/1", nil, nil)
	requireStatus(t, w, 303)
}

func TestBadRoutesMethodsAndInput(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	for _, path := range []string{"/missing", "/boards/no", "/boards/999", "/topics/-1", "/topics/999", "/boards/999/new"} {
		requireStatus(t, client.request("GET", path, nil, nil), 404)
	}
	for _, path := range []string{"/boards/1?page=0", "/recent?page=99999999999999999999", "/search?q=" + strings.Repeat("a", 101)} {
		requireStatus(t, client.request("GET", path, nil, nil), 400)
	}
	requireStatus(t, client.request("GET", "/logout", nil, nil), 405)
	requireStatus(t, client.post("/boards/999/new", url.Values{"title": {"Hello there"}, "body": {"Hi"}}), 404)
	requireStatus(t, client.post("/topics/999/replies", url.Values{"body": {"Hi"}}), 404)
	requireStatus(t, client.post("/boards/1/new", url.Values{"title": {"Hello there"}, "body": {strings.Repeat("x", 257<<10)}}), 400)
	for _, path := range []string{"/static/htmx.min.js", "/static/app.js", "/static/style.css", "/healthz"} {
		requireStatus(t, client.request("GET", path, nil, nil), 200)
	}
}

func TestPasswordValidation(t *testing.T) {
	for _, tc := range []struct {
		name, password string
		valid          bool
	}{
		{"alex", "twelve chars long", true}, {"Alex_24", strings.Repeat("a", 72), true}, {"ab", "a long enough password", false}, {"a<script>", "a long enough password", false}, {"alex", "short", false}, {"alex", strings.Repeat("a", 73), false}, {"alex", strings.Repeat("界", 25), false}, {"alex", strings.Repeat("界", 12), true},
	} {
		if got := ValidateCredentials(tc.name, tc.password) == ""; got != tc.valid {
			t.Errorf("validation for %q length %d: %v", tc.name, len(tc.password), got)
		}
	}
}

func TestLoginFailureAndRateLimiting(t *testing.T) {
	app, client := newTestApp(t, false)
	client.request("GET", "/login", nil, nil)
	w := client.post("/login", url.Values{"username": {"nobody"}, "password": {"incorrect"}})
	requireStatus(t, w, 422)
	if !strings.Contains(w.Body.String(), "did not match") {
		t.Fatal("missing login error")
	}
	app.limiter.mu.Lock()
	app.limiter.entries["127.0.0.1"] = rateEntry{Count: 20, Until: time.Now().Add(time.Minute)}
	app.limiter.mu.Unlock()
	w = client.post("/login", url.Values{"username": {"nobody"}, "password": {"incorrect"}})
	requireStatus(t, w, 429)
	if w.Header().Get("Retry-After") != "900" {
		t.Fatal("missing retry delay")
	}
}

func TestReplyRedirectAtPageBoundary(t *testing.T) {
	app, client := newTestApp(t, false)
	user := signInTest(t, app, client, false)
	id, err := app.store.CreateTopic(context.Background(), 1, user, "A long conversation", "First")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < pageSize; i++ {
		if _, _, err := app.store.Reply(context.Background(), id, user, "More"); err != nil {
			t.Fatal(err)
		}
	}
	w := client.post(fmt.Sprintf("/topics/%d/replies", id), url.Values{"body": {"A fresh page"}})
	requireStatus(t, w, 303)
	if !strings.Contains(w.Header().Get("Location"), "?page=2#post-") {
		t.Fatal("reply did not open its page")
	}
	w = client.request("GET", fmt.Sprintf("/topics/%d?page=2", id), nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "A fresh page") || !strings.Contains(w.Body.String(), "Previous page") {
		t.Fatal("missing page content or navigation")
	}
}

func TestUnicodeMessageAtCharacterLimit(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	// URL encoding expands each three-byte character to nine bytes. The request
	// limit must still admit a message at the advertised 20,000-character limit.
	w := client.post("/boards/1/new", url.Values{"title": {"A message in any language"}, "body": {strings.Repeat("界", 20000)}})
	requireStatus(t, w, 303)
}
