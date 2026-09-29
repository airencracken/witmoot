package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	errSuspended          = errors.New("this account is suspended; contact a site owner")
	errMemberAction       = errors.New("only member accounts can be suspended or restored")
	errCommunityConflict  = errors.New("this has changed since you opened the form; reload it before continuing")
	errMemberConfirmation = errors.New("type the member's username exactly to confirm")
)

const removedMessage = "This message was removed by a site owner."

func requireActive(ctx context.Context, q rowQuerier, userID int64) error {
	var suspended bool
	if err := q.QueryRowContext(ctx, "SELECT suspended FROM users WHERE id = ?", userID).Scan(&suspended); err != nil {
		return err
	}
	if suspended {
		return errSuspended
	}
	return nil
}

// ChangeMember preserves contributions and permissions. Restoring access never
// restores old sessions, reset links, or invitations.
func (s *Store) ChangeMember(ctx context.Context, ownerID, memberID, revision int64, action, confirmation string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireOwner(ctx, tx, ownerID); err != nil {
		return err
	}
	var name, role string
	var suspended bool
	var current int64
	if err := tx.QueryRowContext(ctx, "SELECT username, role, suspended, suspension_revision FROM users WHERE id = ?", memberID).Scan(&name, &role, &suspended, &current); err != nil {
		return err
	}
	if role != "member" {
		return errMemberAction
	}
	if action != "suspend" && action != "restore" || current != revision || suspended == (action == "suspend") {
		return errCommunityConflict
	}
	if confirmation != name {
		return errMemberConfirmation
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET suspended = ?, suspension_revision = suspension_revision + 1 WHERE id = ?", action == "suspend", memberID); err != nil {
		return err
	}
	if action == "suspend" {
		for _, query := range []string{
			"DELETE FROM sessions WHERE user_id = ?",
			"DELETE FROM auth_tokens WHERE user_id = ?",
			"UPDATE invitations SET revoked_at = coalesce(revoked_at, strftime('%s', 'now')) WHERE created_by = ?",
		} {
			if _, err := tx.ExecContext(ctx, query, memberID); err != nil {
				return err
			}
		}
	}
	if err := recordCommunityEvent(ctx, tx, ownerID, action, name); err != nil {
		return err
	}
	return tx.Commit()
}

// RemovePost clears the text and shared image links without moving replies or
// reusing message IDs. A stale confirmation cannot remove an edited message.
func (s *Store) RemovePost(ctx context.Context, ownerID, postID, revision int64, replaceTitle bool) (Post, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	if err := requireOwner(ctx, tx, ownerID); err != nil {
		return Post{}, err
	}
	p, err := readPost(ctx, tx, postID, &User{ID: ownerID, Role: "owner"})
	if err != nil {
		return Post{}, err
	}
	if p.Revision != revision || replaceTitle && p.Number != 1 {
		return Post{}, errCommunityConflict
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM attachments WHERE post_id = ?", postID); err != nil {
		return Post{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE posts SET body = ?, removed = 1, revision = revision + 1 WHERE id = ?", removedMessage, postID); err != nil {
		return Post{}, err
	}
	if replaceTitle {
		if _, err := tx.ExecContext(ctx, "UPDATE topics SET title = 'Conversation' WHERE id = ?", p.TopicID); err != nil {
			return Post{}, err
		}
	}
	if err := recordCommunityEvent(ctx, tx, ownerID, "remove-message", fmt.Sprintf("Message %d in conversation %d", postID, p.TopicID)); err != nil {
		return Post{}, err
	}
	p.Body, p.Removed, p.Revision = removedMessage, true, p.Revision+1
	return p, tx.Commit()
}

func recordCommunityEvent(ctx context.Context, tx *sql.Tx, ownerID int64, action, subject string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO community_events(actor_id, actor_name, action, subject, created_at)
		SELECT id, username, ?, ?, ? FROM users WHERE id = ?`, action, subject, time.Now().Unix(), ownerID)
	return err
}

type CommunityEvent struct {
	Actor, Action, Subject string
	CreatedAt              int64
}

func (s *Store) CommunityEvents(ctx context.Context) ([]CommunityEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT actor_name, action, subject, created_at FROM community_events ORDER BY id DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []CommunityEvent
	for rows.Next() {
		var e CommunityEvent
		if err := rows.Scan(&e.Actor, &e.Action, &e.Subject, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) Owners(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT username FROM users WHERE role = 'owner' ORDER BY username COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
