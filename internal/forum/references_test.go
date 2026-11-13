// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"fmt"
	"github.com/airencracken/comfylib/reference"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"witmoot/internal/imvault"
)

func TestDiscussionHandoffOnlyPreparesDrafts(t *testing.T) {
	a, c := newTestApp(t, false)
	id := signInTest(t, a, c, true)
	source := "https://music.example/recommendations/1"
	link, err := reference.Handoff("https://board.example", reference.Draft{Source: source, Title: "Our album", Body: "Title by artist\nhttps://provider.example/album"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := reference.URL(link)
	path := "/share?" + u.RawQuery
	w := c.request("GET", path, nil, nil)
	requireStatus(t, w, 200)
	w = c.request("GET", path+"&board=1", nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), source) || !strings.Contains(w.Body.String(), "Who can read this") {
		t.Fatal(w.Body.String())
	}
	topics, _, err := a.store.Topics(t.Context(), 1, "", 50, 0, &User{ID: id, Role: "owner"})
	if err != nil || len(topics) != 0 {
		t.Fatal("GET created thread", topics, err)
	}
	topic, err := a.store.CreateTopic(t.Context(), 1, id, "Existing", "Old words", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	w = c.request("GET", path+fmt.Sprintf("&topic=%d", topic), nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), source) {
		t.Fatal(w.Body.String())
	}
	posts, _, _ := a.store.Posts(t.Context(), topic, 50, 0, &User{ID: id, Role: "owner"})
	if len(posts) != 1 {
		t.Fatal("GET created reply")
	}
	w = c.request("GET", path+"&source=https%3A%2F%2Fevil.example", nil, nil)
	requireStatus(t, w, 400)
	w = c.request("GET", "/share?source=javascript%3Aalert(1)&title=Bad&body=x", nil, nil)
	requireStatus(t, w, 400)
}
func TestAlbumPreviewRequiresVisiblePostAndExactReference(t *testing.T) {
	a, c := newTestApp(t, false)
	id := signInTest(t, a, c, true)
	topic, err := a.store.CreateTopic(t.Context(), 1, id, "Album thread", "https://vault.example/a/summer", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	posts, _, _ := a.store.Posts(t.Context(), topic, 50, 0, &User{ID: id, Role: "owner"})
	path := fmt.Sprintf("/posts/%d/album-preview?url=https%%3A%%2F%%2Fvault.example%%2Fa%%2Fsummer", posts[0].ID)
	w := c.request("GET", path, nil, nil)
	requireStatus(t, w, 404)
	anon := &testClient{app: a, cookies: map[string]*http.Cookie{}}
	w = anon.request("GET", path, nil, nil)
	if w.Code == 200 {
		t.Fatal("anonymous preview")
	}
	w = c.request("GET", fmt.Sprintf("/topics/%d", topic), nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "https://vault.example/a/summer") {
		t.Fatal("outage removed text")
	}
}

func TestAlbumPreviewFollowsCurrentRemotePermissions(t *testing.T) {
	a, c := newTestApp(t, false)
	id := signInTest(t, a, c, true)
	a.config.ImageKey = []byte("01234567890123456789012345678901")
	var err error
	a.vault, err = imvault.New("https://vault.example")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := a.encryptImageToken(id, "key")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.ConnectImages(t.Context(), id, Connection{Server: a.vault.Base, Username: "owner", Token: encrypted}); err != nil {
		t.Fatal(err)
	}
	mode := "public"
	calls := 0
	a.vault.HTTP.Transport = previewTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/albums/summer/preview" || r.Header.Get("Authorization") != "Bearer key" {
			t.Fatal(r.URL, r.Header)
		}
		w := httptest.NewRecorder()
		if mode == "offline" {
			w.WriteHeader(503)
		} else {
			fmt.Fprintf(w, `{"title":"<script>title</script>","slug":"summer","visibility":%q,"image_count":2}`, mode)
		}
		return w.Result(), nil
	})
	topic, err := a.store.CreateTopic(t.Context(), 1, id, "Album", "https://vault.example/a/summer", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	posts, _, _ := a.store.Posts(t.Context(), topic, 50, 0, &User{ID: id, Role: "owner"})
	w := c.request("GET", fmt.Sprintf("/topics/%d", topic), nil, nil)
	requireStatus(t, w, 200)
	if calls != 0 || !strings.Contains(w.Body.String(), "View current public album preview") {
		t.Fatal("topic read fetched remote", calls)
	}
	path := fmt.Sprintf("/posts/%d/album-preview?url=https%%3A%%2F%%2Fvault.example%%2Fa%%2Fsummer", posts[0].ID)
	w = c.request("GET", path, nil, nil)
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "<script>title") || !strings.Contains(w.Body.String(), "2 publicly visible images") {
		t.Fatal(w.Body.String())
	}
	for _, private := range []string{"private", "members", "offline"} {
		mode = private
		w = c.request("GET", path, nil, nil)
		requireStatus(t, w, 404)
		if strings.Contains(w.Body.String(), "title</script>") {
			t.Fatal("concealed album exposed")
		}
	}
	mode = "public"
	w = c.request("GET", path+"-other", nil, nil)
	requireStatus(t, w, 404)
	if err = a.store.DisconnectImages(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	w = c.request("GET", path, nil, nil)
	requireStatus(t, w, 404)
	w = c.request("GET", fmt.Sprintf("/topics/%d", topic), nil, nil)
	requireStatus(t, w, 200)
}

type previewTransport func(*http.Request) (*http.Response, error)

