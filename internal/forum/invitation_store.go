package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type InvitationOptions struct {
	Label     string
	MaxUses   int
	ExpiresAt *time.Time
}

type Invitation struct {
	ID                     int64
	CreatorID              int64
	Label, Prefix, Creator string
	MaxUses, Uses          int
	Invitees               int
	ExpiresAt, RevokedAt   sql.NullInt64
}

type InviteMember struct {
	ID            int64
	Username      string
	CanInvite     bool
	InvitedByName string
	CreatedAt     int64
}

func (i Invitation) Status() string {
	switch {
	case i.RevokedAt.Valid:
		return "revoked"
	case i.ExpiresAt.Valid && i.ExpiresAt.Int64 <= time.Now().Unix():
		return "expired"
	case i.MaxUses > 0 && i.Uses >= i.MaxUses:
		return "used up"
	default:
		return "open"
	}
}

func (i Invitation) UsesLabel() string {
	if i.MaxUses == 0 {
		return fmt.Sprintf("%d used, no limit", i.Uses)
	}
	return fmt.Sprintf("%d of %d used", i.Uses, i.MaxUses)
}

func (s *Store) CreateInvitation(ctx context.Context, ownerID int64, hash, prefix string, opts InvitationOptions) (int64, error) {
	if !validText(opts.Label, 0, 64) || opts.MaxUses < 0 || opts.MaxUses > 10000 {
		return 0, errors.New("invalid invitation options")
	}
	var expires any
	if opts.ExpiresAt != nil {
		expires = opts.ExpiresAt.Unix()
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO invitations(token_hash, prefix, label, created_by, created_at, max_uses, expires_at)
		SELECT ?, ?, ?, u.id, ?, ?, ? FROM settings s JOIN users u ON u.id = ?
		WHERE s.id = 1 AND s.mode != 'personal' AND (u.role = 'owner' OR u.can_invite = 1)`, hash, prefix, opts.Label, time.Now().Unix(), opts.MaxUses, expires, ownerID)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, errPersonal
	}
	return result.LastInsertId()
}

func (s *Store) Invitations(ctx context.Context, limit, offset int) ([]Invitation, bool, error) {
	return s.listInvitations(ctx, 0, true, limit, offset)
}

func (s *Store) InvitationsByCreator(ctx context.Context, creatorID int64, limit, offset int) ([]Invitation, bool, error) {
	return s.listInvitations(ctx, creatorID, false, limit, offset)
}

func (s *Store) listInvitations(ctx context.Context, creatorID int64, all bool, limit, offset int) ([]Invitation, bool, error) {
	filter := ""
	args := []any{}
	if !all {
		filter = "WHERE i.created_by = ?"
		args = append(args, creatorID)
	}
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.created_by, i.label, i.prefix, u.username, i.max_uses, i.uses,
		(SELECT count(*) FROM users newcomer WHERE newcomer.invitation_id = i.id), i.expires_at, i.revoked_at
		FROM invitations i JOIN users u ON u.id = i.created_by `+filter+` ORDER BY i.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var list []Invitation
	for rows.Next() {
		var i Invitation
		if err := rows.Scan(&i.ID, &i.CreatorID, &i.Label, &i.Prefix, &i.Creator, &i.MaxUses, &i.Uses, &i.Invitees, &i.ExpiresAt, &i.RevokedAt); err != nil {
			return nil, false, err
		}
		list = append(list, i)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	return list, more, nil
}

func (s *Store) InviteMembers(ctx context.Context) ([]InviteMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, can_invite, invited_by_name, created_at
		FROM users WHERE role != 'owner' ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []InviteMember
	for rows.Next() {
		var member InviteMember
		if err := rows.Scan(&member.ID, &member.Username, &member.CanInvite, &member.InvitedByName, &member.CreatedAt); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *Store) SetInvitePermission(ctx context.Context, userID int64, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET can_invite = ? WHERE id = ? AND role != 'owner'`, value, userID)
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

func (s *Store) RevokeInvitation(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "UPDATE invitations SET revoked_at = coalesce(revoked_at, ?) WHERE id = ?", time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RevokeInvitationByCreator(ctx context.Context, id, creatorID int64, owner bool) error {
	query := `UPDATE invitations SET revoked_at = coalesce(revoked_at, ?) WHERE id = ? AND created_by = ?`
	args := []any{time.Now().Unix(), id, creatorID}
	if owner {
		query = `UPDATE invitations SET revoked_at = coalesce(revoked_at, ?) WHERE id = ?`
		args = args[:2]
	}
	result, err := s.db.ExecContext(ctx, query, args...)
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
