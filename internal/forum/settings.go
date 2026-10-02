package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func (s *Store) SaveInstanceSettings(ctx context.Context, mode Mode, branding SiteBranding) error {
	if !mode.Valid() {
		return errMode
	}
	branding, err := cleanBranding(branding)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE settings SET mode = ? WHERE id = 1", mode); err != nil {
		return err
	}
	if err := saveBranding(ctx, tx, branding); err != nil {
		return fmt.Errorf("save site branding: %w", err)
	}
	return tx.Commit()
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, 200, a.settingsPage(r, ""))
}

func (a *App) settingsPage(r *http.Request, message string) Page {
	return Page{View: "settings", Title: "Site settings", Saved: r.URL.Query().Get("saved") == "1", Error: message}
}

// settingsDraft redisplays a rejected settings form with what was submitted.
// Other forms on the page, such as image uploads, show the saved values.
func (a *App) settingsDraft(r *http.Request, message string) Page {
	page := a.settingsPage(r, message)
	if _, submitted := r.PostForm["site_name"]; submitted {
		page.BrandingDraft = true
		page.Name = r.PostForm.Get("site_name")
		page.SourceURL = r.PostForm.Get("source_url")
		page.WelcomeTitle = r.PostForm.Get("welcome_title")
		page.WelcomeText = r.PostForm.Get("welcome_text")
		page.HouseRules = r.PostForm.Get("house_rules")
		page.OwnerContact = r.PostForm.Get("owner_contact")
	}
	return page
}

func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	mode := Mode(r.PostForm.Get("mode"))
	if !mode.Valid() {
		a.render(w, r, 422, a.settingsDraft(r, errMode.Error()))
		return
	}
	if _, hasBranding := r.PostForm["site_name"]; hasBranding {
		branding := SiteBranding{
			Name: r.PostForm.Get("site_name"), SourceURL: r.PostForm.Get("source_url"),
			WelcomeTitle: r.PostForm.Get("welcome_title"), WelcomeText: r.PostForm.Get("welcome_text"),
			HouseRules: r.PostForm.Get("house_rules"), OwnerContact: r.PostForm.Get("owner_contact"),
		}
		if err := a.store.SaveInstanceSettings(r.Context(), mode, branding); err != nil {
			a.render(w, r, 422, a.settingsDraft(r, err.Error()))
			return
		}
	} else if err := a.store.SetMode(r.Context(), mode); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/settings?saved=1")
}
