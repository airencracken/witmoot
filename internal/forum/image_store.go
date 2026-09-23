package forum

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

type Connection struct {
	Username, Server string
	Token            []byte
}
type Attachment struct {
	ID, PostID                        int64
	Server, RemoteID, Name, Rendition string
	CredentialUserID                  sql.NullInt64
}

func LoadImageKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		rand.Read(key)
		file, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(createErr, os.ErrExist) {
			return LoadImageKey(path)
		}
		if createErr != nil {
			return nil, createErr
		}
		_, writeErr := file.Write(key)
		closeErr := file.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("imvault.key must contain exactly 32 bytes; restore it from backup")
	}
	return key, nil
}

func imageCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (a *App) encryptImageToken(userID int64, token string) ([]byte, error) {
	c, err := imageCipher(a.config.ImageKey)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.NonceSize())
	rand.Read(nonce)
	return c.Seal(nonce, nonce, []byte(token), []byte(fmt.Sprintf("%d:%s", userID, a.vault.Base))), nil
}

func (a *App) imageToken(ctx context.Context, userID int64) (string, error) {
	connection, err := a.store.Connection(ctx, userID)
	if err != nil {
		return "", err
	}
	if a.vault == nil || connection.Server != a.vault.Base {
		return "", sql.ErrNoRows
	}
	c, err := imageCipher(a.config.ImageKey)
	if err != nil {
		return "", err
	}
	if len(connection.Token) < c.NonceSize() {
		return "", errors.New("invalid stored imvault token")
	}
	plain, err := c.Open(nil, connection.Token[:c.NonceSize()], connection.Token[c.NonceSize():], []byte(fmt.Sprintf("%d:%s", userID, a.vault.Base)))
	return string(plain), err
}

func (s *Store) Connection(ctx context.Context, userID int64) (Connection, error) {
	var c Connection
	err := s.db.QueryRowContext(ctx, "SELECT server, username, token FROM imvault_connections WHERE user_id = ?", userID).Scan(&c.Server, &c.Username, &c.Token)
	return c, err
}

func (s *Store) ConnectImages(ctx context.Context, userID int64, c Connection) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO imvault_connections(user_id, server, username, token) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET server = excluded.server, username = excluded.username, token = excluded.token`, userID, c.Server, c.Username, c.Token)
	return err
}

func (s *Store) DisconnectImages(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM imvault_connections WHERE user_id = ?", userID)
	return err
}

func attachImages(ctx context.Context, tx *sql.Tx, postID int64, images []Attachment) error {
	for _, img := range images {
		if _, err := tx.ExecContext(ctx, `INSERT INTO attachments(id, post_id, server, remote_id, credential_user_id, name, rendition) VALUES ((SELECT last_id + 1 FROM object_sequences WHERE kind = 'attachments'), ?, ?, ?, ?, ?, ?)`, postID, img.Server, img.RemoteID, img.CredentialUserID, img.Name, img.Rendition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Attachment(ctx context.Context, id int64, reader *User) (Attachment, error) {
	var img Attachment
	err := s.db.QueryRowContext(ctx, visibleTopics+`SELECT a.id, a.post_id, a.server, a.remote_id, a.credential_user_id, a.name, a.rendition FROM attachments a
		JOIN posts p ON p.id = a.post_id JOIN visible_topics t ON t.id = p.topic_id WHERE a.id = ?`, append(readerArgs(reader), id)...).Scan(&img.ID, &img.PostID, &img.Server, &img.RemoteID, &img.CredentialUserID, &img.Name, &img.Rendition)
	return img, err
}

func (s *Store) PostImages(ctx context.Context, posts []Post) error {
	for i := range posts {
		rows, err := s.db.QueryContext(ctx, "SELECT id, name FROM attachments WHERE post_id = ? ORDER BY id", posts[i].ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var img Attachment
			if err := rows.Scan(&img.ID, &img.Name); err != nil {
				rows.Close()
				return err
			}
			posts[i].Images = append(posts[i].Images, img)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
