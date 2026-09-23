package forum

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

func (a *App) manageGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := a.store.Groups(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, 200, Page{View: "groups", Title: "Manage groups", Groups: groups, Deleted: r.URL.Query().Get("deleted") == "1"})
}

func (a *App) groupSettings(w http.ResponseWriter, r *http.Request) {
	var g Group
	if r.PathValue("id") != "" {
		var err error
		g, err = a.store.Group(r.Context(), pathID(r))
		if err != nil {
			a.storeError(w, r, err)
			return
		}
	}
	a.showGroupSettings(w, r, 200, g, "")
}

func (a *App) showGroupSettings(w http.ResponseWriter, r *http.Request, status int, g Group, message string) {
	members, err := a.store.GroupMembers(r.Context(), g.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	boards, err := a.store.GroupBoards(r.Context(), g.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if r.Method == http.MethodPost {
		for i := range members {
			members[i].Selected = slices.Contains(r.PostForm["members"], strconv.FormatInt(members[i].ID, 10))
		}
	}
	title := "Group settings"
	if g.ID == 0 {
		title = "Create a group"
	}
	a.render(w, r, status, Page{View: "group-settings", Title: title, Group: g, GroupMembers: members, GroupBoards: boards, Error: message, Saved: r.URL.Query().Get("saved") == "1"})
}

func groupForm(r *http.Request) (Group, []int64, error) {
	g := Group{ID: pathID(r), Name: strings.TrimSpace(r.PostForm.Get("name")), Description: strings.TrimSpace(r.PostForm.Get("description"))}
	revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
	if err != nil || revision < 0 {
		return g, nil, errGroupConflict
	}
	g.Revision = revision
	var members []int64
	for _, value := range r.PostForm["members"] {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return g, nil, errGroupMembers
		}
		members = append(members, id)
	}
	return g, members, nil
}

func (a *App) saveGroup(w http.ResponseWriter, r *http.Request) {
	g, members, err := groupForm(r)
	if err != nil {
		a.fail(w, r, 400, err.Error())
		return
	}
	if r.PathValue("id") != "" && g.ID <= 0 {
		a.storeError(w, r, sql.ErrNoRows)
		return
	}
	id, err := a.store.SaveGroup(r.Context(), state(r).User.ID, g, members)
	if err != nil {
		switch {
		case errors.Is(err, errGroupConflict):
			a.showGroupSettings(w, r, 409, g, err.Error())
		case errors.Is(err, errGroupDetails), errors.Is(err, errGroupName), errors.Is(err, errGroupMembers):
			a.showGroupSettings(w, r, 422, g, err.Error())
		default:
			a.storeError(w, r, err)
		}
		return
	}
	a.redirect(w, r, fmt.Sprintf("/groups/%d?saved=1", id))
}

func (a *App) groupDeleteForm(w http.ResponseWriter, r *http.Request) {
	g, err := a.store.Group(r.Context(), pathID(r))
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	a.render(w, r, 200, Page{View: "group-delete", Title: "Delete group", Group: g})
}

func (a *App) deleteGroup(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
	if err != nil || revision < 0 {
		a.fail(w, r, 400, "Reload the confirmation page before continuing.")
		return
	}
	err = a.store.DeleteGroup(r.Context(), state(r).User.ID, pathID(r), revision, r.PostForm.Get("confirmation"))
	if errors.Is(err, errGroupConfirmation) {
		g, readErr := a.store.Group(r.Context(), pathID(r))
		if readErr != nil {
			a.storeError(w, r, readErr)
			return
		}
		g.Revision = revision
		a.render(w, r, 422, Page{View: "group-delete", Title: "Delete group", Group: g, Error: err.Error()})
		return
	}
	if errors.Is(err, errGroupConflict) {
		a.fail(w, r, 409, err.Error())
		return
	}
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	a.redirect(w, r, "/groups?deleted=1")
}
