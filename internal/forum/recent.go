package forum

import "context"

// RecentPosts lists individual messages in active boards, newest first. The
// shared audience filter runs before pagination, so hidden posts cannot crowd
// out readable messages or disclose their existence through extra pages.
func (s *Store) RecentPosts(ctx context.Context, limit, offset int, reader *User) ([]Post, bool, error) {
	rows, err := s.db.QueryContext(ctx, visibleTopics+`, recent_page AS MATERIALIZED (
 SELECT p.id,p.topic_id FROM posts p JOIN visible_topics t ON t.id=p.topic_id
 JOIN visible_boards b ON b.id=t.board_id
 WHERE b.archived = 0 AND p.removed = 0
 ORDER BY p.created_at DESC,p.id DESC LIMIT ? OFFSET ?
), topic_counts AS MATERIALIZED (
 SELECT topic_id,count(*) AS total,min(id) AS first_id,max(id) AS last_id FROM posts
 WHERE topic_id IN (SELECT topic_id FROM recent_page) GROUP BY topic_id
)
SELECT p.id,p.topic_id,p.author_id,p.body,CASE WHEN u.deleted THEN 'Former member' ELSE u.username END,
 p.created_at,p.edited_at,p.revision,
 EXISTS(SELECT 1 FROM user_avatars va WHERE va.user_id = p.author_id),
 CASE WHEN p.id-counts.first_id < counts.last_id-p.id
 THEN (SELECT count(*) FROM posts earlier WHERE earlier.topic_id = p.topic_id AND earlier.id <= p.id)
 ELSE counts.total-(SELECT count(*) FROM posts later WHERE later.topic_id=p.topic_id AND later.id>p.id) END,
 t.title,t.board_id,b.name,t.audience,b.restricted,b.access
 FROM recent_page selected JOIN posts p ON p.id=selected.id JOIN users u ON u.id=p.author_id
 JOIN visible_topics t ON t.id=p.topic_id JOIN visible_boards b ON b.id=t.board_id
 JOIN topic_counts counts ON counts.topic_id=p.topic_id
 ORDER BY p.created_at DESC,p.id DESC`, append(readerArgs(reader), limit+1, offset)...)
	if err != nil {
		return nil, false, err
	}
	defer closeRows(rows)
	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.TopicID, &p.AuthorID, &p.Body, &p.Author,
			&p.CreatedAt, &p.EditedAt, &p.Revision, &p.HasAvatar, &p.Number,
			&p.Topic.Title, &p.Topic.BoardID, &p.Topic.BoardName, &p.Topic.Audience,
			&p.Topic.BoardRestricted, &p.Topic.BoardAccess); err != nil {
			return nil, false, err
		}
		p.Topic.ID = p.TopicID
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(posts) > limit
	if more {
		posts = posts[:limit]
	}
	return posts, more, nil
}
