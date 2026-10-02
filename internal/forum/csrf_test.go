package forum

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/airencracken/comfylib/token"
)

func csrfCookie(t *testing.T, c *testClient) string {
	t.Helper()
	cookie := c.cookies[c.app.cookieName("csrf")]
	if cookie == nil {
		t.Fatal("no CSRF cookie was issued")
	}
	return cookie.Value
}

func userEmail(t *testing.T, a *App, name string) string {
	t.Helper()
	user, _, err := a.store.Credentials(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return user.Email
}

// A signed-in token belongs to its session: another member's token is refused,
// even when it is planted as the cookie too, and the refusal changes nothing.
func TestCSRFTokenIsBoundToTheSession(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	member := memberClient(t, app, "jules")
	for _, c := range []*testClient{owner, member} {
		session := c.cookies[app.cookieName("session")].Value
		if got := csrfCookie(t, c); got != sessionCSRFToken(session) {
			t.Fatalf("signed-in CSRF cookie %q is not derived from the session", got)
		}
	}
	ownerToken := csrfCookie(t, owner)
	form := url.Values{"email": {"stolen@example.com"}, "csrf": {ownerToken}}
	requireStatus(t, member.request("POST", "/account/email", form, nil), 403)
	member.cookies[app.cookieName("csrf")] = &http.Cookie{Name: app.cookieName("csrf"), Value: ownerToken}
	requireStatus(t, member.request("POST", "/account/email", form, nil), 403)
	if email := userEmail(t, app, "jules"); email != "" {
		t.Fatalf("a refused form changed the email to %q", email)
	}
	requireStatus(t, member.post("/account/email", url.Values{"email": {"jules@example.com"}}), 303)
	if email := userEmail(t, app, "jules"); email != "jules@example.com" {
		t.Fatalf("the member's own token did not work: %q", email)
	}
}

// A cookie planted by a neighbouring host, or left from before sign-in, does
// not stand in for the session's token, and is replaced on the next response.
func TestPlantedCSRFCookieIsRefusedWhenSignedIn(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	planted := testToken()
	client.cookies[app.cookieName("csrf")] = &http.Cookie{Name: app.cookieName("csrf"), Value: planted}
	w := client.request("POST", "/invites", url.Values{"csrf": {planted}, "uses": {"1"}, "days": {"7"}}, nil)
	requireStatus(t, w, 403)
	var invites int
	if err := app.store.db.QueryRow("SELECT count(*) FROM invitations").Scan(&invites); err != nil || invites != 0 {
		t.Fatalf("planted token created an invitation: %d %v", invites, err)
	}
	session := client.cookies[app.cookieName("session")].Value
	if got := csrfCookie(t, client); got != sessionCSRFToken(session) {
		t.Fatalf("planted cookie was not replaced: %q", got)
	}
}

// Every state-changing method needs the token, not only POST. With the token,
// the request reaches the router, which refuses methods it does not serve.
func TestEveryStateChangingMethodNeedsTheToken(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	anonymous := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	anonymous.request("GET", "/login", nil, nil)
	for _, c := range []*testClient{owner, anonymous} {
		token := csrfCookie(t, c)
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			for _, path := range []string{"/settings", "/topics/1"} {
				requireStatus(t, c.request(method, path, url.Values{}, nil), 403)
				requireStatus(t, c.request(method, path, nil, nil), 403)
				requireStatus(t, c.request(method, path, nil, map[string]string{"X-CSRF-Token": testToken()}), 403)
				w := c.request(method, path, nil, map[string]string{"X-CSRF-Token": token})
				if w.Code == 403 {
					t.Errorf("%s %s with a valid header token: 403", method, path)
				}
				if method != "DELETE" {
					if w := c.request(method, path, url.Values{"csrf": {token}}, nil); w.Code == 403 {
						t.Errorf("%s %s with a valid form token: 403", method, path)
					}
				}
			}
		}
	}
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "TRACE"} {
		if isMutating(method) {
			t.Errorf("%s is treated as state-changing", method)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "CONNECT", "PROPFIND", ""} {
		if !isMutating(method) {
			t.Errorf("%s is not checked", method)
		}
	}
}

