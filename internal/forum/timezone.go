package forum

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // Standalone binaries also work on hosts without zoneinfo.
)

var timezoneName = regexp.MustCompile(`^(UTC|[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)+)$`)
var errTimezone = errors.New("Choose a timezone such as UTC or America/Los_Angeles.")

// These suggestions also work without JavaScript. Other IANA names may be
// entered directly; browsers with a timezone catalog expand the suggestions.
var timezoneSuggestions = []string{
	"UTC", "Africa/Cairo", "Africa/Johannesburg", "America/Anchorage",
	"America/Argentina/Buenos_Aires", "America/Chicago", "America/Denver",
	"America/Los_Angeles", "America/Mexico_City", "America/New_York",
	"America/Sao_Paulo", "America/Toronto", "Asia/Dubai", "Asia/Hong_Kong",
	"Asia/Kolkata", "Asia/Seoul", "Asia/Shanghai", "Asia/Singapore", "Asia/Tokyo",
	"Australia/Adelaide", "Australia/Brisbane", "Australia/Perth", "Australia/Sydney",
	"Europe/Berlin", "Europe/London", "Europe/Paris", "Pacific/Auckland", "Pacific/Honolulu",
}

func parseTimezone(name string) (*time.Location, error) {
	if len(name) > 64 || !timezoneName.MatchString(name) {
		return nil, errTimezone
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, errTimezone
	}
	return location, nil
}

func displayTimezone(name string) *time.Location {
	location, err := parseTimezone(name)
	if err != nil {
		return time.UTC
	}
	return location
}

func (p Page) Date(unix int64) string {
	return p.displayTime(unix, "Jan 2, 2006")
}

func (p Page) Stamp(unix int64) string {
	return p.displayTime(unix, "Jan 2, 2006 · 15:04 MST")
}

func (p Page) displayTime(unix int64, format string) string {
	location := p.timezone
	if location == nil {
		location = time.UTC
	}
	return time.Unix(unix, 0).In(location).Format(format)
}

func (s *Store) SetTimezone(ctx context.Context, userID int64, name string) error {
	if _, err := parseTimezone(name); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE users SET timezone=? WHERE id=? AND deleted=0 AND suspended=0", name, userID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (a *App) saveTimezone(w http.ResponseWriter, r *http.Request) {
	user := state(r).User
	name := strings.TrimSpace(r.PostForm.Get("timezone"))
	if err := a.store.SetTimezone(r.Context(), user.ID, name); err != nil {
		if errors.Is(err, errTimezone) {
			page := a.accountPage(r, errTimezone.Error(), "", user.Email)
			page.TimezoneInput = name
			a.render(w, r, http.StatusUnprocessableEntity, page)
			return
		}
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/account?saved=timezone")
}
