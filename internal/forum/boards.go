package forum

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (a *App) owner(next http.HandlerFunc) http.HandlerFunc {
	return a.signedIn(func(w http.ResponseWriter, r *http.Request) {
		if state(r).User.Role != "owner" {
			a.fail(w, r, 403, errOwner.Error())
			return
		}
		next(w, r)
	})
}

func (a *App) manageBoards(w http.ResponseWriter, r *http.Request) {
	boards, err := a.store.Boards(r.Context(), state(r).User)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, 200, Page{View: "boards", Title: "Manage boards", Boards: boards, Deleted: r.URL.Query().Get("deleted") == "1"})
}

func (a *App) boardSettings(w http.ResponseWriter, r *http.Request) {
	b := Board{Restricted: true, Category: "Our little corner"}
	if r.PathValue("id") != "" {
		var err error
		b, err = a.store.Board(r.Context(), pathID(r), state(r).User)
		if err != nil {
			a.storeError(w, r, err)
			return
		}
	}
	a.showBoardSettings(w, r, 200, b, "")
}

func (a *App) showBoardSettings(w http.ResponseWriter, r *http.Request, status int, b Board, message string) {
	members, err := a.store.BoardMembers(r.Context(), b.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if r.Method == http.MethodPost {
		for i := range members {
			members[i].Access = r.PostForm.Get(fmt.Sprintf("access_%d", members[i].ID))
		}
	}
	title := "Board settings"
	if b.ID == 0 {
		title = "Create a board"
	}
	a.render(w, r, status, Page{View: "board-settings", Title: title, Board: b, BoardMembers: members, Error: message, Saved: r.URL.Query().Get("saved") == "1"})
}

func (a *App) saveBoardSettings(w http.ResponseWriter, r *http.Request) {
	b := Board{ID: pathID(r), Name: strings.TrimSpace(r.PostForm.Get("name")), Category: strings.TrimSpace(r.PostForm.Get("category")), Description: strings.TrimSpace(r.PostForm.Get("description")), Restricted: r.PostForm.Get("visibility") == "selected"}
	if r.PathValue("id") != "" {
		current, err := a.store.Board(r.Context(), b.ID, state(r).User)
		if err != nil {
			a.storeError(w, r, err)
			return
		}
		b.Archived = current.Archived
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
	if err != nil || revision < 0 {
		a.fail(w, r, 400, "Reload the board settings before saving.")
		return
	}
	b.Revision = revision
	if visibility := r.PostForm.Get("visibility"); visibility != "site" && visibility != "selected" {
		a.showBoardSettings(w, r, 422, b, "Choose who can access this board.")
		return
	}
	access, err := boardAccessForm(r)
	if err != nil {
		a.showBoardSettings(w, r, 422, b, err.Error())
		return
	}
	id, err := a.store.SaveBoard(r.Context(), state(r).User.ID, b, access)
	if err != nil {
		if !errors.Is(err, errBoardConflict) && !errors.Is(err, errBoardAccess) && !errors.Is(err, errBoardDetails) {
			a.storeError(w, r, err)
			return
		}
		status := http.StatusUnprocessableEntity
		if errors.Is(err, errBoardConflict) {
			status = http.StatusConflict
		}
		a.showBoardSettings(w, r, status, b, err.Error())
		return
	}
	a.redirect(w, r, fmt.Sprintf("/boards/%d/settings?saved=1", id))
}

func boardAccessForm(r *http.Request) (map[int64]string, error) {
	access := make(map[int64]string)
	for key, values := range r.PostForm {
		if !strings.HasPrefix(key, "access_") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(key, "access_"), 10, 64)
		if err != nil || id <= 0 || len(values) != 1 {
			return nil, errBoardAccess
		}
		level := values[0]
		if level != "none" && level != "read" && level != "write" {
			return nil, errBoardAccess
		}
		access[id] = level
	}
	return access, nil
}
