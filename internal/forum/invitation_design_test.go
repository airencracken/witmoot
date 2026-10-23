// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"html"
	"net/url"
	"strings"
	"testing"
)

func TestInvitationPageSchemaAndEscaping(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	w := owner.request("GET", "/invites", nil, nil)
	requireStatus(t, w, 200)
	assertInvitationPageSchema(t, w.Body.String(), true)
	if !strings.Contains(w.Body.String(), "No invitations yet.") {
		t.Fatal("empty list has no explanation")
	}
	for _, label := range []string{"For Sam", strings.Repeat("w", 64), `<script>alert("invite")</script>`, `Quotes " & < >`} {
		w = owner.post("/invites", url.Values{"label": {label}, "max_uses": {"2"}, "expires_days": {"7"}})
		requireStatus(t, w, 200)
		assertInvitationPageSchema(t, w.Body.String(), true)
		if !strings.Contains(w.Body.String(), "<h3>"+html.EscapeString(label)+"</h3>") || !strings.Contains(w.Body.String(), "/revoke") {
			t.Fatal("record lost its escaped label or revoke action")
		}
	}
	before, _, err := a.store.Invitations(t.Context(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	w = owner.post("/invites", url.Values{"label": {"Keep my draft"}, "max_uses": {"-1"}})
	requireStatus(t, w, 422)
	assertInvitationPageSchema(t, w.Body.String(), true)
	if !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), `value="Keep my draft"`) {
		t.Fatal("validation lost the error or draft")
	}
	after, _, err := a.store.Invitations(t.Context(), 100, 0)
	if err != nil || len(before) != len(after) {
		t.Fatal("invalid form changed invitations", err)
	}
}

func TestAccountPagesUseCompactForms(t *testing.T) {
	_, guest := newTestApp(t, false)
	for _, path := range []string{"/login", "/join"} {
		w := guest.request("GET", path, nil, nil)
		requireStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), `class="paper narrow account-card account-page"`) || !strings.Contains(w.Body.String(), `autocomplete="username"`) {
			t.Fatalf("%s lost its compact account form", path)
		}
	}
}
