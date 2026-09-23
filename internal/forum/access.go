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
const visibleTopics = `WITH reader AS (SELECT ? AS id, ? AS owner), visible_boards AS (
	SELECT b.*, CASE WHEN r.owner OR (b.restricted = 0 AND r.id != 0) THEN 'write'
		WHEN b.restricted = 0 THEN 'read' ELSE bm.access END AS access
	FROM boards b CROSS JOIN reader r LEFT JOIN board_members bm ON bm.board_id = b.id AND bm.user_id = r.id
	WHERE b.restricted = 0 OR r.owner OR bm.access IS NOT NULL
), visible_topics AS (
	SELECT t.* FROM topics t JOIN visible_boards b ON b.id = t.board_id CROSS JOIN reader r
	WHERE t.audience = 'public' OR (r.id != 0 AND t.audience = 'members') OR r.owner
) `

func readerArgs(user *User) []any {
	if user == nil {
		return []any{int64(0), false}
	}
	return []any{user.ID, user.Role == "owner"}
}

var (
	errPersonal        = errors.New("this board is in Personal mode; only owners can use it")
	errAudienceChanged = errors.New("the board's audience has changed; review who can read this conversation and submit again")
	errReadOnly        = errors.New("you have read-only access to this board")
	errArchived        = errors.New("this board is archived; an owner must restore it before anyone can post or edit")
	errEditConflict    = errors.New("this message has changed since you opened it; reload it before editing again")
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

func boardForWriter(ctx context.Context, q rowQuerier, boardID, authorID int64) (Board, Mode, error) {
	mode, err := canWrite(ctx, q, authorID)
	if err != nil {
		return Board{}, mode, err
	}
	user := User{ID: authorID}
	if err := q.QueryRowContext(ctx, "SELECT role FROM users WHERE id = ?", authorID).Scan(&user.Role); err != nil {
		return Board{}, mode, err
	}
	b, err := readBoard(ctx, q, boardID, &user)
	if err == nil {
		err = boardWriteError(b.Access, b.Archived)
	}
	return b, mode, err
}

func boardWriteError(access string, archived bool) error {
	if archived {
		return errArchived
	}
	if access != "write" {
		return errReadOnly
	}
	return nil
}

func (b Board) Audience(mode Mode) Audience {
	if b.Restricted && mode != ModePersonal {
		return AudienceMembers
	}
	return mode.Audience()
}

func (t Topic) AudienceLabel() string {
	if t.BoardRestricted && t.Audience != AudienceOwners {
		return "Selected board members"
	}
	return t.Audience.Label()
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
