package forum

import (
	"errors"
	"net/http"
	"strconv"
)

func (a *App) about(w http.ResponseWriter, r *http.Request) {
	owners, err := a.store.Owners(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, 200, Page{View: "about", Title: "About this board", Owners: owners})
}

func (a *App) memberActionPage(w http.ResponseWriter, r *http.Request, action string) (Page, bool) {
	u, err := a.store.UserByID(r.Context(), pathID(r))
	if err != nil {
		a.storeError(w, r, err)
		return Page{}, false
	}
	if u.Role != "member" {
		a.fail(w, r, 403, errMemberAction.Error())
		return Page{}, false
	}
	if u.Suspended == (action == "suspend") {
		a.fail(w, r, 409, errCommunityConflict.Error())
		return Page{}, false
	}
	title := "Suspend member"
	if action == "restore" {
		title = "Restore member access"
	}
	return Page{View: "member-action", Title: title, Member: u, CommunityAction: action}, true
}

func (a *App) memberActionForm(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.memberActionPage(w, r, action)
		if ok {
			a.render(w, r, 200, p)
		}
	}
}

func (a *App) memberAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.memberActionPage(w, r, action)
		if !ok {
			return
		}
		revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
		if err != nil || revision < 0 {
			a.fail(w, r, 400, "Reload the confirmation page before continuing.")
			return
		}
		err = a.store.ChangeMember(r.Context(), state(r).User.ID, p.Member.ID, revision, action, r.PostForm.Get("confirmation"))
		if errors.Is(err, errMemberConfirmation) || errors.Is(err, errCommunityConflict) {
			status := 422
			if errors.Is(err, errCommunityConflict) {
				status = 409
			}
			p.Member.SuspensionRevision = revision
			p.Error = err.Error()
			a.render(w, r, status, p)
			return
		}
		if err != nil {
			a.storeError(w, r, err)
			return
		}
		a.redirect(w, r, "/members?saved="+action)
	}
}

func (a *App) removalPage(w http.ResponseWriter, r *http.Request) (Page, bool) {
	p, err := readPost(r.Context(), a.store.db, pathID(r), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return Page{}, false
	}
	t, err := a.store.Topic(r.Context(), p.TopicID, state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return Page{}, false
	}
	return Page{View: "remove-post", Title: "Remove message", Post: p, Topic: t}, true
}

func (a *App) removePostForm(w http.ResponseWriter, r *http.Request) {
	p, ok := a.removalPage(w, r)
	if ok {
		a.render(w, r, 200, p)
	}
}

func (a *App) removePost(w http.ResponseWriter, r *http.Request) {
	p, ok := a.removalPage(w, r)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
	if err != nil || revision < 0 {
		a.fail(w, r, 400, "Reload the confirmation page before continuing.")
		return
	}
	if r.PostForm.Get("confirmation") != "1" {
		p.Error = "Confirm that you want to permanently remove this message."
		a.render(w, r, 422, p)
		return
	}
	removed, err := a.store.RemovePost(r.Context(), state(r).User.ID, p.Post.ID, revision, r.PostForm.Get("replace_title") == "1")
	if errors.Is(err, errCommunityConflict) {
		p.Post.Revision = revision
		p.Error = err.Error()
		a.render(w, r, 409, p)
		return
	}
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	a.redirect(w, r, removed.URL())
}

func (a *App) moderation(w http.ResponseWriter, r *http.Request) {
	events, err := a.store.CommunityEvents(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, 200, Page{View: "moderation", Title: "Owner activity", Events: events})
}

func communityError(err error) bool {
	return errors.Is(err, errSuspended) || errors.Is(err, errMemberAction)
}
