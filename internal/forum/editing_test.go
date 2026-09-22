package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestAuthorCanEditWithTimestampEscapingAndAttachmentsPreserved(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	c := sessionClient(t, f.app, f.writer)
	path := fmt.Sprintf("/posts/%d/edit", f.postID)
	topic := fmt.Sprintf("/topics/%d", f.topicID)
	requireStatus(t, c.request("GET", path, nil, nil), 200)
	unchanged := c.post(path, url.Values{"body": {"Original message"}, "revision": {"0"}})
	requireStatus(t, unchanged, 303)
	if strings.Contains(c.request("GET", topic, nil, nil).Body.String(), "class=\"edited\"") {
		t.Fatal("unchanged message marked edited")
	}
	body := "A correction\n<script>alert('plain text')</script>"
	w := c.post(path, url.Values{"body": {body}, "revision": {"0"}})
	requireStatus(t, w, 303)
	if want := fmt.Sprintf("/topics/%d?page=1#post-%d", f.topicID, f.postID); w.Header().Get("Location") != want {
		t.Fatalf("redirect: %s", w.Header().Get("Location"))
	}
	location, _, _ := strings.Cut(w.Header().Get("Location"), "#")
	rendered := c.request("GET", location, nil, nil)
	requireStatus(t, rendered, 200)
	if !strings.Contains(rendered.Body.String(), "class=\"edited\">Edited <time datetime=") || !strings.Contains(rendered.Body.String(), "&lt;script&gt;") || strings.Contains(rendered.Body.String(), "<script>alert") {
		t.Fatalf("edited text/timestamp not safely rendered: %s", rendered.Body.String())
	}
	p, err := readPost(ctx, f.app.store.db, f.postID, f.owner)
	if err != nil || p.Body != body || p.EditedAt <= 0 || p.Revision != 1 {
		t.Fatalf("edit not persisted: %+v %v", p, err)
	}
	if _, err := f.app.store.Attachment(ctx, f.imageID, f.writer); err != nil {
		t.Fatalf("edit lost image: %v", err)
	}
	w = c.post(path, url.Values{"body": {"A stale replacement"}, "revision": {"0"}})
	requireStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), "A stale replacement") || !strings.Contains(w.Body.String(), "has changed since") {
		t.Fatal("conflict discarded attempted text or explanation")
	}
	p, err = readPost(ctx, f.app.store.db, f.postID, f.owner)
	if err != nil || p.Body != body || p.Revision != 1 {
		t.Fatalf("stale save overwrote edit: %+v %v", p, err)
	}
}

func TestEditsRequireAuthorCSRFAndValidBody(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	path := fmt.Sprintf("/posts/%d/edit", f.postID)
	for _, user := range []*User{f.owner, f.reader, f.outsider} {
		c := sessionClient(t, f.app, user)
		requireStatus(t, c.request("GET", path, nil, nil), 404)
		requireStatus(t, c.post(path, url.Values{"body": {"Not yours"}, "revision": {"0"}}), 404)
		if _, err := f.app.store.EditPost(ctx, f.postID, user.ID, "Not yours", 0); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("non-author edit: %v", err)
		}
	}
	c := sessionClient(t, f.app, f.writer)
	requireStatus(t, c.request("POST", path, url.Values{"body": {"No CSRF"}, "revision": {"0"}}, nil), 403)
	for _, body := range []string{"", " \n ", strings.Repeat("界", 20001), "has\x00null", string([]byte{0xff})} {
		if _, err := f.app.store.EditPost(ctx, f.postID, f.writer.ID, body, 0); !errors.Is(err, errPostBody) {
			t.Fatalf("invalid body accepted: %v", err)
		}
	}
	requireStatus(t, c.post(path, url.Values{"body": {"  "}, "revision": {"0"}}), 422)
	requireStatus(t, c.post(path, url.Values{"body": {"Valid"}, "revision": {"nonsense"}}), 400)
	p, err := readPost(ctx, f.app.store.db, f.postID, f.owner)
	if err != nil || p.Body != "Original message" || p.EditedAt != 0 {
		t.Fatalf("failed edit changed content: %+v %v", p, err)
	}
	if _, err := f.app.store.EditPost(ctx, f.postID, f.writer.ID, strings.Repeat("界", 20000), 0); err != nil {
		t.Fatalf("valid Unicode boundary rejected: %v", err)
	}
}

func TestConcurrentEditsHaveOneWinner(t *testing.T) {
	f := privateBoardFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, body := range []string{"First correction", "Other correction"} {
		wg.Go(func() {
			_, err := f.app.store.EditPost(context.Background(), f.postID, f.writer.ID, body, 0)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, errEditConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
}

func TestEditRedirectsToThePostsPageWithoutBumpingConversation(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	id := f.postID
	for i := 0; i < 20; i++ {
		var err error
		id, _, err = f.app.store.Reply(ctx, f.topicID, f.writer.ID, "Another message")
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.app.store.db.Exec("UPDATE topics SET updated_at = 1 WHERE id = ?", f.topicID); err != nil {
		t.Fatal(err)
	}
	c := sessionClient(t, f.app, f.writer)
	w := c.post(fmt.Sprintf("/posts/%d/edit", id), url.Values{"body": {"A correction on page two"}, "revision": {"0"}})
	requireStatus(t, w, 303)
	want := fmt.Sprintf("/topics/%d?page=2#post-%d", f.topicID, id)
	if w.Header().Get("Location") != want {
		t.Fatalf("redirect=%s want=%s", w.Header().Get("Location"), want)
	}
	topic, err := f.app.store.Topic(ctx, f.topicID, f.writer)
	if err != nil || topic.UpdatedAt != 1 {
		t.Fatalf("edit bumped topic: %+v %v", topic, err)
	}
}
