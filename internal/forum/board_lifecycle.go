package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
)

var (
	errBoardActionConflict = errors.New("this board has changed; reload the confirmation page before continuing")
	errBoardConfirmation   = errors.New("type the board name exactly to confirm permanent deletion")
)

type BoardContents struct{ Topics, Posts, Images int }

func (s *Store) BoardContents(ctx context.Context, boardID int64) (BoardContents, error) {
	var c BoardContents
	err := s.db.QueryRowContext(ctx, `WITH board_topics AS (SELECT id FROM topics WHERE board_id = ?),
		board_posts AS (SELECT id FROM posts WHERE topic_id IN (SELECT id FROM board_topics))
		SELECT (SELECT count(*) FROM board_topics), (SELECT count(*) FROM board_posts),
		(SELECT count(*) FROM attachments WHERE post_id IN (SELECT id FROM board_posts))`, boardID).Scan(&c.Topics, &c.Posts, &c.Images)
	return c, err
}

func requireOwner(ctx context.Context, q rowQuerier, userID int64) error {
	var role string
	if err := q.QueryRowContext(ctx, "SELECT role FROM users WHERE id = ?", userID).Scan(&role); err != nil {
		return err
	}
	if role != "owner" {
		return errOwner
	}
	return nil
}

func (s *Store) ChangeBoard(ctx context.Context, ownerID, boardID, revision int64, action, confirmation string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireOwner(ctx, tx, ownerID); err != nil {
		return err
	}
	b, err := readBoard(ctx, tx, boardID, &User{ID: ownerID, Role: "owner"})
	if err != nil {
		return err
	}
	if b.Revision != revision {
		return errBoardActionConflict
	}
	if err := changeBoard(ctx, tx, b, action, confirmation); err != nil {
		return err
	}
	return tx.Commit()
}

func changeBoard(ctx context.Context, tx *sql.Tx, b Board, action, confirmation string) error {
	switch action {
	case "archive", "restore":
		archived := action == "archive"
		if b.Archived == archived {
			return errBoardActionConflict
		}
		_, err := tx.ExecContext(ctx, "UPDATE boards SET archived = ?, revision = revision + 1 WHERE id = ?", archived, b.ID)
		return err
	case "delete":
		if confirmation != b.Name {
			return errBoardConfirmation
		}
		// Attachment links and member grants cascade; imvault originals stay in imvault.
		for _, query := range []string{
			"DELETE FROM posts WHERE topic_id IN (SELECT id FROM topics WHERE board_id = ?)",
			"DELETE FROM topics WHERE board_id = ?",
			"DELETE FROM boards WHERE id = ?",
		} {
			if _, err := tx.ExecContext(ctx, query, b.ID); err != nil {
				return err
			}
		}
		return nil
	default:
		return errBoardActionConflict
	}
}

func (a *App) archive(w http.ResponseWriter, r *http.Request) {
	boards, err := a.store.Boards(r.Context(), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	boards = slices.DeleteFunc(boards, func(b Board) bool { return !b.Archived })
	a.render(w, r, 200, Page{View: "archive", Title: "Archived boards", Boards: boards})
}

func (a *App) boardActionForm(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := a.store.Board(r.Context(), pathID(r), state(r).User)
		if err != nil {
			a.storeError(w, r, err)
			return
		}
		if action == "archive" && b.Archived || action == "restore" && !b.Archived {
			a.fail(w, r, 409, errBoardActionConflict.Error())
			return
		}
		a.showBoardAction(w, r, 200, b, action, "")
	}
}

func (a *App) showBoardAction(w http.ResponseWriter, r *http.Request, status int, b Board, action, message string) {
	contents, err := a.store.BoardContents(r.Context(), b.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	title := map[string]string{"archive": "Archive board", "restore": "Restore board", "delete": "Delete board permanently"}[action]
	a.render(w, r, status, Page{View: "board-action", Title: title, Board: b, BoardAction: action, BoardContents: contents, Error: message})
}

func (a *App) boardAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
		if err != nil || revision < 0 {
			a.fail(w, r, 400, "Reload the confirmation page before continuing.")
			return
		}
		err = a.store.ChangeBoard(r.Context(), state(r).User.ID, pathID(r), revision, action, r.PostForm.Get("confirmation"))
		if errors.Is(err, errBoardActionConflict) {
			a.fail(w, r, 409, err.Error())
			return
		}
		if errors.Is(err, errBoardConfirmation) {
			b, readErr := a.store.Board(r.Context(), pathID(r), state(r).User)
			if readErr != nil {
				a.storeError(w, r, readErr)
				return
			}
			b.Revision = revision
			a.showBoardAction(w, r, 422, b, action, err.Error())
			return
		}
		if err != nil {
			a.storeError(w, r, err)
			return
		}
		if action == "delete" {
			a.redirect(w, r, "/boards/manage?deleted=1")
			return
		}
		a.redirect(w, r, fmt.Sprintf("/boards/%d/settings", pathID(r)))
	}
}
