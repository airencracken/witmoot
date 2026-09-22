package forum

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

type Mode string

const (
	ModePersonal Mode = "personal"
	ModePrivate  Mode = "private"
	ModeOpen     Mode = "open"
)

func (m Mode) Valid() bool { return m == ModePersonal || m == ModePrivate || m == ModeOpen }
func (m Mode) Label() string {
	switch m {
	case ModePersonal:
		return "Personal"
	case ModeOpen:
		return "Open"
	default:
		return "Private"
	}
}

var errMode = errors.New("choose Personal, Private, or Open")

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readMode(ctx context.Context, q rowQuerier) (Mode, error) {
	var mode Mode
	if err := q.QueryRowContext(ctx, "SELECT mode FROM settings WHERE id = 1").Scan(&mode); err != nil {
		return "", err
	}
	if !mode.Valid() {
		return "", errMode
	}
	return mode, nil
}

func (s *Store) Mode(ctx context.Context) (Mode, error) { return readMode(ctx, s.db) }

func (s *Store) SetMode(ctx context.Context, mode Mode) error {
	if !mode.Valid() {
		return errMode
	}
	_, err := s.db.ExecContext(ctx, "UPDATE settings SET mode = ? WHERE id = 1", mode)
	return err
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	if state(r).User.Role != "owner" {
		a.fail(w, r, 403, "Only owners can change site settings.")
		return
	}
	a.render(w, r, 200, Page{View: "settings", Title: "Site settings", Saved: r.URL.Query().Get("saved") == "1"})
}

func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	if state(r).User.Role != "owner" {
		a.fail(w, r, 403, "Only owners can change site settings.")
		return
	}
	mode := Mode(r.PostForm.Get("mode"))
	if !mode.Valid() {
		a.render(w, r, 422, Page{View: "settings", Title: "Site settings", Error: errMode.Error()})
		return
	}
	if err := a.store.SetMode(r.Context(), mode); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/settings?saved=1")
}