func (f previewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDiscussionReturnAfterExplicitPostAndConfiguredSource(t *testing.T) {
	t.Setenv("WITMOOT_APPS", `[{"id":"songstead","url":"https://music.example"}]`)
	a, c := newTestApp(t, false)
	signInTest(t, a, c, true)
	a.config.BaseURL = "https://board.example"
	source := "https://music.example/recommendations/8"
	callback := source + "/discussion-return?state=" + strings.Repeat("a", 64)
	query := url.Values{"source": {source}, "title": {"An album"}, "body": {"A listening link"}, "return_to": {callback}, "board": {"1"}}
	w := c.request("GET", "/share?"+query.Encode(), nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `name="return_to"`) || !strings.Contains(w.Body.String(), "Who can read this") {
		t.Fatal("return draft lost confirmation", w.Body.String())
	}
	w = c.post("/boards/1/new", url.Values{"title": {"An album"}, "body": {"A listening link"}, "audience": {"members"}, "return_to": {callback}})
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Your message is posted") || !strings.Contains(w.Body.String(), "thread=https%3A%2F%2Fboard.example%2Ftopics%2F") {
		t.Fatal("missing reviewed return", w.Body.String())
	}
	query.Set("return_to", "https://evil.example/discussion-return?state="+strings.Repeat("a", 64))
	w = c.request("GET", "/share?"+query.Encode(), nil, nil)
	requireStatus(t, w, 400)
	// Forging a posted hidden field never produces an external redirect.
	w = c.post("/boards/1/new", url.Values{"title": {"Another album"}, "body": {"Words"}, "audience": {"members"}, "return_to": {"https://evil.example/discussion-return"}})
	if w.Code != 303 || strings.HasPrefix(w.Header().Get("Location"), "https:") {
		t.Fatal("external return accepted", w.Code, w.Header())
	}
}

// The share page offers recent conversations by name, limited to those the
// member can read and reply to, and a chosen one is still checked on the server.
func TestSharePickerOffersOnlyReplyableConversations(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := t.Context()
	open, err := f.app.store.CreateTopic(ctx, 1, f.owner.ID, `Picnic <plans> & more`, "Shared words", AudiencePublic)
	if err != nil {
		t.Fatal(err)
	}
	source := "https://music.example/recommendations/3"
	link, err := reference.Handoff("https://board.example", reference.Draft{Source: source, Title: "A song", Body: "Listen"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := reference.URL(link)
	path := "/share?" + u.RawQuery
	option := func(id int64) string { return fmt.Sprintf(`<option value="%d">`, id) }
	for name, c := range map[string]struct {
		user      *User
		see, miss []int64
	}{
		"writer":   {f.writer, []int64{open, f.topicID}, nil},
		"reader":   {f.reader, []int64{open}, []int64{f.topicID}},
		"outsider": {f.outsider, []int64{open}, []int64{f.topicID}},
	} {
		client := sessionClient(t, f.app, c.user)
		w := client.request("GET", path, nil, nil)
		requireStatus(t, w, 200)
		body := w.Body.String()
		if strings.Contains(body, "topic ID") || strings.Contains(body, `inputmode="numeric"`) {
			t.Fatalf("%s still sees the numeric conversation field", name)
		}
		at := strings.Index(body, `<select id="share-topic" name="topic">`)
		if at < 0 {
			t.Fatalf("%s has no conversation picker", name)
		}
		body = body[at:]
		body = body[:strings.Index(body, "</select>")]
		for _, id := range c.see {
			if !strings.Contains(body, option(id)) {
				t.Errorf("%s cannot pick conversation %d", name, id)
			}
		}
		for _, id := range c.miss {
			if strings.Contains(body, option(id)) {
				t.Errorf("%s can pick conversation %d", name, id)
			}
		}
		if strings.Contains(body, "Secret party plans") && name == "outsider" {
			t.Fatal("hidden conversation title leaked into the picker")
		}
		if !strings.Contains(body, "Picnic &lt;plans&gt; &amp; more") {
			t.Fatalf("%s: picker title not escaped", name)
		}
	}
	outsider := sessionClient(t, f.app, f.outsider)
	requireStatus(t, outsider.request("GET", path+fmt.Sprintf("&topic=%d", f.topicID), nil, nil), 404)
	for _, bad := range []string{"0", "-1", "abc", "1e3", "9223372036854775808", "<script>"} {
		requireStatus(t, outsider.request("GET", path+"&topic="+url.QueryEscape(bad), nil, nil), 400)
	}
	requireStatus(t, outsider.request("GET", path+fmt.Sprintf("&topic=%d&board=1", open), nil, nil), 400)
}

func TestSharePickerIsLimitedToRecentConversations(t *testing.T) {
	a, c := newTestApp(t, false)
	id := signInTest(t, a, c, true)
	for i := range shareChoices + 5 {
		if _, err := a.store.CreateTopic(t.Context(), 1, id, fmt.Sprintf("Conversation %02d", i), "Words", AudienceMembers); err != nil {
			t.Fatal(err)
		}
	}
	w := c.request("GET", "/share?source="+url.QueryEscape("https://music.example/recommendations/1")+"&title=Song&body=Listen", nil, nil)
	requireStatus(t, w, 200)
	picker := w.Body.String()[strings.Index(w.Body.String(), `id="share-topic"`):]
	picker = picker[:strings.Index(picker, "</select>")]
	if n := strings.Count(picker, "<option value=\"") - 1; n != shareChoices {
		t.Fatalf("picker offers %d conversations, want %d", n, shareChoices)
	}
	if strings.Contains(picker, "Conversation 00") || !strings.Contains(picker, fmt.Sprintf("Conversation %02d", shareChoices+4)) {
		t.Fatal("picker should offer the newest conversations")
	}
}
