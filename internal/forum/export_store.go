package forum

import (
	"context"
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
	CreatedAt, EditedAt                int64
	Attachments                        []ContributionAttachment
}

// ContributionAttachment records a shared image by reference. Witmoot stores
// only a preview link into imvault; the original stays with its owner there.
type ContributionAttachment struct {
	Name, Rendition, Server, RemoteID string
}

// Contributions is everything one member has written, gathered in one pass.
type Contributions struct {
	Boards []ContributionBoard
	Topics []ContributionTopic
	Posts  []ContributionPost
}

// MemberContributions collects a member's own messages and the context needed
// to understand them. It deliberately returns only this member's post bodies:
// a member export must not include other people's words.
func (s *Store) MemberContributions(ctx context.Context, userID int64) (Contributions, error) {
	out := Contributions{}

	boards, err := s.db.QueryContext(ctx, `SELECT b.id, b.category, b.name FROM boards b
		WHERE b.id IN (SELECT t.board_id FROM posts p JOIN topics t ON t.id = p.topic_id WHERE p.author_id = ?)
		ORDER BY b.position`, userID)
	if err != nil {
		return out, err
	}
	for boards.Next() {
		var b ContributionBoard
		if err := boards.Scan(&b.ID, &b.Category, &b.Name); err != nil {
			boards.Close()
			return out, err
		}
		out.Boards = append(out.Boards, b)
	}
	if err := boards.Err(); err != nil {
		boards.Close()
		return out, err
	}
	boards.Close()

	topics, err := s.db.QueryContext(ctx, `SELECT t.id, t.board_id, t.title, t.audience, t.created_at, t.updated_at,
		(SELECT count(*) FROM posts x WHERE x.topic_id = t.id)
		FROM topics t WHERE t.id IN (SELECT p.topic_id FROM posts p WHERE p.author_id = ?)
		ORDER BY t.id`, userID)
	if err != nil {
		return out, err
	}
	for topics.Next() {
		var t ContributionTopic
		if err := topics.Scan(&t.ID, &t.BoardID, &t.Title, &t.Audience, &t.CreatedAt, &t.UpdatedAt, &t.Posts); err != nil {
			topics.Close()
			return out, err
		}
		out.Topics = append(out.Topics, t)
	}
	if err := topics.Err(); err != nil {
		topics.Close()
		return out, err
	}
	topics.Close()

	posts, err := s.db.QueryContext(ctx, `SELECT p.id, p.topic_id, t.board_id, t.title, b.name, p.body, p.created_at, p.edited_at, p.revision,
		(SELECT count(*) FROM posts x WHERE x.topic_id = p.topic_id AND x.id <= p.id)
		FROM posts p JOIN topics t ON t.id = p.topic_id JOIN boards b ON b.id = t.board_id
		WHERE p.author_id = ? ORDER BY p.topic_id, p.id`, userID)
	if err != nil {
		return out, err
	}
	index := make(map[int64]int) // post ID to slice position
	for posts.Next() {
		var p ContributionPost
		if err := posts.Scan(&p.ID, &p.TopicID, &p.BoardID, &p.TopicTitle, &p.BoardName, &p.Body, &p.CreatedAt, &p.EditedAt, &p.Revision, &p.Number); err != nil {
			posts.Close()
			return out, err
		}
		index[p.ID] = len(out.Posts)
		out.Posts = append(out.Posts, p)
	}
	if err := posts.Err(); err != nil {
		posts.Close()
		return out, err
	}
	posts.Close()

	attachments, err := s.db.QueryContext(ctx, `SELECT a.post_id, a.name, a.rendition, a.server, a.remote_id
		FROM attachments a JOIN posts p ON p.id = a.post_id
		WHERE p.author_id = ? ORDER BY a.post_id, a.id`, userID)
	if err != nil {
		return out, err
	}
	defer attachments.Close()
	for attachments.Next() {
		var postID int64
		var a ContributionAttachment
		if err := attachments.Scan(&postID, &a.Name, &a.Rendition, &a.Server, &a.RemoteID); err != nil {
			return out, err
		}
		if at, ok := index[postID]; ok {
			out.Posts[at].Attachments = append(out.Posts[at].Attachments, a)
		}
	}
	return out, attachments.Err()
}
