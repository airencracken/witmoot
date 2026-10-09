// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/airencracken/comfylib/memberprofile"
)

type MemberProfile struct {
	ID        int64
	Username  string
	HasAvatar bool
	Biography memberprofile.Profile
}

func (s *Store) MemberProfile(ctx context.Context, id int64) (MemberProfile, error) {
	var p MemberProfile
	var links string
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,EXISTS(SELECT 1 FROM user_avatars WHERE user_id=u.id),coalesce(p.name,''),coalesce(p.bio,''),coalesce(p.links,'[]') FROM users u LEFT JOIN member_profiles p ON p.user_id=u.id WHERE u.id=? AND u.suspended=0 AND u.deleted=0`, id).Scan(&p.ID, &p.Username, &p.HasAvatar, &p.Biography.Name, &p.Biography.Bio, &links)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(links), &p.Biography.Links); err != nil {
		return p, err
	}
	p.Biography, err = memberprofile.Normalize(p.Biography)
	return p, err
}

func (s *Store) SetMemberProfile(ctx context.Context, id int64, p memberprofile.Profile) error {
	p, err := memberprofile.Normalize(p)
	if err != nil {
		return err
	}
	links, err := json.Marshal(p.Links)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO member_profiles(user_id,name,bio,links) SELECT id,?,?,? FROM users WHERE id=? AND suspended=0 AND deleted=0 ON CONFLICT(user_id) DO UPDATE SET name=excluded.name,bio=excluded.bio,links=excluded.links`, p.Name, p.Bio, string(links), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return sql.ErrNoRows
	}
	return err
}

func (a *App) memberProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	p, err := a.store.MemberProfile(r.Context(), pathID(r))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, 200, Page{View: "member-profile", Title: p.Username + "'s profile", ProfileMember: p})
}

func (a *App) editMemberProfile(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.MemberProfile(r.Context(), state(r).User.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	notice := ""
	if r.URL.Query().Get("saved") == "profile" {
		notice = "Your profile has been saved."
	}
	a.render(w, r, 200, Page{View: "profile-settings", Title: "Your profile", Biography: p.Biography, Notice: notice})
}

func (a *App) saveMemberProfile(w http.ResponseWriter, r *http.Request) {
	p, err := memberprofile.ParseForm(r.PostForm)
	if err != nil {
		a.render(w, r, 422, Page{View: "profile-settings", Title: "Your profile", Biography: p, Error: err.Error()})
		return
	}
	if err = a.store.SetMemberProfile(r.Context(), state(r).User.ID, p); err != nil {
		a.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/account/profile?saved=profile", 303)
}
