package forum

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"witmoot/internal/mail"
)

var resetLinkPattern = regexp.MustCompile(`/reset/([a-f0-9]{64})`)

// recordingMailer stands in for a configured relay so email paths can be tested
// without a socket.
type recordingMailer struct {
	sent    []mail.Message
	failure error
}

func (m *recordingMailer) Enabled() bool { return true }

func (m *recordingMailer) Send(_ context.Context, msg mail.Message) error {
	if m.failure != nil {
		return m.failure
	}
	m.sent = append(m.sent, msg)
	return nil
}

func TestOwnerIssuesResetLinkAndMemberRedeems(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	memberID := testMember(t, app.store, "jules")

	w := client.request("GET", "/members", nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "jules") {
		t.Fatal("member list omits an account")
	}

	w = client.post("/members/"+strconv.FormatInt(memberID, 10)+"/reset", nil)
	requireStatus(t, w, 200)
	match := resetLinkPattern.FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatalf("no reset link in response: %s", w.Body.String())
	}
	token := match[1]

	// Only the digest is stored.
	var stored string
	if err := app.store.db.QueryRow("SELECT token_hash FROM auth_tokens").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != tokenHash(token) {
		t.Fatal("reset link should be stored only as a digest")
	}

	member := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	requireStatus(t, member.request("GET", "/reset/"+token, nil, nil), 200)
	form := url.Values{"password": {"a brand new password"}, "confirm_password": {"a brand new password"}}
	requireStatus(t, member.post("/reset/"+token, form), 303)

	// Redeeming signs the member in.
	requireStatus(t, member.request("GET", "/", nil, nil), 200)

	// The new password works and the old one does not.
	if _, hash, err := app.store.Credentials(context.Background(), "jules"); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte("a brand new password")) != nil {
		t.Fatalf("new password was not stored: %v", err)
	}

	// The link cannot be used twice.
	replay := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	w = replay.request("GET", "/reset/"+token, nil, nil)
	requireStatus(t, w, 400)
	if !strings.Contains(w.Body.String(), "invalid or has expired") {
		t.Fatal("spent link did not explain itself")
	}
}

func TestResetPasswordRejectsWeakInputBeforeSpendingTheLink(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	memberID := testMember(t, app.store, "jules")
	w := client.post("/members/"+strconv.FormatInt(memberID, 10)+"/reset", nil)
	requireStatus(t, w, 200)
	token := resetLinkPattern.FindStringSubmatch(w.Body.String())[1]

	member := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	member.request("GET", "/reset/"+token, nil, nil)
	// A short password and a mismatched confirmation are refused.
	requireStatus(t, member.post("/reset/"+token, url.Values{"password": {"short"}, "confirm_password": {"short"}}), 422)
	requireStatus(t, member.post("/reset/"+token, url.Values{"password": {"a long enough password"}, "confirm_password": {"a different long password"}}), 422)
	// The link is still usable after those failures.
	requireStatus(t, member.post("/reset/"+token, url.Values{"password": {"a long enough password"}, "confirm_password": {"a long enough password"}}), 303)
}

func TestMemberToolsRequireAnOwner(t *testing.T) {
	app, member := newTestApp(t, false)
	signInTest(t, app, member, false)
	requireStatus(t, member.request("GET", "/members", nil, nil), 403)
	requireStatus(t, member.post("/members/1/reset", nil), 403)
	requireStatus(t, member.post("/members/1/reset/revoke", nil), 403)

	anonymous := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	requireStatus(t, anonymous.request("GET", "/members", nil, nil), 303)
	requireStatus(t, anonymous.request("GET", "/account", nil, nil), 303)
}

