package forum

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func canonicalBaseURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("WITMOOT_BASE_URL must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	u.Path = ""
	return u.String(), nil
}

func (a *App) inviteURL(r *http.Request, token string) string {
	return a.origin(r) + "/join?invite=" + token
}

// origin is the public HTTP(S) origin used to build shareable links. It prefers
// WITMOOT_BASE_URL and otherwise trusts the request, matching invitation links.
func (a *App) origin(r *http.Request) string {
	if a.config.BaseURL != "" {
		return a.config.BaseURL
	}
	scheme := "http"
	if r.TLS != nil || a.config.SecureCookies {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: r.Host}).String()
}

func (a *App) mayInvite(w http.ResponseWriter, r *http.Request) bool {
	user := state(r).User
	if user.Role != "owner" && !user.CanInvite {
		a.fail(w, r, 403, "You do not have permission to issue invitations.")
		return false
	}
	if state(r).Mode == ModePersonal {
		a.fail(w, r, 403, "Invitations are disabled in Personal mode.")
		return false
	}
	return true
}

func (a *App) renderInvites(w http.ResponseWriter, r *http.Request, status int, p Page) {
	page, err := pageNumber(r)
	if err != nil {
		a.fail(w, r, 400, "That page number is not valid.")
		return
	}
	user := state(r).User
	var list []Invitation
	var more bool
	if user.Role == "owner" {
		list, more, err = a.store.Invitations(r.Context(), pageSize, (page-1)*pageSize)
	} else {
		list, more, err = a.store.InvitationsByCreator(r.Context(), user.ID, pageSize, (page-1)*pageSize)
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p.View, p.Title, p.Invitations = "invites", "Invite someone in", list
	if user.Role == "owner" {
		p.InviteMembers, err = a.store.InviteMembers(r.Context())
		if err != nil {
			a.serverError(w, r, err)
			return
		}
	}
	pagination(r, &p, page, more)
	a.render(w, r, status, p)
}

func (a *App) invites(w http.ResponseWriter, r *http.Request) {
	if a.mayInvite(w, r) {
		a.renderInvites(w, r, 200, Page{InviteUses: "1", InviteDays: "7", Saved: r.URL.Query().Get("saved") == "1"})
	}
}

func invitationForm(r *http.Request) (Page, InvitationOptions, error) {
	p := Page{InviteLabel: strings.TrimSpace(r.PostForm.Get("label")), InviteUses: strings.TrimSpace(r.PostForm.Get("max_uses")), InviteDays: strings.TrimSpace(r.PostForm.Get("expires_days"))}
	if p.InviteUses == "" {
		p.InviteUses = "1"
	}
	if _, present := r.PostForm["expires_days"]; !present {
		p.InviteDays = "7"
	}
	opts := InvitationOptions{Label: p.InviteLabel}
	if !validText(opts.Label, 0, 64) || strings.ContainsAny(opts.Label, "\r\n") {
		return p, opts, errors.New("Keep the label to 64 characters on one line.")
	}
	uses, err := strconv.Atoi(p.InviteUses)
	if err != nil || uses < 0 || uses > 10000 {
		return p, opts, errors.New("Uses must be a whole number from 0 to 10000; 0 means no limit.")
	}
	opts.MaxUses = uses
	if p.InviteDays != "" {
		days, err := strconv.Atoi(p.InviteDays)
		if err != nil || days < 0 || days > 3650 {
			return p, opts, errors.New("Expiry must be a whole number from 0 to 3650 days; blank or 0 means never.")
		}
		if days > 0 {
			expires := time.Now().AddDate(0, 0, days)
			opts.ExpiresAt = &expires
		}
	}
	return p, opts, nil
}

func (a *App) createInvite(w http.ResponseWriter, r *http.Request) {
	if !a.mayInvite(w, r) {
		return
	}
	p, opts, err := invitationForm(r)
	if err != nil {
		p.Error = err.Error()
		a.renderInvites(w, r, 422, p)
		return
	}
	token := randomToken()
	if _, err := a.store.CreateInvitation(r.Context(), state(r).User.ID, tokenHash(token), token[:12], opts); err != nil {
		if errors.Is(err, errPersonal) {
			a.fail(w, r, 403, err.Error())
			return
		}
		a.serverError(w, r, err)
		return
	}
	p.InviteCode, p.InviteLink = token, a.inviteURL(r, token)
	a.renderInvites(w, r, 200, p)
}

func (a *App) revokeInvite(w http.ResponseWriter, r *http.Request) {
	if !a.mayInvite(w, r) {
		return
	}
	user := state(r).User
	if err := a.store.RevokeInvitationByCreator(r.Context(), pathID(r), user.ID, user.Role == "owner"); err != nil {
		a.storeError(w, r, err)
		return
	}
	a.redirect(w, r, "/invites?saved=1")
}

func (a *App) setInvitePermission(w http.ResponseWriter, r *http.Request) {
	enabled := r.PostForm.Get("enabled") == "1"
	if err := a.store.SetInvitePermission(r.Context(), pathID(r), enabled); err != nil {
		a.storeError(w, r, err)
		return
	}
	a.redirect(w, r, "/invites?saved=1")
}
