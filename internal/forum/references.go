// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"github.com/airencracken/comfylib/reference"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Reference struct{ URL, Label, Preview string }

func (a *App) discussionHandoff(w http.ResponseWriter, r *http.Request) {
	d, err := reference.Read(r.URL.Query())
	if err != nil {
		a.fail(w, r, 400, "That discussion draft is not valid.")
		return
	}
	body := strings.TrimSpace(d.Body) + "\n\n" + d.Source
	board := r.URL.Query().Get("board")
	topic := r.URL.Query().Get("topic")
	if board != "" && topic != "" {
		a.fail(w, r, 400, "Choose a board or an existing conversation.")
		return
	}
	if topic != "" {
		id, err := strconv.ParseInt(topic, 10, 64)
		if err != nil || id <= 0 {
			a.fail(w, r, 400, "Choose a conversation.")
			return
		}
		r.SetPathValue("id", topic)
		a.showTopic(w, r, 200, "", body)
		return
	}
	if board != "" {
		id, err := strconv.ParseInt(board, 10, 64)
		if err != nil || id <= 0 {
			a.fail(w, r, 400, "Choose a board.")
			return
		}
		b, err := a.store.Board(r.Context(), id, state(r).User)
		if err != nil {
			a.storeError(w, r, err)
			return
		}
		if err = boardWriteError(b.Access, b.Archived); err != nil {
			a.storeError(w, r, err)
			return
		}
		title := d.Title
		if len(strings.TrimSpace(title)) < 3 {
			title = "A shared link"
		}
		a.render(w, r, 200, Page{View: "compose", Title: "Start a conversation", Board: b, TitleInput: title, BodyInput: body})
		return
	}
	boards, err := a.store.Boards(r.Context(), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	a.render(w, r, 200, Page{View: "share", Title: "Bring a link to the conversation", Boards: boards, SharedSource: d.Source, TitleInput: d.Title, BodyInput: d.Body})
}

func (a *App) postReferences(posts []Post) {
	for i := range posts {
		if posts[i].Removed {
			continue
		}
		seen := map[string]bool{}
		for _, s := range segments(posts[i].Body) {
			if s.URL == "" || seen[s.URL] {
				continue
			}
			seen[s.URL] = true
			u, err := reference.URL(s.URL)
			if err != nil {
				continue
			}
			label := ""
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 2 && parts[len(parts)-2] == "recommendations" {
				if n, err := strconv.ParseInt(parts[len(parts)-1], 10, 64); err == nil && n > 0 {
					label = "Songstead recommendation"
				}
			}
			preview := ""
			if a.vault != nil {
				if _, err := a.vault.ParseAlbumLink(s.URL); err == nil {
					label = "imvault album"
					preview = "/posts/" + strconv.FormatInt(posts[i].ID, 10) + "/album-preview?url=" + url.QueryEscape(s.URL)
				}
			}
			if label != "" {
				posts[i].References = append(posts[i].References, Reference{URL: s.URL, Label: label, Preview: preview})
			}
		}
	}
}

// Preview is deliberately requested, never fetched while opening a topic.
// Only currently public albums yield metadata, even to a signed-in reader.
func (a *App) albumPreview(w http.ResponseWriter, r *http.Request) {
	post, err := a.store.Post(r.Context(), pathID(r), state(r).User)
	if err != nil || post.Removed {
		a.fail(w, r, 404, "Album preview unavailable.")
		return
	}
	raw := r.URL.Query().Get("url")
	found := false
	for _, s := range segments(post.Body) {
		if s.URL == raw {
			found = true
		}
	}
	if !found || a.vault == nil {
		a.fail(w, r, 404, "Album preview unavailable.")
		return
	}
	slug, err := a.vault.ParseAlbumLink(raw)
	if err != nil {
		a.fail(w, r, 404, "Album preview unavailable.")
		return
	}
	token, err := a.imageToken(r.Context(), post.AuthorID)
	if err != nil {
		a.fail(w, r, 404, "Album preview unavailable. Open its link in imvault.")
		return
	}
	album, err := a.vault.AlbumPreview(r.Context(), token, slug)
	if err != nil {
		a.fail(w, r, 404, "Album preview unavailable. Open its link in imvault.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.render(w, r, 200, Page{View: "album-preview", Title: "Album preview", AlbumTitle: album.Title, AlbumCount: album.ImageCount, SharedSource: raw})
}