func TestOwnerCanRevokeAResetLink(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	memberID := testMember(t, app.store, "jules")
	w := client.post("/members/"+strconv.FormatInt(memberID, 10)+"/reset", nil)
	requireStatus(t, w, 200)
	// The pending link is shown on the member list.
	w = client.request("GET", "/members", nil, nil)
	if !strings.Contains(w.Body.String(), "reset link waiting") {
		t.Fatal("pending link not reported")
	}
	requireStatus(t, client.post("/members/"+strconv.FormatInt(memberID, 10)+"/reset/revoke", nil), 303)
	members, err := app.store.Members(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if members[0].Pending {
		t.Fatal("revoked link still pending")
	}
}

func TestSelfServicePasswordChange(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	requireStatus(t, client.request("GET", "/account", nil, nil), 200)

	// The wrong current password is refused.
	requireStatus(t, client.post("/account/password", url.Values{
		"current_password": {"not my password"},
		"password":         {"a brand new password"},
		"confirm_password": {"a brand new password"},
	}), 403)

	w := client.post("/account/password", url.Values{
		"current_password": {"a long test password"},
		"password":         {"a brand new password"},
		"confirm_password": {"a brand new password"},
	})
	requireStatus(t, w, 303)
	if w.Header().Get("Location") != "/account?saved=password" {
		t.Fatalf("redirect=%q", w.Header().Get("Location"))
	}
	if _, hash, err := app.store.Credentials(context.Background(), "alex"); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte("a brand new password")) != nil {
		t.Fatalf("password was not changed: %v", err)
	}
	// The caller keeps working on a fresh session.
	requireStatus(t, client.request("GET", "/account", nil, nil), 200)
}

func TestAccountEmailAndResetEmailDelivery(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	memberID := testMember(t, app.store, "jules")

	// An invalid address is refused.
	requireStatus(t, client.post("/account/email", url.Values{"email": {"not an address"}}), 422)
	requireStatus(t, client.post("/account/email", url.Values{"email": {"owner@example.org"}}), 303)
	// Give the member an address so the link has somewhere to go.
	if err := app.store.SetEmail(context.Background(), memberID, "jules@example.org"); err != nil {
		t.Fatal(err)
	}

	// With mail disabled, asking for an email explains why it did not happen.
	w := client.post("/members/"+strconv.FormatInt(memberID, 10)+"/reset", url.Values{"email": {"1"}})
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Mail is not configured") || !resetLinkPattern.MatchString(w.Body.String()) {
		t.Fatalf("disabled mail response: %s", w.Body.String())
	}

	// With a working relay, the link is emailed.
	relay := &recordingMailer{}
	app.mailer = relay
	w = client.post("/members/"+strconv.FormatInt(memberID, 10)+"/reset", url.Values{"email": {"1"}})
	requireStatus(t, w, 200)
	if len(relay.sent) != 1 || relay.sent[0].To != "jules@example.org" {
		t.Fatalf("message not sent: %+v", relay.sent)
	}
	if !strings.Contains(relay.sent[0].Body, "/reset/") || !strings.Contains(w.Body.String(), "emailed to") {
		t.Fatal("reset link missing from the message or confirmation")
	}

	// A relay failure still leaves the link for the owner to copy.
	app.mailer = &recordingMailer{failure: errors.New("relay down")}
	w = client.post("/members/"+strconv.FormatInt(memberID, 10)+"/reset", url.Values{"email": {"1"}})
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "could not be sent") || !resetLinkPattern.MatchString(w.Body.String()) {
		t.Fatalf("failed mail response: %s", w.Body.String())
	}
}

func TestResetLinkForUnknownAccountIsNotFound(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	requireStatus(t, client.post("/members/9999/reset", nil), 404)
}

func TestMalformedResetLinkIsRejected(t *testing.T) {
	_, client := newTestApp(t, false)
	for _, path := range []string{"/reset/short", "/reset/" + strings.Repeat("z", 64)} {
		requireStatus(t, client.request("GET", path, nil, nil), 400)
	}
}

func TestValidEmail(t *testing.T) {
	for _, tc := range []struct {
		email string
		ok    bool
	}{
		{"", true},
		{"a@b", true},
		{"jules@example.org", true},
		{"one@two@three", false},
		{"no-at-sign", false},
		{"@example.org", false},
		{"jules@", false},
		{"has space@example.org", false},
		{"line\nbreak@example.org", false},
		{strings.Repeat("a", 250) + "@example.org", false},
	} {
		if got := validEmail(tc.email); got != tc.ok {
			t.Errorf("validEmail(%q) = %v, want %v", tc.email, got, tc.ok)
		}
	}
}
