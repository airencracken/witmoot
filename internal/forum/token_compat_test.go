// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"bufio"
	"context"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/token"
)

// storedToken is one line of testdata/token-digests.txt.
type storedToken struct{ purpose, secret, digest string }

// readStoredTokens reads tokens and digests made by the code Witmoot used
// before comfylib, so the digests are exactly what existing databases hold.
func readStoredTokens(t *testing.T) map[string]storedToken {
	t.Helper()
	file, err := os.Open("testdata/token-digests.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, file)
	tokens := map[string]storedToken{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) != 3 {
			t.Fatalf("malformed fixture line %q", scanner.Text())
		}
		tokens[fields[0]] = storedToken{fields[0], fields[1], fields[2]}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 3 {
		t.Fatalf("fixture holds %d tokens", len(tokens))
	}
	return tokens
}

func TestStoredTokenDigestsStillMatch(t *testing.T) {
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, stored := range readStoredTokens(t) {
		if got := token.Hash(stored.secret); got != stored.digest {
			t.Errorf("%s: digest %s, but the database holds %s", stored.purpose, got, stored.digest)
		}
		if !validToken(stored.secret) || !hex64.MatchString(stored.secret) || !hex64.MatchString(stored.digest) {
			t.Errorf("%s: fixture is not in the stored format", stored.purpose)
		}
	}
	// New tokens keep the shape the routes and the database expect.
	for range 100 {
		secret, digest := token.New()
		if !validToken(secret) || !hex64.MatchString(secret) || digest != token.Hash(secret) || !hex64.MatchString(digest) {
			t.Fatalf("new token %q with digest %q", secret, digest)
		}
	}
}

// A session, a reset link and an invitation issued before the switch, stored
// only as their digests, keep working through the routes.
func TestTokensIssuedBeforeComfylibStillWork(t *testing.T) {
	stored := readStoredTokens(t)
	app, owner := newTestApp(t, false)
	ownerID := signInTest(t, app, owner, true)
	ctx := context.Background()

	session := &testClient{app: app, cookies: map[string]*http.Cookie{}}
	if err := app.store.NewSession(ctx, stored["session"].digest, ownerID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	session.cookies[app.cookieName("session")] = &http.Cookie{Name: app.cookieName("session"), Value: stored["session"].secret}
	w := session.request("GET", "/account", nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "alex") {
		t.Fatal("the stored session did not sign in")
	}

	memberID := testMember(t, app.store, "jules")
	if err := app.store.CreateAuthToken(ctx, memberID, TokenPasswordReset, stored["reset"].digest, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	member := &testClient{app: app, cookies: map[string]*http.Cookie{}}
	w = member.request("GET", "/reset/"+stored["reset"].secret, nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `name="confirm_password"`) {
		t.Fatal("the stored reset link did not open")
	}
	requireStatus(t, member.post("/reset/"+stored["reset"].secret, url.Values{"password": {"a long enough password"}, "confirm_password": {"a long enough password"}}), 303)

	if err := testInvite(app.store, ownerID, stored["invitation"].digest); err != nil {
		t.Fatal(err)
	}
	guest := &testClient{app: app, cookies: map[string]*http.Cookie{}}
	guest.request("GET", "/join", nil, nil)
	requireStatus(t, guest.post("/join", url.Values{"username": {"sam"}, "password": {"a long test password"}, "invite": {stored["invitation"].secret}}), 303)
	if user, _, err := app.store.Credentials(ctx, "sam"); err != nil || user.InvitedBy != ownerID {
		t.Fatalf("the stored invitation did not admit its guest: %+v %v", user, err)
	}
}
