package forum

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"witmoot/internal/instance"
)

func TestCLIRefusesMigrationAndSnapshotKeepsOriginalSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "witmoot.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(string(migration) + "; PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	closeTest(t, old)
	if current, err := OpenCurrentStore(path); err == nil {
		closeTest(t, current)
		t.Fatal("CLI migrated without being asked")
	}
	server, err := instance.AcquireServer(path)
	if err != nil {
		t.Fatal(err)
	}
	if migration, err := instance.Acquire(path, true); err == nil {
		closeTest(t, migration)
		t.Fatal("migration admitted while server is running")
	}
	closeTest(t, server)
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	closeTest(t, store)
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "witmoot-before-v1-*", "witmoot.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("migration backups: %v %v", backups, err)
	}
	backup, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, backup)
	var version int
	if err := backup.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("backup migrated: %d %v", version, err)
	}
	info, err := os.Stat(backups[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("backup credentials are not private", err)
	}
	var count int
	if err := backup.QueryRow("SELECT count(*) FROM boards").Scan(&count); err != nil || count != 5 {
		t.Fatal("backup lost existing data", err)
	}
	current, err := OpenCurrentStore(path)
	if err != nil {
		t.Fatal(err)
	}
	closeTest(t, current)
	if _, err := os.Stat(backups[0]); errors.Is(err, os.ErrNotExist) {
		t.Fatal("ordinary CLI erased pre-migration snapshot")
	}
}
