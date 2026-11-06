// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"context"
	"errors"
	"time"

	"github.com/airencracken/comfylib/setup"
)

// HasOwner includes suspended owners so setup never reopens an existing instance.
func (s *Store) HasOwner(ctx context.Context) (bool, error) {
	var present bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE role='owner')").Scan(&present)
	return present, err
}

// CreateFirstOwner creates the browser setup account with an atomic owner guard.
func (s *Store) CreateFirstOwner(ctx context.Context, owner setup.Owner) error {
	if message := ValidateCredentials(owner.Username, owner.Password); message != "" {
		return errors.New(message)
	}
	hash, err := HashPassword(owner.Password)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var present bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE role='owner')").Scan(&present); err != nil {
		return err
	}
	if present {
		return setup.ErrComplete
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,'owner',?)", owner.Username, hash, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
