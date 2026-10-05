package forum

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

var migrationFiles = []string{"001_initial.sql", "002_access_modes.sql", "003_imvault.sql", "004_invitations.sql", "005_board_access_and_edits.sql", "006_board_lifecycle.sql", "007_user_groups.sql", "008_invite_attribution.sql", "009_instance_branding.sql", "010_auth_tokens.sql", "011_user_email.sql", "012_user_avatars.sql", "013_community_care.sql", "014_moderation_log.sql", "015_account_deletion.sql"}

func (s *Store) backupBeforeMigration(database string) (err error) {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 0 || version >= len(migrationFiles) {
		return nil
	}
	directory, err := os.MkdirTemp(filepath.Dir(database), fmt.Sprintf("witmoot-before-v%d-", version))
	if err != nil {
		return fmt.Errorf("create pre-migration backup: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			err = errors.Join(err, os.RemoveAll(directory))
		}
	}()
	snapshot := filepath.Join(directory, "witmoot.db")
	if _, err := s.db.Exec("VACUUM INTO ?", snapshot); err != nil {
		return fmt.Errorf("pre-migration snapshot: %w", err)
	}
	if err := os.Chmod(snapshot, 0o600); err != nil {
		return err
	}
	file, err := os.Open(snapshot)
	if err != nil {
		return err
	}
	err = errors.Join(file.Sync(), file.Close())
	if err != nil {
		return err
	}
	for _, path := range []string{directory, filepath.Dir(directory)} {
		dir, err := os.Open(path)
		if err != nil {
			return err
		}
		err = errors.Join(dir.Sync(), dir.Close())
		if err != nil {
			return err
		}
	}
	complete = true
	slog.Info("Pre-migration database backup saved", "path", snapshot, "schema", version)
	return nil
}
