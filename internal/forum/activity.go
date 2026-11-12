// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"github.com/airencracken/comfylib/analytics"
	"net/http"
)

func (a *App) trackOpen(kind analytics.Kind, next http.HandlerFunc) http.HandlerFunc {
	return analytics.Track(a.store.AnalyticsDB(), next, func(r *http.Request) []analytics.Event {
		u := state(r).User
		if u == nil {
			return nil
		}
		if kind == analytics.Image {
			if r.URL.Query().Has("width") {
				return nil
			}
			if r.URL.Query().Get("open") != "1" {
				return nil
			}
		}
		item := analytics.NumericID(r.PathValue("id"))
		if item == "" {
			return nil
		}
		return []analytics.Event{{Kind: kind, Member: analytics.MemberID(u.ID), Item: item}}
	})
}

func (a *App) renderTopicActivity(w http.ResponseWriter, r *http.Request, status int, p Page) {
	analytics.Track(a.store.AnalyticsDB(), func(w http.ResponseWriter, r *http.Request) { a.render(w, r, status, p) }, func(r *http.Request) []analytics.Event {
		u := state(r).User
		if u == nil {
			return nil
		}
		var events []analytics.Event
		for _, post := range p.Posts {
			if !post.Removed {
				events = append(events, analytics.Event{Kind: analytics.Post, Member: analytics.MemberID(u.ID), Item: analytics.MemberID(post.ID)})
			}
		}
		return events
	})(w, r)
}
