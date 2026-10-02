package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type communityFixture struct {
	s                          *Store
	owner, member, topic, post int64
}

func newCommunityFixture(t *testing.T) communityFixture {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	owner := testInvitationOwner(t, s, "owner")
	member := testMember(t, s, "jules")
	if _, err := s.db.Exec("UPDATE users SET can_invite = 1 WHERE id = ?", member); err != nil {
		t.Fatal(err)
	}
	topic, err := s.CreateTopic(ctx, 1, member, "A private plan", "Original secret message", AudienceMembers,
		Attachment{Server: "https://photos.example.org", RemoteID: "secret-image", CredentialUserID: sql.NullInt64{Int64: member, Valid: true}, Name: "Private photo", Rendition: "thumb"})
	if err != nil {
		t.Fatal(err)
	}
	var post int64
	if err := s.db.QueryRow("SELECT id FROM posts WHERE topic_id = ?", topic).Scan(&post); err != nil {
		t.Fatal(err)
	}
	if err := s.NewSession(ctx, "old-session", member, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuthToken(ctx, member, TokenPasswordReset, "old-reset", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInvitation(ctx, member, "old-invite", "old", InvitationOptions{MaxUses: 0}); err != nil {
		t.Fatal(err)
	}
	return communityFixture{s: s, owner: owner, member: member, topic: topic, post: post}
}

func TestSuspensionRevokesAccessAndRestorePreservesContributions(t *testing.T) {
	f := newCommunityFixture(t)
	ctx := context.Background()
	if err := f.s.ChangeMember(ctx, f.owner, f.member, 0, "suspend", "jules"); err != nil {
		t.Fatal(err)
	}
	u, _, err := f.s.Credentials(ctx, "jules")
	if err != nil || !u.Suspended || u.SuspensionRevision != 1 {
		t.Fatalf("suspended credentials: %+v %v", u, err)
	}
	if session, err := f.s.Session(ctx, "old-session"); err != nil || session != nil {
		t.Fatalf("old session: %+v %v", session, err)
	}
	for name, err := range map[string]error{
		"new session": f.s.NewSession(ctx, "new-session", f.member, time.Now().Add(time.Hour)),
		"new reset":   f.s.CreateAuthToken(ctx, f.member, TokenPasswordReset, "new-reset", time.Now().Add(time.Hour)),
	} {
		if !errors.Is(err, errSuspended) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := f.s.ResetPassword(ctx, "old-reset", "changed", time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("reset bypass: %v", err)
	}
	if _, err := f.s.AuthTokenValid(ctx, "old-reset", TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("reset form bypass: %v", err)
	}
	if _, _, err := f.s.Reply(ctx, f.topic, f.member, "Stale signed-in request"); !errors.Is(err, errSuspended) {
		t.Fatalf("reply bypass: %v", err)
	}
	if _, err := f.s.EditPost(ctx, f.post, f.member, "Stale edit", 0); !errors.Is(err, errSuspended) {
		t.Fatalf("edit bypass: %v", err)
	}
	if _, err := f.s.CreateTopic(ctx, 1, f.member, "Another topic", "Stale form", AudienceMembers); !errors.Is(err, errSuspended) {
		t.Fatalf("topic bypass: %v", err)
	}
	staleReader := &User{ID: f.member, Role: "member"}
	if _, err := f.s.Topic(ctx, f.topic, staleReader); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale request read private topic: %v", err)
	}
	if posts, _, err := f.s.Posts(ctx, f.topic, 20, 0, staleReader); err != nil || len(posts) != 0 {
		t.Fatalf("stale request read private posts: %+v %v", posts, err)
	}
	if _, err := f.s.CreateInvitation(ctx, f.member, "new-invite", "new", InvitationOptions{}); err == nil {
		t.Fatal("suspended member issued invitation")
	}
	if _, err := f.s.Register(ctx, "outsider", "hash", "old-invite"); !errors.Is(err, errInvitation) {
		t.Fatalf("suspended member's invitation: %v", err)
	}
	if err := f.s.ChangeMember(ctx, f.owner, f.member, 1, "restore", "jules"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.NewSession(ctx, "restored-session", f.member, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if session, _ := f.s.Session(ctx, "old-session"); session != nil {
		t.Fatal("restoration revived old session")
	}
	if _, err := f.s.AuthTokenValid(ctx, "old-reset", TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatal("restoration revived old reset link")
	}
	if _, err := f.s.Register(ctx, "outsider", "hash", "old-invite"); !errors.Is(err, errInvitation) {
		t.Fatal("restoration revived old invitation")
	}
	posts, _, err := f.s.Posts(ctx, f.topic, 20, 0, testReader)
	if err != nil || len(posts) != 1 || posts[0].Body != "Original secret message" {
		t.Fatalf("contributions changed: %+v %v", posts, err)
	}
	if events, err := f.s.CommunityEvents(ctx); err != nil || len(events) != 2 || events[0].Actor != "owner" || events[0].Action != "restore" {
		t.Fatalf("events: %+v %v", events, err)
	}
}

func TestMemberActionsRequireOwnerConfirmationAndCurrentRevision(t *testing.T) {
	f := newCommunityFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name                    string
		actor, target, revision int64
		action, confirmation    string
		want                    error
	}{
		{"member actor", f.member, f.member, 0, "suspend", "jules", errOwner},
		{"owner target", f.owner, f.owner, 0, "suspend", "owner", errMemberAction},
		{"missing target", f.owner, 999, 0, "suspend", "jules", sql.ErrNoRows},
		{"wrong name", f.owner, f.member, 0, "suspend", "JULES", errMemberConfirmation},
		{"stale", f.owner, f.member, 9, "suspend", "jules", errCommunityConflict},
		{"invalid action", f.owner, f.member, 0, "delete", "jules", errCommunityConflict},
		{"already active", f.owner, f.member, 0, "restore", "jules", errCommunityConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.s.ChangeMember(ctx, tc.actor, tc.target, tc.revision, tc.action, tc.confirmation); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	if err := f.s.ChangeMember(ctx, f.owner, f.member, 0, "suspend", "jules"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ChangeMember(ctx, f.owner, f.member, 1, "restore", "jules"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ChangeMember(ctx, f.owner, f.member, 0, "suspend", "jules"); !errors.Is(err, errCommunityConflict) {
		t.Fatal("old form accepted after suspend/restore")
	}
}

func TestSuspensionIsAtomicAtEveryWrite(t *testing.T) {
	for _, stage := range []struct{ name, event, table string }{
		{"account", "UPDATE", "users"}, {"sessions", "DELETE", "sessions"},
		{"resets", "DELETE", "auth_tokens"}, {"invitations", "UPDATE", "invitations"},
		{"activity", "INSERT", "community_events"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			f := newCommunityFixture(t)
			if _, err := f.s.db.Exec(fmt.Sprintf("CREATE TRIGGER fail_write BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'injected failure'); END", stage.event, stage.table)); err != nil {
				t.Fatal(err)
			}
			if err := f.s.ChangeMember(context.Background(), f.owner, f.member, 0, "suspend", "jules"); err == nil {
				t.Fatal("failure ignored")
			}
			var suspended, revision, sessions, tokens, revoked, events int
			if err := f.s.db.QueryRow(`SELECT suspended, suspension_revision,
				(SELECT count(*) FROM sessions), (SELECT count(*) FROM auth_tokens),
				(SELECT count(*) FROM invitations WHERE revoked_at IS NOT NULL),
				(SELECT count(*) FROM community_events) FROM users WHERE id = ?`, f.member).Scan(&suspended, &revision, &sessions, &tokens, &revoked, &events); err != nil {
				t.Fatal(err)
			}
			if suspended != 0 || revision != 0 || sessions != 1 || tokens != 1 || revoked != 0 || events != 0 {
				t.Fatalf("partial suspension: %d %d %d %d %d %d", suspended, revision, sessions, tokens, revoked, events)
			}
		})
	}
}

func TestConcurrentSuspensionAndSignInCannotRestoreAccess(t *testing.T) {
	f := newCommunityFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- f.s.ChangeMember(ctx, f.owner, f.member, 0, "suspend", "jules") }()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := f.s.NewSession(ctx, "racing-session", f.member, time.Now().Add(time.Hour))
		if err != nil && !errors.Is(err, errSuspended) {
			t.Error(err)
		}
	}()
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, errCommunityConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	if session, err := f.s.Session(ctx, "racing-session"); err != nil || session != nil {
		t.Fatalf("racing session survived: %+v %v", session, err)
	}
}

func TestRemovalErasesTextImagesAndSearchButKeepsReplyContext(t *testing.T) {
	f := newCommunityFixture(t)
	ctx := context.Background()
	reply, _, err := f.s.Reply(ctx, f.topic, f.member, "A useful reply")
	if err != nil {
		t.Fatal(err)
	}
	removed, err := f.s.RemovePost(ctx, f.owner, f.post, 0, true)
	if err != nil || !removed.Removed || removed.URL() != fmt.Sprintf("/topics/%d?page=1#post-%d", f.topic, f.post) {
		t.Fatalf("removal: %+v %v", removed, err)
	}
	posts, _, err := f.s.Posts(ctx, f.topic, 20, 0, testReader)
	if err != nil || len(posts) != 2 || posts[0].Body != removedMessage || posts[1].ID != reply || posts[1].Number != 2 {
		t.Fatalf("reply context: %+v %v", posts, err)
	}
	for _, query := range []string{"secret", "site owner"} {
		topics, _, err := f.s.Topics(ctx, 0, query, 20, 0, testReader)
		if err != nil || len(topics) != 0 {
			t.Fatalf("removed content searchable: %+v %v", topics, err)
		}
	}
	if _, err := f.s.Attachment(ctx, 1, testReader); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed image visible: %v", err)
	}
	if _, err := f.s.EditPost(ctx, f.post, f.member, "Revive removed text", 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("edit revived removed message: %v", err)
	}
	if _, err := f.s.RemovePost(ctx, f.owner, f.post, 1, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("repeat removal: %v", err)
	}
	export, err := f.s.MemberContributions(ctx, &User{ID: f.member, Role: "member"})
	if err != nil || len(export.Posts) != 2 || !export.Posts[0].Removed || export.Posts[0].Body != removedMessage || len(export.Posts[0].Attachments) != 0 || export.Posts[0].TopicTitle != "Conversation" {
		t.Fatalf("export leaked erased content: %+v %v", export, err)
	}
}

func TestRemovalConfirmationCannotDeleteNewEditsOrReplaceReplyTitle(t *testing.T) {
	f := newCommunityFixture(t)
	ctx := context.Background()
	if _, err := f.s.RemovePost(ctx, f.member, f.post, 0, false); !errors.Is(err, errOwner) {
		t.Fatal(err)
	}
	if _, err := f.s.EditPost(ctx, f.post, f.member, "A correction", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RemovePost(ctx, f.owner, f.post, 0, false); !errors.Is(err, errCommunityConflict) {
		t.Fatal("stale confirmation removed edited message")
	}
	reply, _, err := f.s.Reply(ctx, f.topic, f.member, "Reply")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RemovePost(ctx, f.owner, reply, 0, true); !errors.Is(err, errCommunityConflict) {
		t.Fatal("reply removed conversation title")
	}
	if _, err := f.s.RemovePost(ctx, f.owner, reply, 0, false); err != nil {
		t.Fatal(err)
	}
	topic, err := f.s.Topic(ctx, f.topic, testReader)
	if err != nil || topic.Title != "A private plan" {
		t.Fatalf("title changed: %+v %v", topic, err)
	}
}

func TestRemovalIsAtomicAtEveryWrite(t *testing.T) {
	for _, stage := range []struct{ name, event, table string }{
		{"images", "DELETE", "attachments"}, {"message", "UPDATE", "posts"},
		{"title", "UPDATE", "topics"}, {"activity", "INSERT", "community_events"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			f := newCommunityFixture(t)
			if _, err := f.s.db.Exec(fmt.Sprintf("CREATE TRIGGER fail_write BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'injected failure'); END", stage.event, stage.table)); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.RemovePost(context.Background(), f.owner, f.post, 0, true); err == nil {
				t.Fatal("failure ignored")
			}
			var body, title string
			var removed, revision, images, events int
			if err := f.s.db.QueryRow(`SELECT p.body, p.removed, p.revision, t.title,
				(SELECT count(*) FROM attachments), (SELECT count(*) FROM community_events)
				FROM posts p JOIN topics t ON t.id = p.topic_id WHERE p.id = ?`, f.post).Scan(&body, &removed, &revision, &title, &images, &events); err != nil {
				t.Fatal(err)
			}
			if body != "Original secret message" || removed != 0 || revision != 0 || title != "A private plan" || images != 1 || events != 0 {
				t.Fatalf("partial removal: %q %d %d %q %d %d", body, removed, revision, title, images, events)
			}
		})
	}
}

func TestCommunitySchemaUpgradeAndConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_initial.sql", "002_access_modes.sql", "003_imvault.sql", "004_invitations.sql", "005_board_access_and_edits.sql", "006_board_lifecycle.sql", "007_user_groups.sql", "008_invite_attribution.sql", "009_instance_branding.sql", "010_auth_tokens.sql", "011_user_email.sql", "012_user_avatars.sql"} {
		migration, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 12;
		INSERT INTO users(id, username, password_hash, role, created_at) VALUES (1, 'owner', 'hash', 'owner', 1), (2, 'jules', 'hash', 'member', 1);
		INSERT INTO topics(id, board_id, author_id, title, audience, created_at, updated_at) VALUES (1, 1, 2, 'Old conversation', 'members', 1, 1);
		INSERT INTO posts(id, topic_id, author_id, body, created_at) VALUES (1, 1, 2, 'Old content', 1)`); err != nil {
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
	u, err := s.UserByID(context.Background(), 2)
	if err != nil || u.Suspended || u.SuspensionRevision != 0 {
		t.Fatalf("migration changed account: %+v %v", u, err)
	}
	for _, query := range []string{
		"UPDATE users SET suspended = 1 WHERE id = 1",
		"UPDATE users SET suspended = 2 WHERE id = 2",
		"UPDATE users SET suspension_revision = -1 WHERE id = 2",
		"UPDATE posts SET removed = 1 WHERE id = 1",
		"UPDATE posts SET removed = 2 WHERE id = 1",
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatalf("invalid schema state accepted: %s", query)
		}
	}
	if _, err := s.RemovePost(context.Background(), 1, 1, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE posts SET body = 'Revived' WHERE id = 1"); err == nil {
		t.Fatal("schema allowed removed message contents")
	}
	if err := s.migrate(); err != nil {
		t.Fatal("migration is not repeatable:", err)
	}
}
