package forum

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

var (
	errGroupDetails      = errors.New("use a single-line group name of 1–80 characters and up to 500 for the description")
	errGroupName         = errors.New("that group name is already in use")
	errGroupMembers      = errors.New("choose each member once from the available member accounts")
	errGroupConflict     = errors.New("this group has changed; reload the page before continuing")
	errGroupConfirmation = errors.New("type the group name exactly to confirm deletion")
)

type Group struct {
	ID, Revision      int64
	Name, Description string
	Members, Boards   int
}

type GroupMember struct {
	ID       int64
	Username string
	Selected bool
}

const groupSelect = `SELECT g.id, g.name, g.description, g.revision,
	(SELECT count(*) FROM group_members WHERE group_id = g.id),
	(SELECT count(*) FROM board_groups WHERE group_id = g.id) FROM user_groups g `

func (s *Store) Groups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, groupSelect+"ORDER BY g.name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.Revision, &g.Members, &g.Boards); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *Store) Group(ctx context.Context, id int64) (Group, error) {
	return readGroup(ctx, s.db, id)
}

func readGroup(ctx context.Context, q rowQuerier, id int64) (Group, error) {
	var g Group
	err := q.QueryRowContext(ctx, groupSelect+"WHERE g.id = ?", id).Scan(&g.ID, &g.Name, &g.Description, &g.Revision, &g.Members, &g.Boards)
	return g, err
}

func (s *Store) GroupMembers(ctx context.Context, id int64) ([]GroupMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.username, gm.user_id IS NOT NULL FROM users u
		LEFT JOIN group_members gm ON gm.user_id = u.id AND gm.group_id = ? WHERE u.role = 'member' ORDER BY u.username`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []GroupMember
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.ID, &m.Username, &m.Selected); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *Store) SaveGroup(ctx context.Context, ownerID int64, g Group, members []int64) (int64, error) {
	g.Name, g.Description = strings.TrimSpace(g.Name), strings.TrimSpace(g.Description)
	if !validText(g.Name, 1, 80) || strings.ContainsAny(g.Name, "\r\n\t") || !validText(g.Description, 0, 500) {
		return 0, errGroupDetails
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := requireOwner(ctx, tx, ownerID); err != nil {
		return 0, err
	}
	id, err := saveGroupDetails(ctx, tx, g)
	if err != nil {
		return 0, err
	}
	if err := replaceGroupMembers(ctx, tx, id, members); err != nil {
		return 0, err
	}
	if err := invalidateGroupBoards(ctx, tx, id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func saveGroupDetails(ctx context.Context, tx *sql.Tx, g Group) (int64, error) {
	if g.ID != 0 {
		current, err := readGroup(ctx, tx, g.ID)
		if err != nil {
			return 0, err
		}
		if current.Revision != g.Revision {
			return 0, errGroupConflict
		}
	}
	var duplicate bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_groups WHERE name = ? AND id != ?)", g.Name, g.ID).Scan(&duplicate); err != nil {
		return 0, err
	}
	if duplicate {
		return 0, errGroupName
	}
	if g.ID == 0 {
		result, err := tx.ExecContext(ctx, "INSERT INTO user_groups(name, description) VALUES (?, ?)", g.Name, g.Description)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	_, err := tx.ExecContext(ctx, "UPDATE user_groups SET name = ?, description = ?, revision = revision + 1 WHERE id = ?", g.Name, g.Description, g.ID)
	return g.ID, err
}

func replaceGroupMembers(ctx context.Context, tx *sql.Tx, id int64, members []int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM group_members WHERE group_id = ?", id); err != nil {
		return err
	}
	seen := make(map[int64]bool)
	for _, userID := range members {
		var role string
		if err := tx.QueryRowContext(ctx, "SELECT role FROM users WHERE id = ?", userID).Scan(&role); err != nil || role != "member" || seen[userID] {
			return errGroupMembers
		}
		seen[userID] = true
		if _, err := tx.ExecContext(ctx, "INSERT INTO group_members(group_id, user_id) VALUES (?, ?)", id, userID); err != nil {
			return err
		}
	}
	return nil
}

func invalidateGroupBoards(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE boards SET revision = revision + 1 WHERE id IN (SELECT board_id FROM board_groups WHERE group_id = ?)", id)
	return err
}

func (s *Store) DeleteGroup(ctx context.Context, ownerID, id, revision int64, confirmation string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireOwner(ctx, tx, ownerID); err != nil {
		return err
	}
	g, err := readGroup(ctx, tx, id)
	if err != nil {
		return err
	}
	if g.Revision != revision {
		return errGroupConflict
	}
	if confirmation != g.Name {
		return errGroupConfirmation
	}
	if err := invalidateGroupBoards(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM user_groups WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}
