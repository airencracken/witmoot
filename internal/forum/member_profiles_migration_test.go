// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"testing"
)

func TestMemberProfileMigrationPreservesAccounts(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	id := testMember(t, s, "alice")
	var path, beforeName, beforeHash string
	if err := s.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT username,password_hash FROM users WHERE id=?", id).Scan(&beforeName, &beforeHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE member_profiles; PRAGMA user_version=18"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })
	var name, hash string
	if err := upgraded.db.QueryRow("SELECT username,password_hash FROM users WHERE id=?", id).Scan(&name, &hash); err != nil || name != beforeName || hash != beforeHash {
		t.Fatal("upgrade changed account identity", name, err)
	}
	p, err := upgraded.MemberProfile(ctx, id)
	if err != nil || p.Username != "alice" || p.Biography.Name != "" || p.Biography.Bio != "" || len(p.Biography.Links) != 0 {
		t.Fatal("upgrade populated optional profile fields", p, err)
	}
	var profiles int
	if err = upgraded.db.QueryRow("SELECT count(*) FROM member_profiles").Scan(&profiles); err != nil || profiles != 0 {
		t.Fatal("upgrade created unwanted biographies", profiles, err)
	}
}
