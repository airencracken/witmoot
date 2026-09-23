package forum

import (
	"context"
	"database/sql"
)

type BoardGroup struct {
	ID           int64
	Name, Access string
	Members      int
}

func validAccess(level string, inherit bool) bool {
	return level == "none" || level == "read" || level == "write" || inherit && level == "inherit"
}

func (s *Store) BoardGroups(ctx context.Context, boardID int64) ([]BoardGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.id, g.name, coalesce(bg.access, 'none'),
		(SELECT count(*) FROM group_members WHERE group_id = g.id) FROM user_groups g
		LEFT JOIN board_groups bg ON bg.group_id = g.id AND bg.board_id = ? ORDER BY g.name`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []BoardGroup
	for rows.Next() {
		var g BoardGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Access, &g.Members); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func replaceBoardGroups(ctx context.Context, tx *sql.Tx, boardID int64, groups map[int64]string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM board_groups WHERE board_id = ?", boardID); err != nil {
		return err
	}
	for id, access := range groups {
		if !validAccess(access, false) {
			return errBoardAccess
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_groups WHERE id = ?)", id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errBoardAccess
		}
		if access == "none" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO board_groups(board_id, group_id, access) VALUES (?, ?, ?)", boardID, id, access); err != nil {
			return err
		}
	}
	return nil
}

type GroupMemberAccess struct {
	Username, Override, Effective string
}

type GroupBoard struct {
	Board   Board
	Access  string
	Members []GroupMemberAccess
}

func accessLabel(access string) string {
	switch access {
	case "inherit":
		return "Use groups"
	case "read":
		return "Read only"
	case "write":
		return "Read and post"
	default:
		return "No access"
	}
}

func (s *Store) GroupBoards(ctx context.Context, groupID int64) ([]GroupBoard, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	boards, err := groupBoards(ctx, tx, groupID)
	if err != nil {
		return nil, err
	}
	if err := groupBoardMembers(ctx, tx, groupID, boards); err != nil {
		return nil, err
	}
	return boards, tx.Commit()
}

func groupBoards(ctx context.Context, tx *sql.Tx, groupID int64) ([]GroupBoard, error) {
	rows, err := tx.QueryContext(ctx, `SELECT b.id, b.name, b.restricted, b.archived, bg.access
		FROM board_groups bg JOIN boards b ON b.id = bg.board_id WHERE bg.group_id = ? ORDER BY b.position`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boards []GroupBoard
	for rows.Next() {
		var b GroupBoard
		if err := rows.Scan(&b.Board.ID, &b.Board.Name, &b.Board.Restricted, &b.Board.Archived, &b.Access); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func groupBoardMembers(ctx context.Context, tx *sql.Tx, groupID int64, boards []GroupBoard) error {
	rows, err := tx.QueryContext(ctx, `WITH reader AS (
		SELECT u.id, u.role = 'owner' AS owner FROM users u JOIN group_members gm ON gm.user_id = u.id WHERE gm.group_id = ?
	), `+boardPermissions+` SELECT p.id, u.username, coalesce(bm.access, 'inherit'),
		CASE WHEN settings.mode = 'personal' AND u.role != 'owner' THEN 'none'
		WHEN p.archived AND p.access != 'none' THEN 'read' ELSE p.access END
		FROM board_permissions p JOIN board_groups bg ON bg.board_id = p.id AND bg.group_id = ?
		JOIN users u ON u.id = p.user_id CROSS JOIN settings
		LEFT JOIN board_members bm ON bm.board_id = p.id AND bm.user_id = p.user_id ORDER BY u.username`, groupID, groupID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[int64]*GroupBoard, len(boards))
	for i := range boards {
		byID[boards[i].Board.ID] = &boards[i]
	}
	for rows.Next() {
		var id int64
		var m GroupMemberAccess
		if err := rows.Scan(&id, &m.Username, &m.Override, &m.Effective); err != nil {
			return err
		}
		byID[id].Members = append(byID[id].Members, m)
	}
	return rows.Err()
}
