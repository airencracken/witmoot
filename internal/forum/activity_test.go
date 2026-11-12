// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestActivityCountsAuthorizedOpensAndSuccessfulLogins(t *testing.T) {
	a, owner := newTestApp(t, false)
	id := signInTest(t, a, owner, true)
	topic, err := a.store.CreateTopic(t.Context(), 1, id, "Activity fixture", "first post", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.store.Reply(t.Context(), topic, id, "second post"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fmt.Sprintf("/topics/%d", topic), fmt.Sprintf("/topics/%d", topic), fmt.Sprintf("/topics/%04d", topic)} {
		if w := owner.request("GET", path, nil, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	owner.request("HEAD", fmt.Sprintf("/topics/%d", topic), nil, nil)
	owner.request("GET", "/topics/9999999", nil, nil)
	owner.request("GET", "/recent", nil, nil)
	result, err := a.store.Analytics(t.Context(), time.Now())
	if err != nil || result.Logins != 1 || result.Views != 2 {
		t.Fatal(result, err)
	}
	if err := a.startSessionForActivityTest(id); err != nil {
		t.Fatal(err)
	}
	result, err = a.store.Analytics(t.Context(), time.Now())
	if err != nil || result.Logins != 1 {
		t.Fatal("session rotation counted as login", result, err)
	}
	if w := owner.request("GET", "/admin/metrics", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "comfy-admin-dashboard") {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = a.store.db.Exec("UPDATE users SET suspended=1,role='member' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if w := owner.request("GET", "/admin/metrics?role=owner", nil, nil); w.Code == 200 || strings.Contains(w.Body.String(), "Recent sign-ins") {
		t.Fatal("suspended owner read metrics", w.Code)
	}
}
func (a *App) startSessionForActivityTest(id int64) error {
	return a.store.NewSession(context.Background(), "activity-rotation", id, time.Now().Add(time.Hour))
}
