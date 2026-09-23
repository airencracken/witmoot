package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func createTestGroup(t *testing.T, f accessFixture, name string, members ...int64) Group {
	t.Helper()
	id, err := f.app.store.SaveGroup(context.Background(), f.owner.ID, Group{Name: name, Description: "Our people"}, members)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.app.store.Group(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func setTestGrants(t *testing.T, f accessFixture, members, groups map[int64]string) Board {
	t.Helper()
	ctx := context.Background()
	b, err := f.app.store.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.store.SaveBoard(ctx, f.owner.ID, b, members, groups); err != nil {
		t.Fatal(err)
	}
	b.Revision++
	return b
}

func TestGroupGrantsAndIndividualOverridesProtectEveryRead(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, context.Background()
	quiet := &User{ID: testMember(t, s, "quiet"), Role: "member"}
	solo := &User{ID: testMember(t, s, "solo"), Role: "member"}
	unlisted := &User{ID: testMember(t, s, "unlisted"), Role: "member"}
	readers := createTestGroup(t, f, "Readers", f.writer.ID, f.reader.ID, quiet.ID)
	writers := createTestGroup(t, f, "Writers", f.writer.ID, f.reader.ID, f.outsider.ID)
	grants := map[int64]string{readers.ID: "read", writers.ID: "write"}
	overrides := map[int64]string{f.reader.ID: "read", f.outsider.ID: "none", solo.ID: "write"}
	setTestGrants(t, f, overrides, grants)
	for _, tc := range []struct {
		name, access string
		user         *User
	}{
		{"owner", "write", f.owner}, {"multiple groups", "write", f.writer},
		{"individual read", "read", f.reader}, {"individual denial", "none", f.outsider},
		{"group read", "read", quiet}, {"individual write", "write", solo},
		{"no grants", "none", unlisted}, {"guest", "none", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowed := tc.access != "none"
			b, err := s.Board(ctx, f.boardID, tc.user)
			if allowed && (err != nil || b.Access != tc.access) || !allowed && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("board permission: %+v %v", b, err)
			}
			c := sessionClient(t, f.app, tc.user)
			for _, path := range []string{"/", "/recent", "/search?q=Secret"} {
				w := c.request("GET", path, nil, nil)
				requireStatus(t, w, 200)
				needle := "Secret party plans"
				if path == "/" {
					needle = "Hidden planning room"
				}
				if strings.Contains(w.Body.String(), needle) != allowed {
					t.Fatalf("visibility at %s: %s", path, w.Body.String())
				}
			}
			for _, path := range []string{fmt.Sprintf("/boards/%d", f.boardID), fmt.Sprintf("/topics/%d", f.topicID)} {
				status := 404
				if allowed {
					status = 200
				}
				w := c.request("GET", path, nil, nil)
				requireStatus(t, w, status)
				if strings.HasPrefix(path, "/topics/") && strings.Contains(w.Body.String(), "Post reply</button>") != (tc.access == "write") {
					t.Fatal("reply controls disagree with effective permission")
				}
			}
			_, err = s.Attachment(ctx, f.imageID, tc.user)
			if allowed && err != nil || !allowed && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("image permission: %v", err)
			}
			stats, err := s.Stats(ctx, tc.user)
			want := 0
			if allowed {
				want = 1
			}
			if err != nil || stats.Topics != want || stats.Posts != want {
				t.Fatalf("counts leaked or duplicated through overlapping groups: %+v %v", stats, err)
			}
		})
	}
	if _, _, err := s.Reply(ctx, f.topicID, f.reader.ID, "Group must not bypass read override"); !errors.Is(err, errReadOnly) {
		t.Fatalf("read override ignored: %v", err)
	}
	if _, err := s.CreateTopic(ctx, f.boardID, f.outsider.ID, "Hidden board", "Must not post", AudienceMembers); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deny override ignored: %v", err)
	}
	overrides[f.writer.ID] = "read"
	setTestGrants(t, f, overrides, grants)
	if _, err := s.EditPost(ctx, f.postID, f.writer.ID, "Cannot edit through a group", 0); !errors.Is(err, errReadOnly) {
		t.Fatalf("group bypassed read-only edit restriction: %v", err)
	}
	overrides[f.writer.ID] = "inherit"
	setTestGrants(t, f, overrides, grants)
	if _, err := s.EditPost(ctx, f.postID, f.writer.ID, "Posting restored by groups", 0); err != nil {
		t.Fatal(err)
	}
}

