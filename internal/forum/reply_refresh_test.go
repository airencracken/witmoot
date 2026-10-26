package forum

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHTMXRedirectRefreshesFragmentDestinations(t *testing.T) {
	app := &App{}
	for _, tc := range []struct {
		name, current, target string
		refresh               bool
	}{
		{"same page, new fragment", "https://board.example/topics/1?page=1#post-2", "/topics/1?page=1#post-3", true},
		{"same permalink", "https://board.example/topics/1?page=1#post-2", "/topics/1?page=1#post-2", true},
		{"same page, no query", "https://board.example/topics/1#reply", "/topics/1#post-2", true},
		{"new query", "https://board.example/topics/1", "/topics/1?page=1#post-2", false},
		{"page boundary", "https://board.example/topics/1?page=1#post-20", "/topics/1?page=2#post-21", false},
		{"different topic", "https://board.example/topics/2?page=1#post-3", "/topics/1?page=1#post-2", false},
		{"different path", "https://board.example/login", "/topics/1", false},
		{"missing current URL", "", "/topics/1?page=1#post-2", false},
		{"missing current URL at root", "", "/", false},
		{"malformed current URL", "https://%zz/topics/1?page=1", "/topics/1?page=1#post-2", false},
	} {
		for _, enhanced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/htmx=%t", tc.name, enhanced), func(t *testing.T) {
				r := httptest.NewRequest("POST", "/topics/1/replies", nil)
				r.Header.Set("HX-Current-URL", tc.current)
				if enhanced {
					r.Header.Set("HX-Request", "true")
				}
				w := httptest.NewRecorder()
				app.redirect(w, r, tc.target)
				wantRefresh := ""
				if enhanced {
					requireStatus(t, w, 200)
					if w.Header().Get("HX-Redirect") != tc.target {
						t.Fatalf("wrong enhanced destination: %v", w.Header())
					}
					if tc.refresh {
						wantRefresh = "true"
					}
				} else {
					requireStatus(t, w, 303)
					if w.Header().Get("Location") != tc.target || w.Header().Get("HX-Redirect") != "" {
						t.Fatalf("wrong native redirect: %v", w.Header())
					}
				}
				if got := w.Header().Get("HX-Refresh"); got != wantRefresh {
					t.Fatalf("HX-Refresh=%q, want %q", got, wantRefresh)
				}
			})
		}
	}
}

func TestRepliesRefreshSavedPostAcrossPages(t *testing.T) {
	app, client := newTestApp(t, false)
	user := signInTest(t, app, client, false)
	id, err := app.store.CreateTopic(t.Context(), 1, user, "Consecutive replies", "Starting message", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	for n := 2; n <= pageSize+2; n++ {
		body := fmt.Sprintf("Reply number %d with <markup> & Unicode 界", n)
		form := url.Values{"body": {body}, "csrf": {client.cookies[app.cookieName("csrf")].Value}}
		headers := map[string]string{}
		if n%2 == 0 {
			headers["HX-Request"] = "true"
			headers["HX-Current-URL"] = fmt.Sprintf("https://board.example/topics/%d?page=%d#reply", id, (n-1)/pageSize+1)
		}
		w := client.request("POST", fmt.Sprintf("/topics/%d/replies", id), form, headers)
		location := w.Header().Get("Location")
		if n%2 == 0 {
			requireStatus(t, w, 200)
			location = w.Header().Get("HX-Redirect")
			if w.Header().Get("HX-Refresh") != "true" {
				t.Fatal("enhanced reply must reload when only the fragment changes")
			}
		} else {
			requireStatus(t, w, 303)
		}
		posts, _, err := app.store.Posts(t.Context(), id, 1, n-1, &User{ID: user, Role: "member"})
		if err != nil || len(posts) != 1 || posts[0].Body != body {
			t.Fatalf("saved reply %d: %+v, %v", n, posts, err)
		}
		want := fmt.Sprintf("/topics/%d?page=%d#post-%d", id, (n-1)/pageSize+1, posts[0].ID)
		if location != want {
			t.Fatalf("reply %d goes to %q, want %q", n, location, want)
		}
		u, err := url.Parse(location)
		if err != nil {
			t.Fatal(err)
		}
		w = client.request("GET", u.RequestURI(), nil, nil)
		requireStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), fmt.Sprintf(`id="%s"`, u.Fragment)) || !strings.Contains(w.Body.String(), "&lt;markup&gt; &amp; Unicode 界") {
			t.Fatal("redirect destination must contain the saved reply with escaped content")
		}
		if !strings.Contains(w.Body.String(), `hx-sync="this:drop"`) {
			t.Fatal("reply form must drop repeated submissions while busy")
		}
	}
}

func TestInvalidRepliesDoNotPostOrRedirect(t *testing.T) {
	app, client := newTestApp(t, false)
	user := signInTest(t, app, client, false)
	id, err := app.store.CreateTopic(t.Context(), 1, user, "Reply validation", "Starting message", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"empty": "", "whitespace": " \n\t ", "NUL": "before\x00after",
		"too long": strings.Repeat("界", 20001), "invalid UTF-8": "bad\xfftext",
	} {
		t.Run(name, func(t *testing.T) {
			form := url.Values{"body": {body}, "csrf": {client.cookies[app.cookieName("csrf")].Value}}
			w := client.request("POST", fmt.Sprintf("/topics/%d/replies", id), form, map[string]string{
				"HX-Request": "true", "HX-Current-URL": fmt.Sprintf("https://board.example/topics/%d?page=1#reply", id),
			})
			requireStatus(t, w, 422)
			for _, header := range []string{"Location", "HX-Redirect", "HX-Refresh"} {
				if w.Header().Get(header) != "" {
					t.Fatalf("invalid reply must stay on the form: %s=%q", header, w.Header().Get(header))
				}
			}
			if !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), `hx-sync="this:drop"`) {
				t.Fatal("validation must keep the error and protected reply form usable")
			}
			posts, _, err := app.store.Posts(t.Context(), id, pageSize, 0, &User{ID: user, Role: "member"})
			if err != nil || len(posts) != 1 {
				t.Fatalf("invalid reply changed the conversation: %+v, %v", posts, err)
			}
		})
	}
}
