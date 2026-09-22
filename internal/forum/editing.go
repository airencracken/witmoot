package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var errPostBody = errors.New("write a message between 1 and 20,000 characters")

func readPost(ctx context.Context, q rowQuerier, id int64, reader *User) (Post, error) {
	var p Post
	err := q.QueryRowContext(ctx, visibleTopics+`SELECT p.id, p.topic_id, p.author_id, p.body, p.edited_at, p.revision,
		(SELECT count(*) FROM posts earlier WHERE earlier.topic_id = p.topic_id AND earlier.id <= p.id)
		FROM posts p JOIN visible_topics t ON t.id = p.topic_id WHERE p.id = ?`, append(readerArgs(reader), id)...).Scan(&p.ID, &p.TopicID, &p.AuthorID, &p.Body, &p.EditedAt, &p.Revision, &p.Number)
	return p, err
}

func (s *Store) EditPost(ctx context.Context, id, authorID int64, body string, revision int64) (Post, error) {
	body = strings.TrimSpace(body)
	if !validText(body, 1, 20000) {
		return Post{}, errPostBody
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	user := User{ID: authorID}
	if err := tx.QueryRowContext(ctx, "SELECT role FROM users WHERE id = ?", authorID).Scan(&user.Role); err != nil {
		return Post{}, err
	}
	p, err := readPost(ctx, tx, id, &user)
	if err != nil {
		return Post{}, err
	}
	if p.AuthorID != authorID {
		return Post{}, sql.ErrNoRows
	}
	var boardID int64
	if err := tx.QueryRowContext(ctx, "SELECT board_id FROM topics WHERE id = ?", p.TopicID).Scan(&boardID); err != nil {
		return Post{}, err
	}
	if _, _, err := boardForWriter(ctx, tx, boardID, authorID); err != nil {
		return Post{}, err
	}
	if p.Revision != revision {
		return Post{}, errEditConflict
	}
	if p.Body != body {
		p.EditedAt = time.Now().Unix()
		p.Revision++
		p.Body = body
		if _, err := tx.ExecContext(ctx, "UPDATE posts SET body = ?, edited_at = ?, revision = ? WHERE id = ?", body, p.EditedAt, p.Revision, id); err != nil {
			return Post{}, err
		}
	}
	return p, tx.Commit()
}

func (a *App) editablePost(w http.ResponseWriter, r *http.Request) (Post, Topic, bool) {
	p, err := readPost(r.Context(), a.store.db, pathID(r), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return p, Topic{}, false
	}
	if p.AuthorID != state(r).User.ID {
		a.storeError(w, r, sql.ErrNoRows)
		return p, Topic{}, false
	}
	t, err := a.store.Topic(r.Context(), p.TopicID, state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return p, t, false
	}
	if t.BoardAccess != "write" {
		a.fail(w, r, 403, errReadOnly.Error())
		return p, t, false
	}
	return p, t, true
}

func (a *App) editPostForm(w http.ResponseWriter, r *http.Request) {
	p, t, ok := a.editablePost(w, r)
	if !ok {
		return
	}
	a.render(w, r, 200, Page{View: "edit-post", Title: "Edit your message", Post: p, Topic: t, BodyInput: p.Body})
}

func (a *App) editPost(w http.ResponseWriter, r *http.Request) {
	p, t, ok := a.editablePost(w, r)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
	if err != nil || revision < 0 {
		a.fail(w, r, 400, "Reload the message before editing.")
		return
	}
	body := strings.TrimSpace(r.PostForm.Get("body"))
	updated, err := a.store.EditPost(r.Context(), p.ID, state(r).User.ID, body, revision)
	if err != nil {
		if errors.Is(err, errPostBody) || errors.Is(err, errEditConflict) {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, errEditConflict) {
				status = http.StatusConflict
			}
			p.Revision = revision
			a.render(w, r, status, Page{View: "edit-post", Title: "Edit your message", Post: p, Topic: t, BodyInput: body, Error: err.Error()})
			return
		}
		a.storeError(w, r, err)
		return
	}
	a.redirect(w, r, updated.URL())
}

func (p Post) URL() string {
	return fmt.Sprintf("/topics/%d?page=%d#post-%d", p.TopicID, (p.Number-1)/pageSize+1, p.ID)
}