func TestGroupReportShowsOverridesAndEffectiveAccess(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, context.Background()
	g := createTestGroup(t, f, "Friends", f.writer.ID, f.reader.ID, f.outsider.ID)
	setTestGrants(t, f, map[int64]string{f.reader.ID: "read", f.outsider.ID: "none"}, map[int64]string{g.ID: "write"})
	check := func(want map[string]string) {
		t.Helper()
		boards, err := s.GroupBoards(ctx, g.ID)
		if err != nil || len(boards) != 1 || len(boards[0].Members) != 3 {
			t.Fatalf("group report: %+v %v", boards, err)
		}
		for _, m := range boards[0].Members {
			if m.Effective != want[m.Username] {
				t.Fatalf("wrong effective access: %+v, want %s", m, want[m.Username])
			}
			wantOverride := map[string]string{"writer": "inherit", "reader": "read", "outsider": "none"}[m.Username]
			if m.Override != wantOverride {
				t.Fatalf("hidden individual override: %+v", m)
			}
		}
	}
	check(map[string]string{"writer": "write", "reader": "read", "outsider": "none"})
	owner := sessionClient(t, f.app, f.owner)
	w := owner.request("GET", fmt.Sprintf("/groups/%d", g.ID), nil, nil)
	requireStatus(t, w, 200)
	for _, text := range []string{"Current board access", "Individual override", "Effective board access", "No access", "Read only"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatalf("admin cannot see %q", text)
		}
	}
	b, err := s.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeBoard(ctx, f.owner.ID, b.ID, b.Revision, "archive", ""); err != nil {
		t.Fatal(err)
	}
	check(map[string]string{"writer": "read", "reader": "read", "outsider": "none"})
	if _, _, err := s.Reply(ctx, f.topicID, f.writer.ID, "Groups cannot unarchive"); !errors.Is(err, errArchived) {
		t.Fatalf("archive bypass: %v", err)
	}
	if err := s.SetMode(ctx, ModePersonal); err != nil {
		t.Fatal(err)
	}
	check(map[string]string{"writer": "none", "reader": "none", "outsider": "none"})
	if err := s.SetMode(ctx, ModeOpen); err != nil {
		t.Fatal(err)
	}
	b, err = s.Board(ctx, f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	b.Restricted = false
	if _, err := s.SaveBoard(ctx, f.owner.ID, b, map[int64]string{f.reader.ID: "read", f.outsider.ID: "none"}, map[int64]string{g.ID: "write"}); err != nil {
		t.Fatal(err)
	}
	check(map[string]string{"writer": "read", "reader": "read", "outsider": "read"})
}

func TestGroupMembershipChangesRevokeStaleFormsImmediately(t *testing.T) {
	f := privateBoardFixture(t)
	s, ctx := f.app.store, context.Background()
	g := createTestGroup(t, f, "Friends", f.writer.ID, f.reader.ID)
	b := setTestGrants(t, f, nil, map[int64]string{g.ID: "write"})
	var err error
	g, err = s.Group(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := sessionClient(t, f.app, f.writer)
	edit := fmt.Sprintf("/posts/%d/edit", f.postID)
	requireStatus(t, c.request("GET", edit, nil, nil), 200)
	if _, err := s.SaveGroup(ctx, f.owner.ID, g, []int64{f.reader.ID}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{edit, fmt.Sprintf("/topics/%d/replies", f.topicID), fmt.Sprintf("/boards/%d/new", f.boardID)} {
		requireStatus(t, c.post(path, url.Values{"title": {"Stale topic"}, "body": {"Stale message"}, "revision": {"0"}, "audience": {"members"}}), 404)
	}
	if _, err := s.Attachment(ctx, f.imageID, f.writer); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked member sees images: %v", err)
	}
	if _, err := s.SaveBoard(ctx, f.owner.ID, b, nil, map[int64]string{g.ID: "write"}); !errors.Is(err, errBoardConflict) {
		t.Fatalf("stale board form accepted after membership change: %v", err)
	}
	if _, err := s.SaveGroup(ctx, f.owner.ID, g, []int64{f.writer.ID}); !errors.Is(err, errGroupConflict) {
		t.Fatalf("stale group form accepted: %v", err)
	}
	if err := s.DeleteGroup(ctx, f.owner.ID, g.ID, g.Revision, g.Name); !errors.Is(err, errGroupConflict) {
		t.Fatalf("stale group deletion accepted: %v", err)
	}
	g, err = s.Group(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveGroup(ctx, f.owner.ID, g, []int64{f.writer.ID, f.reader.ID, f.outsider.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Reply(ctx, f.topicID, f.outsider.ID, "New member can join in"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode(ctx, ModePersonal); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Reply(ctx, f.topicID, f.outsider.ID, "Groups cannot bypass Personal mode"); !errors.Is(err, errPersonal) {
		t.Fatalf("group bypasses site mode: %v", err)
	}
}
