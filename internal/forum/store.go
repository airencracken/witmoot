package forum

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ db *sql.DB }

type User struct {
	ID        int64
	Username  string
	Role      string
	CreatedAt int64
}

type Board struct {
	ID                          int64
	Category, Name, Description string
	Topics, Posts               int
	LastTitle, LastAuthor       string
	LastTopicID, LastAt         int64
}

type Topic struct {
	ID, BoardID              int64
	Title, Author, BoardName string
	CreatedAt, UpdatedAt     int64
	Replies                  int
}

type Post struct {
	ID                  int64
	Body, Author        string
	CreatedAt, JoinedAt int64
	Number              int
}

type Stats struct{ Topics, Posts, Members int }

func OpenStore(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("database schema %d is newer than this application supports", version)
	}
	if version == 0 {
		migration, err := migrations.ReadFile("migrations/001_initial.sql")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(migration)); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		if _, err := tx.Exec("PRAGMA user_version = 1"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Boards(ctx context.Context) ([]Board, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.id, b.category, b.name, b.description,
		(SELECT count(*) FROM topics WHERE board_id = b.id),
		(SELECT count(*) FROM posts p JOIN topics t ON t.id = p.topic_id WHERE t.board_id = b.id),
		coalesce(t.id, 0), coalesce(t.title, ''), coalesce(t.updated_at, 0),
		coalesce((SELECT u.username FROM posts p JOIN users u ON u.id = p.author_id WHERE p.topic_id = t.id ORDER BY p.id DESC LIMIT 1), '')
		FROM boards b LEFT JOIN topics t ON t.id = (SELECT id FROM topics WHERE board_id = b.id ORDER BY updated_at DESC, id DESC LIMIT 1)
		ORDER BY b.position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boards []Board
	for rows.Next() {
		var b Board
		if err := rows.Scan(&b.ID, &b.Category, &b.Name, &b.Description, &b.Topics, &b.Posts, &b.LastTopicID, &b.LastTitle, &b.LastAt, &b.LastAuthor); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func (s *Store) Board(ctx context.Context, id int64) (Board, error) {
	var b Board
	err := s.db.QueryRowContext(ctx, "SELECT id, category, name, description FROM boards WHERE id = ?", id).Scan(&b.ID, &b.Category, &b.Name, &b.Description)
	return b, err
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var v Stats
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM topics), (SELECT count(*) FROM posts), (SELECT count(*) FROM users)`).Scan(&v.Topics, &v.Posts, &v.Members)
	return v, err
}

const topicSelect = `SELECT t.id, t.board_id, t.title, u.username, b.name, t.created_at, t.updated_at,
	(SELECT count(*) - 1 FROM posts WHERE topic_id = t.id) FROM topics t JOIN users u ON u.id = t.author_id JOIN boards b ON b.id = t.board_id `

func (s *Store) Topics(ctx context.Context, boardID int64, query string, limit, offset int) ([]Topic, bool, error) {
	where := "WHERE 1=1"
	var args []any
	if boardID != 0 {
		where += " AND t.board_id = ?"
		args = append(args, boardID)
	}
	if query != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(query) + "%"
		where += ` AND (t.title LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM posts p WHERE p.topic_id = t.id AND p.body LIKE ? ESCAPE '\'))`
		args = append(args, pattern, pattern)
	}
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, topicSelect+where+" ORDER BY t.updated_at DESC, t.id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var topics []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.BoardID, &t.Title, &t.Author, &t.BoardName, &t.CreatedAt, &t.UpdatedAt, &t.Replies); err != nil {
			return nil, false, err
		}
		topics = append(topics, t)
	}
	more := len(topics) > limit
	if more {
		topics = topics[:limit]
	}
	return topics, more, rows.Err()
}

func (s *Store) Topic(ctx context.Context, id int64) (Topic, error) {
	var t Topic
	err := s.db.QueryRowContext(ctx, topicSelect+" WHERE t.id = ?", id).Scan(&t.ID, &t.BoardID, &t.Title, &t.Author, &t.BoardName, &t.CreatedAt, &t.UpdatedAt, &t.Replies)
	return t, err
}

func (s *Store) Posts(ctx context.Context, topicID int64, limit, offset int) ([]Post, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.body, u.username, p.created_at, u.created_at FROM posts p JOIN users u ON u.id = p.author_id WHERE p.topic_id = ? ORDER BY p.id LIMIT ? OFFSET ?`, topicID, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.Body, &p.Author, &p.CreatedAt, &p.JoinedAt); err != nil {
			return nil, false, err
		}
		p.Number = offset + len(posts) + 1
		posts = append(posts, p)
	}
	more := len(posts) > limit
	if more {
		posts = posts[:limit]
	}
	return posts, more, rows.Err()
}

func (s *Store) CreateTopic(ctx context.Context, boardID, authorID int64, title, body string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, "INSERT INTO topics(board_id, author_id, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", boardID, authorID, title, now, now)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO posts(topic_id, author_id, body, created_at) VALUES (?, ?, ?, ?)", id, authorID, body, now); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) Reply(ctx context.Context, topicID, authorID int64, body string) (int64, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, "INSERT INTO posts(topic_id, author_id, body, created_at) VALUES (?, ?, ?, ?)", topicID, authorID, body, now)
	if err != nil {
		return 0, 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE topics SET updated_at = ? WHERE id = ?", now, topicID); err != nil {
		return 0, 0, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM posts WHERE topic_id = ?", topicID).Scan(&count); err != nil {
		return 0, 0, err
	}
	return id, count, tx.Commit()
}

var errUsernameTaken = errors.New("that username is already in use")

func (s *Store) CreateUser(ctx context.Context, name, hash string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO users(username, password_hash, created_at) VALUES (?, ?, ?) ON CONFLICT(username) DO NOTHING`, name, hash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, errUsernameTaken
	}
	return result.LastInsertId()
}

func (s *Store) Credentials(ctx context.Context, name string) (User, string, error) {
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx, "SELECT id, username, role, created_at, password_hash FROM users WHERE username = ?", name).Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &hash)
	return u, hash, err
}

func (s *Store) Session(ctx context.Context, hash string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.role, u.created_at FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.expires_at > ?`, hash, time.Now().Unix()).Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &u, err
}

func (s *Store) NewSession(ctx context.Context, hash string, userID int64, expires time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sessions(token_hash, user_id, expires_at) VALUES (?, ?, ?)", hash, userID, expires.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hash)
	return err
}

var errInvitation = errors.New("this invitation has expired or has already been used")

func (s *Store) Register(ctx context.Context, name, passwordHash, inviteHash string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM invitations WHERE token_hash = ? AND expires_at > ?", inviteHash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, errInvitation
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO users(username, password_hash, created_at) VALUES (?, ?, ?) ON CONFLICT(username) DO NOTHING`, name, passwordHash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, errUsernameTaken
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) CreateOwner(ctx context.Context, name, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO users(username, password_hash, role, created_at) VALUES (?, ?, 'owner', ?) ON CONFLICT(username) DO NOTHING`, name, passwordHash, time.Now().Unix())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errUsernameTaken
	}
	return nil
}

func (s *Store) Invite(ctx context.Context, ownerID int64, hash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO invitations(token_hash, created_by, expires_at) VALUES (?, ?, ?)`, hash, ownerID, time.Now().Add(7*24*time.Hour).Unix())
	return err
}
