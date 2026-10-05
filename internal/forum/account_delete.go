package forum

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"golang.org/x/crypto/bcrypt"
)

var errLastOwner = errors.New("Create another owner before deleting your account. A site must have someone to look after it.")
var errAccountChanged = errors.New("Your account changed. Sign in again before continuing.")

const deletedMessage = "This message was deleted by its author."

// DeleteAccount removes credentials, profile information and authored content
// atomically. An anonymous, permanently disabled row retains foreign keys and
// conversation positions, so other members' replies and links stay intact.
func (s *Store) DeleteAccount(ctx context.Context, userID int64, name, verifiedHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := checkAccountDeletion(ctx, tx, userID, name, verifiedHash); err != nil {
		return err
	}
	if err := clearAccountArtifacts(ctx, tx, userID, name); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET username=?, password_hash='', email='', role='member', can_invite=0,
  invited_by=NULL, invited_by_name='', invitation_id=NULL, created_at=0, suspended=1,
  suspension_revision=suspension_revision+1, deleted=1 WHERE id=?`, "~d"+strconv.FormatInt(userID, 36), userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func checkAccountDeletion(ctx context.Context, tx *sql.Tx, userID int64, name, verifiedHash string) error {
	var currentName, hash, role string
	if err := requireActive(ctx, tx, userID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "SELECT username,password_hash,role FROM users WHERE id=? AND deleted=0", userID).Scan(&currentName, &hash, &role); err != nil {
		return err
	}
	if name != currentName || hash != verifiedHash {
		return errAccountChanged
	}
	if role != "owner" {
		return nil
	}
	var owners int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE role='owner' AND deleted=0").Scan(&owners); err != nil {
		return err
	}
	if owners <= 1 {
		return errLastOwner
	}
	return nil
}

func clearAccountArtifacts(ctx context.Context, tx *sql.Tx, userID int64, name string) error {
	for _, query := range []string{
		"DELETE FROM attachments WHERE post_id IN (SELECT id FROM posts WHERE author_id=?) OR credential_user_id=?",
		"UPDATE posts SET body='This message was deleted by its author.', revision=revision+1 WHERE author_id=? AND removed=0",
		"UPDATE topics SET title='Conversation' WHERE author_id=?",
		"DELETE FROM sessions WHERE user_id=?",
		"DELETE FROM auth_tokens WHERE user_id=?",
		"DELETE FROM user_avatars WHERE user_id=?",
		"DELETE FROM imvault_connections WHERE user_id=?",
		"DELETE FROM board_members WHERE user_id=?",
		"DELETE FROM group_members WHERE user_id=?",
		"DELETE FROM invitations WHERE created_by=?",
		"UPDATE users SET invited_by=NULL, invited_by_name='' WHERE invited_by=?",
	} {
		args := []any{userID}
		if query == "DELETE FROM attachments WHERE post_id IN (SELECT id FROM posts WHERE author_id=?) OR credential_user_id=?" {
			args = append(args, userID)
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "UPDATE community_events SET actor_name='Former member' WHERE actor_id=?", userID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE community_events SET subject='Former member' WHERE subject=?", name)
	return err
}

func (a *App) accountDeletion(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, 200, Page{View: "delete-account", Title: "Delete your account"})
}

func (a *App) deleteAccount(w http.ResponseWriter, r *http.Request) {
	user := state(r).User
	failure := func(code int, message string) {
		a.render(w, r, code, Page{View: "delete-account", Title: "Delete your account", Error: message})
	}
	if r.PostForm.Get("confirmation") != user.Username {
		failure(422, "Type your username exactly to confirm.")
		return
	}
	_, hash, err := a.store.Credentials(r.Context(), user.Username)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.PostForm.Get("current_password"))) != nil {
		failure(403, "That is not your current password.")
		return
	}
	if err := a.store.DeleteAccount(r.Context(), user.ID, user.Username, hash); err != nil {
		if errors.Is(err, errLastOwner) || errors.Is(err, errAccountChanged) {
			failure(409, err.Error())
			return
		}
		a.serverError(w, r, err)
		return
	}
	a.cookie(w, "session", "", -1)
	a.redirect(w, r, "/login?deleted=1")
}
