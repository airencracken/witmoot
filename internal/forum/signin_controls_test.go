package forum

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSignInControlsDescribeTheirAction(t *testing.T) {
	_, client := newTestApp(t, false)
	landing := client.request(http.MethodGet, "/", nil, nil)
	requireStatus(t, landing, http.StatusOK)
	link := regexp.MustCompile(`<a[^>]+href="/login"[^>]*>Sign in(?:\s|<)`)
	if !link.MatchString(landing.Body.String()) {
		t.Fatal("landing page has no clearly labelled sign-in link")
	}
	login := client.request(http.MethodGet, "/login", nil, nil)
	requireStatus(t, login, http.StatusOK)
	button := regexp.MustCompile(`<button[^>]+type="submit"[^>]*>Sign in</button>`)
	if !button.MatchString(login.Body.String()) {
		t.Fatal("login form has no clearly labelled sign-in submit control")
	}
}

// Repeated controls on the member and invitation pages name the person or
// invitation they act on, so a screen reader list of controls is usable.
func TestRepeatedControlsNameWhoTheyAffect(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	ctx := context.Background()
	for _, name := range []string{"jules", "sam"} {
		id := testMember(t, app.store, name)
		if err := app.store.SaveAvatar(ctx, id, pngAvatarFixture(t, 32, 32)); err != nil {
			t.Fatal(err)
		}
		if err := app.store.SetEmail(ctx, id, name+"@example.org"); err != nil {
			t.Fatal(err)
		}
		if err := app.store.CreateAuthToken(ctx, id, TokenPasswordReset, name+"-reset", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := app.store.SetInvitePermission(ctx, id, name == "jules"); err != nil {
			t.Fatal(err)
		}
	}
	app.mailer = &recordingMailer{}
	for _, label := range []string{"", "For Sam"} {
		requireStatus(t, owner.post("/invites", url.Values{"label": {label}, "max_uses": {"1"}, "expires_days": {"7"}}), http.StatusOK)
	}
	control := regexp.MustCompile(`<(?:button|a)\b[^>]*>([^<]+)</(?:button|a)>`)
	label := regexp.MustCompile(`aria-label="([^"]+)"`)
	for _, page := range []string{"/members", "/invites"} {
		body := owner.request(http.MethodGet, page, nil, nil).Body.String()
		names := map[string]string{}
		for _, text := range []string{"Email a reset link", "Create a reset link", "Cancel reset link", "Remove avatar", "Suspend member", "Revoke invitation", "Remove invite access", "Allow this member to invite"} {
			for _, match := range control.FindAllStringSubmatch(body, -1) {
				if strings.TrimSpace(match[1]) != text {
					continue
				}
				name := label.FindStringSubmatch(match[0])
				if name == nil || !strings.HasPrefix(name[1], text) || name[1] == text {
					t.Errorf("%s: %q has no specific accessible name: %s", page, text, match[0])
					continue
				}
				if previous, seen := names[name[1]]; seen {
					t.Errorf("%s: %q and %q share the accessible name %q", page, previous, text, name[1])
				}
				names[name[1]] = text
			}
		}
		if len(names) < 4 {
			t.Fatalf("%s: found only %d named controls: %v", page, len(names), names)
		}
	}
	members := owner.request(http.MethodGet, "/members", nil, nil).Body.String()
	for _, want := range []string{`aria-label="Suspend member jules"`, `aria-label="Remove avatar for sam"`, `aria-label="Create a reset link for jules"`} {
		if !strings.Contains(members, want) {
			t.Errorf("members page is missing %s", want)
		}
	}
	invites := owner.request(http.MethodGet, "/invites", nil, nil).Body.String()
	for _, want := range []string{`aria-label="Revoke invitation For Sam"`, `aria-label="Remove invite access for jules"`, `aria-label="Allow this member to invite: sam"`} {
		if !strings.Contains(invites, want) {
			t.Errorf("invites page is missing %s", want)
		}
	}
	jules, _, err := app.store.Credentials(ctx, "jules")
	if err != nil {
		t.Fatal(err)
	}
	w := owner.post(fmt.Sprintf("/members/%d/reset", jules.ID), nil)
	if !strings.Contains(w.Body.String(), "expires in "+HumanDuration(ResetLinkTTL)+".") {
		t.Fatal("the stated reset link lifetime does not come from ResetLinkTTL")
	}
}
