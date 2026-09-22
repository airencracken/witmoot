package forum

import (
	"context"
	"errors"
	"net/http"
)

type Audience string

const (
	AudienceOwners  Audience = "owners"
	AudienceMembers Audience = "members"
	AudiencePublic  Audience = "public"
)

func (m Mode) Audience() Audience {
	switch m {
	case ModePersonal:
		return AudienceOwners
	case ModeOpen:
		return AudiencePublic
	default:
		return AudienceMembers
	}
}

func (a Audience) Label() string {
	switch a {
	case AudienceOwners:
		return "Owners only"
	case AudiencePublic:
		return "Public"
	default:
		return "Members only"
	}
}

func (s requestState) canRead() bool {
	return s.Mode == ModeOpen || s.User != nil && (s.Mode == ModePrivate || s.User.Role == "owner")
}

func (s requestState) canPost() bool { return s.User != nil && s.canRead() }

// All aggregate and detail reads start with this same audience filter.
const visibleTopics = `WITH visible_topics AS (
	SELECT * FROM topics WHERE audience = 'public' OR (? AND audience = 'members') OR ?
) `

func readerArgs(user *User) []any { return []any{user != nil, user != nil && user.Role == "owner"} }

var (
	errPersonal        = errors.New("this board is in Personal mode; only owners can use it")
	errAudienceChanged = errors.New("the board's audience has changed; review who can read this conversation and submit again")
)

func canWrite(ctx context.Context, q rowQuerier, authorID int64) (Mode, error) {
	mode, err := readMode(ctx, q)
	if err != nil {
		return "", err
	}
	var role string
	if err := q.QueryRowContext(ctx, "SELECT role FROM users WHERE id = ?", authorID).Scan(&role); err != nil {
		return "", err
	}
	if mode == ModePersonal && role != "owner" {
		return "", errPersonal
	}
	return mode, nil
}

func (a *App) readable(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !state(r).canRead() {
			if state(r).User == nil {
				a.redirect(w, r, "/login")
			} else {
				a.fail(w, r, 403, errPersonal.Error())
			}
			return
		}
		next(w, r)
	}
}