// Signed-out forms, sign-in above all, keep the double-submit cookie.
func TestSignedOutFormsKeepDoubleSubmit(t *testing.T) {
	app, client := newTestApp(t, false)
	hash, err := HashPassword("a long test password")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.CreateOwner(context.Background(), "alex", hash); err != nil {
		t.Fatal(err)
	}
	client.request("GET", "/login", nil, nil)
	token := csrfCookie(t, client)
	if w := client.request("GET", "/login", nil, nil); len(w.Result().Cookies()) != 0 {
		t.Fatalf("a valid signed-out cookie was reissued: %v", w.Result().Cookies())
	}
	login := func(csrf string) url.Values {
		return url.Values{"username": {"alex"}, "password": {"a long test password"}, "csrf": {csrf}}
	}
	requireStatus(t, client.request("POST", "/login", login(""), nil), 403)
	requireStatus(t, client.request("POST", "/login", login(testToken()), nil), 403)
	requireStatus(t, client.request("POST", "/login?csrf="+token, url.Values{"username": {"alex"}, "password": {"a long test password"}}, nil), 403)
	requireStatus(t, client.request("POST", "/login", url.Values{"username": {"alex"}, "password": {"a long test password"}, "csrf": {testToken(), token}}, nil), 403)
	w := client.request("POST", "/login", login(token), nil)
	requireStatus(t, w, 303)
	session := client.cookies[app.cookieName("session")].Value
	if got := csrfCookie(t, client); got != sessionCSRFToken(session) || got == token {
		t.Fatalf("sign-in did not bind the token to the new session: %q", got)
	}
	requireStatus(t, client.post("/logout", nil), 303)
	if got := csrfCookie(t, client); got == sessionCSRFToken(session) || !validToken(got) {
		t.Fatalf("sign-out kept the session's token: %q", got)
	}
}

