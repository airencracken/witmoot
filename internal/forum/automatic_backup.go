// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/airencracken/comfylib/autobackup"
)

// AutomaticSnapshot preserves the database and the existing Imvault connection
// key, when present. A consistent SQLite snapshot includes committed WAL data.
func (s *Store) AutomaticSnapshot(ctx context.Context, dataDir, destination string, limit int64) error {
	if err := autobackup.CheckDatabase(ctx, s.db, limit-32); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	path := filepath.Join(destination, "witmoot.db")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if err := validateBackup(ctx, path); err != nil {
		return err
	}
	key := filepath.Join(dataDir, "imvault.key")
	info, err := os.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return validateSnapshotConnections(ctx, path, nil)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != 32 {
		return errors.New("Imvault connection key must be a regular 32-byte file")
	}
	copied := filepath.Join(destination, "imvault.key")
	if err := autobackup.CopyPrivate(ctx, key, copied, 32); err != nil {
		return err
	}
	keyBytes, err := os.ReadFile(copied)
	if err != nil {
		return err
	}
	return validateSnapshotConnections(ctx, path, keyBytes)
}

func validateSnapshotConnections(ctx context.Context, path string, key []byte) (err error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	rows, err := db.QueryContext(ctx, "SELECT user_id,server,token FROM imvault_connections")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var id int64
		var server string
		var token []byte
		if err := rows.Scan(&id, &server, &token); err != nil {
			return err
		}
		c, err := imageCipher(key)
		if err != nil {
			return errors.New("Imvault connections have no valid encryption key")
		}
		if len(token) < c.NonceSize() {
			return errors.New("invalid stored Imvault connection")
		}
		if _, err := c.Open(nil, token[:c.NonceSize()], token[c.NonceSize():], []byte(fmt.Sprintf("%d:%s", id, server))); err != nil {
			return errors.New("Imvault connection key cannot decrypt the snapshot")
		}
	}
	return rows.Err()
}

func validateBackup(ctx context.Context, path string) (err error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("backup database failed integrity check")
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if rows.Next() {
		return errors.New("backup database has broken foreign keys")
	}
	return rows.Err()
}
