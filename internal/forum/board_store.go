package forum

import (
	"context"
	"database/sql"
	"errors"
)

var (
	errOwner         = errors.New("only owners can manage boards")
	errBoardConflict = errors.New("this board has changed since you opened its settings; reload before saving")
	errBoardAccess   = errors.New("choose No access, Read only, or Read and post for each member")
	errBoardDetails  = errors.New("use 1–80 characters for the name and category, and up to 500 for the description")
)

type BoardMember struct {
	ID               int64
	Username, Access string
}

func (s *Store) BoardMembers(ctx context.Context, boardID int64) ([]BoardMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.username, coalesce(bm.access, 'none')
		FROM users u LEFT JOIN board_members bm ON bm.user_id = u.id AND bm.board_id = ?
		WHERE u.role = 'member' ORDER BY u.username`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []BoardMember
	for rows.Next() {
		var m BoardMember
		if err := rows.Scan(&m.ID, &m.Username, &m.Access); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *Store) SaveBoard(ctx context.Context, ownerID int64, board Board, access map[int64]string) (int64, error) {
	if !validText(board.Name, 1, 80) || !validText(board.Category, 1, 80) || !validText(board.Description, 0, 500) {
		return 0, errBoardDetails
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := requireOwner(ctx, tx, ownerID); err != nil {
		return 0, err
	}
	id, err := saveBoardDetails(ctx, tx, board)
	if err != nil {
		return 0, err
	}
	if err := replaceBoardMembers(ctx, tx, id, access); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func replaceBoardMembers(ctx context.Context, tx *sql.Tx, id int64, access map[int64]string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM board_members WHERE board_id = ?", id); err != nil {
		return err
	}
	for userID, level := range access {
		if level != "none" && level != "read" && level != "write" {
			return errBoardAccess
		}
		var role string
		if err := tx.QueryRowContext(ctx, "SELECT role FROM users WHERE id = ?", userID).Scan(&role); err != nil || role != "member" {
			return errBoardAccess
		}
		if level == "none" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO board_members(board_id, user_id, access) VALUES (?, ?, ?)", id, userID, level); err != nil {
			return err
		}
	}
	return nil
}

func saveBoardDetails(ctx context.Context, tx *sql.Tx, board Board) (int64, error) {
	if board.ID == 0 {
		result, err := tx.ExecContext(ctx, `INSERT INTO boards(id, category, name, description, restricted, position)
			VALUES ((SELECT last_id + 1 FROM object_sequences WHERE kind = 'boards'), ?, ?, ?, ?, (SELECT coalesce(max(position), 0) + 1 FROM boards))`, board.Category, board.Name, board.Description, board.Restricted)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	result, err := tx.ExecContext(ctx, `UPDATE boards SET category = ?, name = ?, description = ?, restricted = ?, revision = revision + 1
		WHERE id = ? AND revision = ?`, board.Category, board.Name, board.Description, board.Restricted, board.ID, board.Revision)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, errBoardConflict
	}
	return board.ID, nil
}
