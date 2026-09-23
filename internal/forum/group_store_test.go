package forum

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroupValidationAndBoardGrantsAreAtomic(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, context.Background()
	g := createTestGroup(t, f, "Friends", f.writer.ID)
	createTestGroup(t, f, "Family", f.reader.ID)
	b := setTestGrants(t, f, nil, map[int64]string{g.ID: "write"})
	var err error
	g, err = s.Group(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, description string
		members           []int64
		want              error
	}{
		{"", "", nil, errGroupDetails}, {strings.Repeat("x", 81), "", nil, errGroupDetails},
		{"Name\nwith newline", "", nil, errGroupDetails}, {"Name\x00", "", nil, errGroupDetails},
		{string([]byte{0xff}), "", nil, errGroupDetails}, {"Fine name", strings.Repeat("x", 501), nil, errGroupDetails},
		{"family", "Duplicate", nil, errGroupName}, {"Changed name", "Changed description", []int64{f.owner.ID}, errGroupMembers},
		{"Changed name", "Changed description", []int64{f.reader.ID, 99999}, errGroupMembers},
		{"Changed name", "Changed description", []int64{f.reader.ID, f.reader.ID}, errGroupMembers},
	} {
		changed := g
		changed.Name, changed.Description = tc.name, tc.description
		if _, err := s.SaveGroup(ctx, f.owner.ID, changed, tc.members); !errors.Is(err, tc.want) {
			t.Fatalf("invalid group accepted: %q: %v", tc.name, err)
		}
		actual, err := s.Group(ctx, g.ID)
		if err != nil || actual != g {
			t.Fatalf("partial group update: %+v %v", actual, err)
		}
		actualBoard, err := s.Board(ctx, b.ID, f.writer)
		if err != nil || actualBoard.Access != "write" || actualBoard.Revision != b.Revision {
			t.Fatalf("failed group change affected access: %+v %v", actualBoard, err)
		}
	}
	for _, grants := range []map[int64]string{{99999: "write"}, {g.ID: "inherit"}, {g.ID: "owner"}} {
		if _, err := s.SaveBoard(ctx, f.owner.ID, b, map[int64]string{f.writer.ID: "none"}, grants); !errors.Is(err, errBoardAccess) {
			t.Fatalf("invalid group grant accepted: %v", err)
		}
		actual, err := s.Board(ctx, b.ID, f.writer)
		if err != nil || actual.Access != "write" || actual.Revision != b.Revision {
			t.Fatalf("partial board permission change: %+v %v", actual, err)
		}
		actualGroup, err := s.Group(ctx, g.ID)
		if err != nil || actualGroup.Revision != g.Revision {
			t.Fatalf("failed board save changed group revision: %+v %v", actualGroup, err)
		}
	}
	if _, err := s.SaveGroup(ctx, f.writer.ID, g, nil); !errors.Is(err, errOwner) {
		t.Fatalf("member changed group: %v", err)
	}
	if err := s.DeleteGroup(ctx, f.writer.ID, g.ID, g.Revision, g.Name); !errors.Is(err, errOwner) {
		t.Fatalf("member deleted group: %v", err)
	}
}