// orderedMultipart posts fields in the given order, then an optional file.
func orderedMultipart(t *testing.T, c *testClient, path string, fields [][2]string, file []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	if file != nil {
		part, err := writer.CreateFormFile("avatar", "avatar.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.RemoteAddr = "127.0.0.1:1234"
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	c.app.ServeHTTP(w, r)
	return w
}

// Multipart forms work with and without JavaScript when the token is their
// first field, as the templates write it; elsewhere it is not looked for.
func TestMultipartFormsCarryTheTokenInTheirFirstPart(t *testing.T) {
	app, client := newTestApp(t, false)
	userID := signInTest(t, app, client, true)
	token := csrfCookie(t, client)
	htmx := map[string]string{"HX-Request": "true"}
	topic := func(title string) [][2]string {
		return [][2]string{{"title", title}, {"body", "Bring a dish to share"}, {"audience", "members"}}
	}
	withToken := func(value string, fields [][2]string) [][2]string {
		return append([][2]string{{"csrf", value}}, fields...)
	}
	for _, tc := range []struct {
		name, title string
		fields      [][2]string
		headers     map[string]string
		status      int
	}{
		{"without JavaScript", "Picnic without scripts", withToken(token, topic("Picnic without scripts")), nil, 303},
		{"with htmx", "Picnic with htmx", withToken(token, topic("Picnic with htmx")), htmx, 200},
		{"with the header", "Picnic by header", topic("Picnic by header"), map[string]string{"X-CSRF-Token": token}, 303},
		{"token last", "Picnic token last", append(topic("Picnic token last"), [2]string{"csrf", token}), nil, 403},
		{"token twice", "Picnic token twice", withToken(testToken(), withToken(token, topic("Picnic token twice"))), nil, 403},
		{"wrong token", "Picnic wrong token", withToken(testToken(), topic("Picnic wrong token")), nil, 403},
		{"empty token", "Picnic empty token", withToken("", topic("Picnic empty token")), nil, 403},
		{"uppercase token", "Picnic uppercase", withToken(strings.ToUpper(token), topic("Picnic uppercase")), nil, 403},
		{"oversized token", "Picnic long token", withToken(token+strings.Repeat("0", 1<<12), topic("Picnic long token")), nil, 403},
		{"no token", "Picnic no token", topic("Picnic no token"), nil, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := orderedMultipart(t, client, "/boards/1/new", tc.fields, nil, tc.headers)
			requireStatus(t, w, tc.status)
			var topics int
			if err := app.store.db.QueryRow("SELECT count(*) FROM topics WHERE title = ?", tc.title).Scan(&topics); err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{true: 0, false: 1}[tc.status == 403]; topics != want {
				t.Fatalf("%d topics titled %q, want %d", topics, tc.title, want)
			}
			if tc.headers["HX-Request"] == "true" && !strings.HasPrefix(w.Header().Get("HX-Redirect"), "/topics/") {
				t.Fatalf("htmx form did not redirect: %v", w.Header())
			}
		})
	}
	// A file upload, with and without htmx, and refused without its token.
	picture := pngAvatarFixture(t, 64, 64)
	requireStatus(t, orderedMultipart(t, client, "/account/avatar", nil, picture, nil), 403)
	if has, err := app.store.HasAvatar(context.Background(), userID); err != nil || has {
		t.Fatalf("a refused upload was stored: %v %v", has, err)
	}
	requireStatus(t, orderedMultipart(t, client, "/account/avatar", [][2]string{{"csrf", token}}, picture, nil), 303)
	requireStatus(t, orderedMultipart(t, client, "/account/avatar", [][2]string{{"csrf", token}}, picture, htmx), 200)
	if has, err := app.store.HasAvatar(context.Background(), userID); err != nil || !has {
		t.Fatalf("the upload was not stored: %v %v", has, err)
	}
}

// countingReader records how much of a request body was read.
type countingReader struct {
	r    io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

// A multipart request is refused after reading no more than a bounded first
// part, however large the upload behind it, so a stranger cannot make the
// server spool an upload it will throw away.
func TestMultipartTokenCheckReadsOnlyTheFirstPart(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	large := strings.Repeat("x", 4<<20)
	for name, body := range map[string]string{
		"file first":      "--b\r\nContent-Disposition: form-data; name=\"images\"; filename=\"a.png\"\r\n\r\n" + large + "\r\n--b--\r\n",
		"field first":     "--b\r\nContent-Disposition: form-data; name=\"body\"\r\n\r\n" + large + "\r\n--b--\r\n",
		"wrong token":     "--b\r\nContent-Disposition: form-data; name=\"csrf\"\r\n\r\n" + testToken() + "\r\n--b\r\nContent-Disposition: form-data; name=\"images\"; filename=\"a.png\"\r\n\r\n" + large + "\r\n--b--\r\n",
		"endless token":   "--b\r\nContent-Disposition: form-data; name=\"csrf\"\r\n\r\n" + large + "\r\n--b--\r\n",
		"long preamble":   large + "\r\n--b\r\nContent-Disposition: form-data; name=\"csrf\"\r\n\r\n" + csrfCookie(t, client) + "\r\n--b--\r\n",
		"endless headers": "--b\r\nX-Padding: " + large + "\r\nContent-Disposition: form-data; name=\"csrf\"\r\n\r\n" + csrfCookie(t, client) + "\r\n--b--\r\n",
		"no boundary":     large,
	} {
		t.Run(name, func(t *testing.T) {
			counter := &countingReader{r: strings.NewReader(body)}
			r := httptest.NewRequest("POST", "/account/avatar", counter)
			r.Header.Set("Content-Type", "multipart/form-data; boundary=b")
			r.RemoteAddr = "127.0.0.1:1234"
			for _, cookie := range client.cookies {
				r.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			requireStatus(t, w, 403)
			if counter.read > csrfPeekLimit+4096 {
				t.Fatalf("read %d bytes of a refused upload, want at most %d", counter.read, csrfPeekLimit+4096)
			}
		})
	}
}

// Finding the token leaves the body exactly as it arrived for the handler.
func TestFirstPartTokenRestoresTheBody(t *testing.T) {
	check := func(token string, fields map[string]string, preamble []byte) bool {
		var body bytes.Buffer
		body.Write(preamble)
		body.WriteString("\r\n")
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("csrf", token); err != nil {
			return false
		}
		for key, value := range fields {
			if err := writer.WriteField(key, value); err != nil {
				return false
			}
		}
		if err := writer.Close(); err != nil {
			return false
		}
		original := bytes.Clone(body.Bytes())
		r := httptest.NewRequest("POST", "/", bytes.NewReader(original))
		r.Header.Set("Content-Type", writer.FormDataContentType())
		got, err := firstPartToken(r)
		if err != nil {
			return false
		}
		rest, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(rest, original) {
			return false
		}
		want := token
		if len(want) > 128 {
			want = want[:128]
		}
		// A token pushed past the peek window by its preamble is not found,
		// which refuses the request rather than misreading it.
		return got == want || got == "" && len(preamble) > csrfPeekLimit/2
	}
	if err := quick.Check(check, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// Property: a token derived from one session never validates for another.
func TestSessionTokensNeverValidateAcrossSessions(t *testing.T) {
	derive := func(a, b [32]byte) bool {
		x, y := hex.EncodeToString(a[:]), hex.EncodeToString(b[:])
		tx := sessionCSRFToken(x)
		if !validToken(tx) || tx == x || tx == token.Hash(x) || tx != sessionCSRFToken(x) {
			return false
		}
		return x == y || tx != sessionCSRFToken(y)
	}
	if err := quick.Check(derive, nil); err != nil {
		t.Fatal(err)
	}

	app, client := newTestApp(t, false)
	ownerID := signInTest(t, app, client, true)
	ctx := context.Background()
	request := func(session, token string) int {
		r := httptest.NewRequest("PUT", "/settings", strings.NewReader(url.Values{"csrf": {token}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: app.cookieName("session"), Value: session})
		r.AddCookie(&http.Cookie{Name: app.cookieName("csrf"), Value: token})
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code
	}
	across := func(a, b [32]byte) bool {
		x, y := hex.EncodeToString(a[:]), hex.EncodeToString(b[:])
		if x == y {
			return true
		}
		for _, session := range []string{x, y} {
			if err := app.store.NewSession(ctx, token.Hash(session), ownerID, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
		// The router refuses PUT, so 405 means the token was accepted.
		return request(x, sessionCSRFToken(y)) == 403 && request(y, sessionCSRFToken(x)) == 403 &&
			request(x, sessionCSRFToken(x)) == 405 && request(y, sessionCSRFToken(y)) == 405
	}
	if err := quick.Check(across, &quick.Config{MaxCount: 50}); err != nil {
		t.Fatal(err)
	}
}

// Every uploading form in the templates puts its token first, which is the
// only place the middleware looks for it in a multipart body.
func TestMultipartTemplatesPutTheTokenFirst(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	form := regexp.MustCompile(`<form[^>]*enctype="multipart/form-data"[^>]*>\s*(.{0,40})`)
	found := 0
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range form.FindAllStringSubmatch(string(text), -1) {
			found++
			if !strings.HasPrefix(match[1], `{{template "csrf" .}}`) && !strings.HasPrefix(match[1], `<input type="hidden" name="csrf"`) {
				t.Errorf("%s: an uploading form does not start with its CSRF token: %s", file, match[0])
			}
		}
	}
	if found < 4 {
		t.Fatalf("found %d uploading forms, want at least 4", found)
	}
}
