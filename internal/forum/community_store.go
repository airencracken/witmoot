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

// removedMessage replaces the text of a removed message. Migration 013 checks
// for this exact text, so it cannot change without a migration.
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
	defer rollback(tx)
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
	if err := recordCommunityEvent(ctx, tx, ownerID, communityEvent{Action: action, Subject: name}); err != nil {
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
	defer rollback(tx)
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
	event := communityEvent{Action: "remove-message", Subject: fmt.Sprintf("message %d in conversation %d", postID, p.TopicID), PostID: postID, TitleReplaced: replaceTitle}
	if err := recordCommunityEvent(ctx, tx, ownerID, event); err != nil {
		return Post{}, err
	}
	p.Body, p.Removed, p.Revision = removedMessage, true, p.Revision+1
	return p, tx.Commit()
}

// RemoveMemberAvatar clears an account's avatar for moderation and records it
// in the owner activity log. It reports sql.ErrNoRows for an unknown account.
func (s *Store) RemoveMemberAvatar(ctx context.Context, ownerID, memberID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := requireOwner(ctx, tx, ownerID); err != nil {
		return err
	}
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT username FROM users WHERE id = ?", memberID).Scan(&name); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM user_avatars WHERE user_id = ?", memberID)
	if err != nil {
		return err
	}
	if removed, err := result.RowsAffected(); err != nil {
		return err
	} else if removed > 0 {
		if err := recordCommunityEvent(ctx, tx, ownerID, communityEvent{Action: "remove-avatar", Subject: name}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// communityEvent is one entry for the owner activity log.
type communityEvent struct {
	Action, Subject string
	PostID          int64
	TitleReplaced   bool
}

func recordCommunityEvent(ctx context.Context, tx *sql.Tx, ownerID int64, event communityEvent) error {
	var postID any
	if event.PostID != 0 {
		postID = event.PostID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO community_events(actor_id, actor_name, action, subject, post_id, title_replaced, created_at)
		SELECT id, username, ?, ?, ?, ?, ? FROM users WHERE id = ?`, event.Action, event.Subject, postID, event.TitleReplaced, time.Now().Unix(), ownerID)
	return err
}

// CommunityEvent is an owner action as the activity log shows it. URL links to
// a removed message while it still exists.
type CommunityEvent struct {
	Actor, Action, Subject, URL string
	TitleReplaced               bool
	CreatedAt                   int64
}

func (s *Store) CommunityEvents(ctx context.Context) ([]CommunityEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.actor_name, e.action, e.subject, e.title_replaced, e.created_at, coalesce(p.id, 0), coalesce(p.topic_id, 0),
		coalesce((SELECT count(*) FROM posts earlier WHERE earlier.topic_id = p.topic_id AND earlier.id <= p.id), 0)
		FROM community_events e LEFT JOIN posts p ON p.id = e.post_id ORDER BY e.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var events []CommunityEvent
	for rows.Next() {
		var e CommunityEvent
		var post Post
		if err := rows.Scan(&e.Actor, &e.Action, &e.Subject, &e.TitleReplaced, &e.CreatedAt, &post.ID, &post.TopicID, &post.Number); err != nil {
			return nil, err
		}
		if post.ID != 0 {
			e.URL = post.URL()
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
	defer closeRows(rows)
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
