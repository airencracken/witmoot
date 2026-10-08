// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"fmt"
	"github.com/airencracken/comfylib/reference"
	"net/http"
	"net/http/httptest"
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
