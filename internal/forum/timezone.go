package forum

import (
	"context"
	"errors"
	"github.com/airencracken/comfylib/displaytime"
	"net/http"
	"time"
)

var errTimezone = displaytime.ErrTimezone
var timezoneSuggestions = displaytime.Suggestions()

func parseTimezone(name string) (*time.Location, error) { return displaytime.Parse(name) }
func displayTimezone(name string) *time.Location        { return displaytime.New(name).Location() }

func (p Page) Date(unix int64) string  { return displaytime.In(p.timezone).Date(unix) }
func (p Page) Stamp(unix int64) string { return displaytime.In(p.timezone).Stamp(unix) }

func (s *Store) SetTimezone(ctx context.Context, id int64, name string) error {
	return displaytime.Save(ctx, s.db, "UPDATE users SET timezone=? WHERE id=? AND deleted=0 AND suspended=0", id, name)
}

func (a *App) saveTimezone(w http.ResponseWriter, r *http.Request) {
	user := state(r).User
	name, err := displaytime.ParseForm(r.PostForm)
	if err == nil {
		err = a.store.SetTimezone(r.Context(), user.ID, name)
	}
	if err != nil {
		if errors.Is(err, errTimezone) {
			page := a.accountPage(r, "", "", user.Email)
			page.TimezoneInput = name
			page.TimezoneError = errTimezone.Error()
			a.render(w, r, http.StatusUnprocessableEntity, page)
			return
		}
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/account?saved=timezone")
}
