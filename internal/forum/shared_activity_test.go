// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"net/http/httptest"
	"testing"
)

// Shared sign-ins start sessions for active accounts only, and record no
// per-member activity.
func TestSharedSignInRecordsNoMemberActivity(t *testing.T) {
	a, _ := newTestApp(t, false)
	id := testMember(t, a.store, "sharedmember")
	request := httptest.NewRequest("GET", "http://example.test/auth/shared/callback", nil)
	if _, err := a.sharedSignIn(httptest.NewRecorder(), request, id); err != nil {
		t.Fatal(err)
	}
	if n := activityRows(t, a.store); n != 0 {
		t.Fatalf("shared sign-in stored %d activity rows", n)
	}
	if _, err := a.store.AnalyticsDB().Exec("UPDATE users SET suspended=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	if _, err := a.sharedSignIn(response, request, id); err == nil {
		t.Fatal("inactive account signed in")
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("inactive account received a session")
	}
}
