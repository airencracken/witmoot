package forum

import (
	"context"
	"database/sql"
)

// ContributionBoard names a board one of the member's messages lives in, so an
// archive can say where a conversation happened.
type ContributionBoard struct {
	ID       int64
	Category string
	Name     string
}

// ContributionTopic is a conversation the member has posted in. It carries the
// audience so a move cannot quietly widen who could read it.
type ContributionTopic struct {
	ID                   int64
	BoardID              int64
	Title, Audience      string
	CreatedAt, UpdatedAt int64
	Posts                int
}

// ContributionPost is one of the member's own messages, with enough of its
// topic for the text to make sense away from the board.
type ContributionPost struct {
	ID                                 int64
	TopicID, BoardID, Number, Revision int64
	TopicTitle, BoardName, Body        string
	Author                             string
	AuthorID                           int64
	CreatedAt, EditedAt                int64
	Attachments                        []ContributionAttachment
	Removed                            bool
}

// ContributionAttachment records a shared image by reference. Witmoot stores
// only a preview link into imvault; the original stays with its owner there.
type ContributionAttachment struct {
	Name, Rendition, Server, RemoteID string
	ID                                int64
}

// Contributions is everything one member has written, gathered in one pass.
type Contributions struct {
	Boards  []ContributionBoard
	Topics  []ContributionTopic
	Posts   []ContributionPost
	Policy  *CommunityPolicy
	Members []exportMember
}

// MemberContributions collects a member's own messages and the context needed
// to understand them. It deliberately returns only this member's post bodies:
// a member export must not include other people's words. Boards and
// conversations the member can no longer read keep their IDs but lose their
// current names, titles and activity, which belong to the people who can.
func (s *Store) MemberContributions(ctx context.Context, member *User) (Contributions, error) {
	return s.contributions(ctx, member, member.ID)
}

// CommunityContributions includes every board and message, for an active owner.
func (s *Store) CommunityContributions(ctx context.Context, owner *User) (Contributions, error) {
	if err := requireOwner(ctx, s.db, owner.ID); err != nil {
		return Contributions{}, err
	}
	return s.contributions(ctx, owner, 0)
}

func (s *Store) contributions(ctx context.Context, member *User, authorID int64) (Contributions, error) {
	var out Contributions
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	if authorID == 0 {
		if err := requireOwner(ctx, tx, member.ID); err != nil {
			return out, err
		}
	}
	reader := readerArgs(member)
	out.Boards, err = collect(ctx, tx, visibleTopics+`SELECT b.id,
		CASE WHEN vb.id IS NULL THEN '' ELSE b.category END, CASE WHEN vb.id IS NULL THEN '' ELSE b.name END
		FROM boards b LEFT JOIN visible_boards vb ON vb.id = b.id
		WHERE (? = 0 OR b.id IN (SELECT t.board_id FROM posts p JOIN topics t ON t.id = p.topic_id WHERE (p.author_id = ? OR ? = 0)))
		ORDER BY b.position`, append(reader, authorID, authorID, authorID), func(rows *sql.Rows) (b ContributionBoard, err error) {
		return b, rows.Scan(&b.ID, &b.Category, &b.Name)
	})
	if err != nil {
		return out, err
	}
	out.Topics, err = collect(ctx, tx, visibleTopics+`SELECT t.id, t.board_id, CASE WHEN vt.id IS NULL THEN '' ELSE t.title END,
		t.audience, t.created_at, CASE WHEN vt.id IS NULL THEN t.created_at ELSE t.updated_at END,
		(SELECT count(*) FROM posts x WHERE x.topic_id = t.id AND (vt.id IS NOT NULL OR x.author_id = ?))
		FROM topics t LEFT JOIN visible_topics vt ON vt.id = t.id WHERE t.id IN (SELECT p.topic_id FROM posts p WHERE (p.author_id = ? OR ? = 0))
		ORDER BY t.id`, append(reader, authorID, authorID, authorID), func(rows *sql.Rows) (t ContributionTopic, err error) {
		return t, rows.Scan(&t.ID, &t.BoardID, &t.Title, &t.Audience, &t.CreatedAt, &t.UpdatedAt, &t.Posts)
	})
	if err != nil {
		return out, err
	}
	out.Posts, err = collect(ctx, tx, visibleTopics+`SELECT p.id, p.topic_id, t.board_id,
		CASE WHEN vt.id IS NULL THEN '' ELSE t.title END, CASE WHEN vt.id IS NULL THEN '' ELSE b.name END,
		p.body, p.author_id, CASE WHEN u.deleted THEN 'Former member' ELSE u.username END, p.created_at, p.edited_at, p.revision, p.removed,
		(SELECT count(*) FROM posts x WHERE x.topic_id = p.topic_id AND x.id <= p.id)
		FROM posts p JOIN topics t ON t.id = p.topic_id JOIN boards b ON b.id = t.board_id JOIN users u ON u.id=p.author_id LEFT JOIN visible_topics vt ON vt.id = t.id
		WHERE (p.author_id = ? OR ? = 0) ORDER BY p.topic_id, p.id`, append(reader, authorID, authorID), func(rows *sql.Rows) (p ContributionPost, err error) {
		return p, rows.Scan(&p.ID, &p.TopicID, &p.BoardID, &p.TopicTitle, &p.BoardName, &p.Body, &p.AuthorID, &p.Author, &p.CreatedAt, &p.EditedAt, &p.Revision, &p.Removed, &p.Number)
	})
	if err != nil {
		return out, err
	}
	index := make(map[int64]int, len(out.Posts)) // post ID to slice position
	for i, p := range out.Posts {
		index[p.ID] = i
	}
	type postAttachment struct {
		postID int64
		ContributionAttachment
	}
	attachments, err := collect(ctx, tx, `SELECT a.post_id, a.id, a.name, a.rendition, a.server, a.remote_id
		FROM attachments a JOIN posts p ON p.id = a.post_id
		WHERE (p.author_id = ? OR ? = 0) ORDER BY a.post_id, a.id`, []any{authorID, authorID}, func(rows *sql.Rows) (a postAttachment, err error) {
		return a, rows.Scan(&a.postID, &a.ID, &a.Name, &a.Rendition, &a.Server, &a.RemoteID)
	})
	for _, a := range attachments {
		if at, ok := index[a.postID]; ok && out.Posts[at].TopicTitle != "" {
			out.Posts[at].Attachments = append(out.Posts[at].Attachments, a.ContributionAttachment)
		}
	}
	if err != nil {
		return out, err
	}
	if authorID == 0 {
		out.Members, err = collect(ctx, tx, "SELECT id,username,role FROM users WHERE deleted=0 ORDER BY id", nil, func(rows *sql.Rows) (member exportMember, err error) {
			return member, rows.Scan(&member.ID, &member.Username, &member.Role)
		})
		if err != nil {
			return out, err
		}
		out.Policy, err = communityPolicy(ctx, tx)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

// collect runs a query and scans every row, closing the rows on every path.
func collect[T any](ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args []any, scan func(*sql.Rows) (T, error)) (out []T, err error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
