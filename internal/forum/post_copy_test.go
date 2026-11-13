package forum

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostBodyKeepsStoredAndAuthoredText(t *testing.T) {
	for _, tc := range []struct {
		body         string
		removed      bool
		author, want string
	}{
		{removedMessage, true, "jules", "This post was removed by a site owner."},
		{deletedMessage, false, "Former member", "This post was deleted by its author."},
		{deletedMessage, false, "jules", deletedMessage},
		{"An ordinary message about email", false, "jules", "An ordinary message about email"},
		{`<img src=x onerror=alert(1)>`, false, "jules", `<img src=x onerror=alert(1)>`},
	} {
		if got := postBody(tc.body, tc.removed, tc.author); got != tc.want {
			t.Fatalf("postBody(%q): %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestPostTerminologyAcrossRoutes(t *testing.T) {
	a, owner := newTestApp(t, false)
	ownerID := signInTest(t, a, owner, true)
	topic, err := a.store.CreateTopic(t.Context(), 1, ownerID, "Post wording", "Test body", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	var post int64
	if err := a.store.db.QueryRow("SELECT id FROM posts WHERE topic_id=?", topic).Scan(&post); err != nil {
		t.Fatal(err)
	}
	for route, want := range map[string]string{
		"/recent":                             "Recent posts",
		"/search":                             "Search conversations and posts",
		"/boards/1/new":                       "Your post",
		fmt.Sprintf("/posts/%d/edit", post):   "Edit your post",
		fmt.Sprintf("/posts/%d/remove", post): "Remove post",
		"/admin/metrics":                      "New posts",
	} {
		w := owner.request("GET", route, nil, nil)
		requireStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s is missing %q", route, want)
		}
		for _, wrong := range []string{"Recent messages", "Your message", "Edit message", "Remove message", "New messages"} {
			if strings.Contains(w.Body.String(), wrong) {
				t.Errorf("%s still says %q", route, wrong)
			}
		}
	}
}