func TestGroupDeletionPreservesPeopleContentAndOtherGrants(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, context.Background()
	g := createTestGroup(t, f, "Friends", f.writer.ID, f.reader.ID, f.outsider.ID)
	other := createTestGroup(t, f, "Readers", f.reader.ID)
	b := setTestGrants(t, f, map[int64]string{f.writer.ID: "write"}, map[int64]string{g.ID: "write", other.ID: "read"})
	if err := s.DeleteGroup(ctx, f.owner.ID, g.ID, g.Revision, g.Name); !errors.Is(err, errGroupConflict) {
		t.Fatalf("old confirmation did not notice changed board grants: %v", err)
	}
	g, err := s.Group(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(ctx, f.owner.ID, g.ID, g.Revision, "Wrong name"); !errors.Is(err, errGroupConfirmation) {
		t.Fatalf("missing exact-name confirmation: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_group_delete BEFORE DELETE ON user_groups BEGIN SELECT RAISE(ABORT, 'simulated failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(ctx, f.owner.ID, g.ID, g.Revision, g.Name); err == nil {
		t.Fatal("simulated delete failure ignored")
	}
	after, err := s.Group(ctx, g.ID)
	if err != nil || after != g {
		t.Fatalf("failed deletion changed group: %+v %v", after, err)
	}
	actualBoard, err := s.Board(ctx, b.ID, f.outsider)
	if err != nil || actualBoard.Revision != b.Revision || actualBoard.Access != "write" {
		t.Fatalf("failed deletion changed board: %+v %v", actualBoard, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_group_delete"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(ctx, f.owner.ID, g.ID, g.Revision, g.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Group(ctx, g.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("group still exists: %v", err)
	}
	for _, tc := range []struct {
		user   *User
		access string
	}{{f.writer, "write"}, {f.reader, "read"}} {
		b, err := s.Board(ctx, b.ID, tc.user)
		if err != nil || b.Access != tc.access {
			t.Fatalf("unrelated grant lost: %+v %v", b, err)
		}
	}
	if _, err := s.Board(ctx, b.ID, f.outsider); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted group still grants access: %v", err)
	}
	stats, err := s.Stats(ctx, f.owner)
	if err != nil || stats.Topics != 1 || stats.Posts != 1 || stats.Members != 4 {
		t.Fatalf("group deletion removed content or people: %+v %v", stats, err)
	}
	if _, err := s.Attachment(ctx, f.imageID, f.reader); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"group_members", "board_groups"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE group_id = ?", g.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("orphaned %s: %d %v", table, count, err)
		}
	}
	if err := s.DeleteGroup(ctx, f.owner.ID, other.ID, 1, other.Name); err != nil {
		t.Fatal(err)
	}
	newGroup := createTestGroup(t, f, "New circle")
	if newGroup.ID <= other.ID {
		t.Fatal("deleted group URL was reused")
	}
}

func TestGroupMigrationPreservesExistingIndividualPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"001_initial.sql", "002_access_modes.sql", "003_imvault.sql", "004_invitations.sql", "005_board_access_and_edits.sql", "006_board_lifecycle.sql"} {
		migration, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 6;
		INSERT INTO users(id, username, password_hash, role, created_at) VALUES (1, 'host', 'hash', 'owner', 1), (2, 'reader', 'hash', 'member', 1);
		UPDATE boards SET restricted = 1 WHERE id = 1;
		INSERT INTO board_members VALUES (1, 2, 'read');
		INSERT INTO topics(id, board_id, author_id, title, created_at, updated_at) VALUES (1, 1, 1, 'Existing conversation', 1, 1);
		INSERT INTO posts(id, topic_id, author_id, body, created_at) VALUES (1, 1, 1, 'Keep this message', 1);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, reader := context.Background(), &User{ID: 2, Role: "member"}
	b, err := s.Board(ctx, 1, reader)
	if err != nil || b.Access != "read" {
		t.Fatalf("migration changed existing access: %+v %v", b, err)
	}
	id, err := s.SaveGroup(ctx, 1, Group{Name: "Friends"}, []int64{2})
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"read", "none", "inherit"} {
		if _, err := s.SaveBoard(ctx, 1, b, map[int64]string{2: level}, map[int64]string{id: "write"}); err != nil {
			t.Fatal(err)
		}
		b.Revision++
		actual, err := s.Board(ctx, 1, reader)
		if level == "none" {
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("explicit denial not enforced after upgrade: %v", err)
			}
		} else {
			want := "read"
			if level == "inherit" {
				want = "write"
			}
			if err != nil || actual.Access != want {
				t.Fatalf("upgraded permission: %+v %v", actual, err)
			}
		}
	}
	posts, _, err := s.Posts(ctx, 1, 20, 0, reader)
	if err != nil || len(posts) != 1 || posts[0].Body != "Keep this message" {
		t.Fatalf("migration lost content: %+v %v", posts, err)
	}
	if _, err := s.db.Exec("INSERT INTO board_members VALUES (1, 2, 'owner')"); err == nil {
		t.Fatal("invalid permission accepted by schema")
	}
}
