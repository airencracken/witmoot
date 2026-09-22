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
	Label, Prefix, Creator string
	MaxUses, Uses          int
	ExpiresAt, RevokedAt   sql.NullInt64
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
		SELECT ?, ?, ?, ?, ?, ?, ? FROM settings WHERE id = 1 AND mode != 'personal'`, hash, prefix, opts.Label, ownerID, time.Now().Unix(), opts.MaxUses, expires)
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
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.label, i.prefix, u.username, i.max_uses, i.uses, i.expires_at, i.revoked_at
		FROM invitations i JOIN users u ON u.id = i.created_by ORDER BY i.id DESC LIMIT ? OFFSET ?`, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var list []Invitation
	for rows.Next() {
		var i Invitation
		if err := rows.Scan(&i.ID, &i.Label, &i.Prefix, &i.Creator, &i.MaxUses, &i.Uses, &i.ExpiresAt, &i.RevokedAt); err != nil {
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
