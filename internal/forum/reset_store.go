package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TokenPurpose distinguishes the flows that use one-time tokens, so a token
// minted for one can never be redeemed for another. Password resets are the
// only flow today; email verification would be the next.
type TokenPurpose string

const (
	// TokenPasswordReset authorises choosing a new password.
	TokenPasswordReset TokenPurpose = "password_reset"
)

// errAuthToken covers every way a one-time token can be unusable: unknown,
// expired, already spent, or minted for a different purpose. Callers cannot
// tell them apart, which is deliberate.
var errAuthToken = errors.New("that reset link is invalid or has expired")

// CreateAuthToken records a single-use token. The caller passes the digest,
// never the token itself. Any earlier token for the same person and purpose is
// removed first, so only the newest link works.
func (s *Store) CreateAuthToken(ctx context.Context, userID int64, purpose TokenPurpose, tokenHash string, expiresAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM auth_tokens WHERE user_id = ? AND purpose = ?", userID, string(purpose)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO auth_tokens(token_hash, user_id, purpose, created_at, expires_at) VALUES (?, ?, ?, ?, ?)", tokenHash, userID, string(purpose), time.Now().Unix(), expiresAt.Unix()); err != nil {
		return fmt.Errorf("insert auth token: %w", err)
	}
	return tx.Commit()
}

// ConsumeAuthToken redeems a token and returns the account it belonged to.
//
// Marking it used and reading the owner happen in one transaction, so a token
// cannot be redeemed twice even if two requests arrive together.
func (s *Store) ConsumeAuthToken(ctx context.Context, tokenHash string, purpose TokenPurpose, at time.Time) (User, error) {
	var userID int64
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var usedAt sql.NullInt64
	var expiresAt int64
	err = tx.QueryRowContext(ctx, "SELECT user_id, used_at, expires_at FROM auth_tokens WHERE token_hash = ? AND purpose = ?", tokenHash, string(purpose)).Scan(&userID, &usedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errAuthToken
	}
	if err != nil {
		return User{}, err
	}
	if usedAt.Valid || expiresAt <= at.Unix() {
		return User{}, errAuthToken
	}
	result, err := tx.ExecContext(ctx, "UPDATE auth_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL", at.Unix(), tokenHash)
	if err != nil {
		return User{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return User{}, err
	}
	if affected != 1 {
		return User{}, errAuthToken // lost a race to another request
	}
	var u User
	if err := tx.QueryRowContext(ctx, "SELECT id, username, role, created_at, can_invite, coalesce(invited_by, 0), invited_by_name, coalesce(invitation_id, 0), email FROM users WHERE id = ?", userID).Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.CanInvite, &u.InvitedBy, &u.InvitedByName, &u.InvitationID, &u.Email); err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

// AuthTokenValid reports whether a token could still be redeemed, without
// spending it. The reset form is reached by a GET and a person may load it
// twice, so validity has to be checkable without consuming the token.
func (s *Store) AuthTokenValid(ctx context.Context, tokenHash string, purpose TokenPurpose, at time.Time) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.role, u.created_at, u.can_invite, coalesce(u.invited_by, 0), u.invited_by_name, coalesce(u.invitation_id, 0), u.email
		FROM auth_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ? AND t.purpose = ? AND t.used_at IS NULL AND t.expires_at > ?`, tokenHash, string(purpose), at.Unix()).Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.CanInvite, &u.InvitedBy, &u.InvitedByName, &u.InvitationID, &u.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errAuthToken
	}
	return u, err
}

// DeleteAuthTokensForUser clears an account's outstanding tokens of one kind,
// so a link an owner no longer wants to honour stops working.
func (s *Store) DeleteAuthTokensForUser(ctx context.Context, userID int64, purpose TokenPurpose) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM auth_tokens WHERE user_id = ? AND purpose = ?", userID, string(purpose))
	return err
}

// DeleteExpiredAuthTokens prunes tokens that have lapsed or been redeemed.
func (s *Store) DeleteExpiredAuthTokens(ctx context.Context, at time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM auth_tokens WHERE expires_at <= ? OR used_at IS NOT NULL", at.Unix())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// SetPassword replaces an account's password hash.
func (s *Store) SetPassword(ctx context.Context, userID int64, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, userID)
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

// DeleteSessionsForUser signs every device out. A password change should not
// leave an old session usable.
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

// SetEmail records an optional address for one account.
func (s *Store) SetEmail(ctx context.Context, userID int64, email string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE users SET email = ? WHERE id = ?", email, userID)
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

// MemberReset reports one account and whether a reset link is waiting for it.
type MemberReset struct {
	User
	Pending   bool
	HasAvatar bool
}

// Members lists every account for the owner's member page.
func (s *Store) Members(ctx context.Context) ([]MemberReset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.username, u.role, u.created_at, u.can_invite, coalesce(u.invited_by, 0), u.invited_by_name, coalesce(u.invitation_id, 0), u.email,
		EXISTS(SELECT 1 FROM auth_tokens t WHERE t.user_id = u.id AND t.purpose = 'password_reset' AND t.used_at IS NULL AND t.expires_at > ?) AS pending,
		EXISTS(SELECT 1 FROM user_avatars va WHERE va.user_id = u.id) AS has_avatar
		FROM users u ORDER BY u.role DESC, u.username COLLATE NOCASE`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []MemberReset
	for rows.Next() {
		var m MemberReset
		if err := rows.Scan(&m.ID, &m.Username, &m.Role, &m.CreatedAt, &m.CanInvite, &m.InvitedBy, &m.InvitedByName, &m.InvitationID, &m.Email, &m.Pending, &m.HasAvatar); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
