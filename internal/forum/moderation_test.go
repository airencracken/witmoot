package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestOwnerActivityLinksRemovalsAndRecordsTitlesAndAvatars(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	_, id := signedInCommunityMember(t, a)
	ctx := context.Background()
	topic, err := a.store.CreateTopic(ctx, 1, id, "An unfortunate title", "Opening words", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := a.store.Reply(ctx, topic, id, "A reply")
	if err != nil {
		t.Fatal(err)
	}
	var opening int64
	if err := a.store.db.QueryRow("SELECT id FROM posts WHERE topic_id = ? ORDER BY id LIMIT 1", topic).Scan(&opening); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, owner.post(fmt.Sprintf("/posts/%d/remove", opening), url.Values{"revision": {"0"}, "confirmation": {"1"}, "replace_title": {"1"}}), 303)
	requireStatus(t, owner.post(fmt.Sprintf("/posts/%d/remove", reply), url.Values{"revision": {"0"}, "confirmation": {"1"}}), 303)
	if err := a.store.SaveAvatar(ctx, id, pngAvatarFixture(t, 64, 64)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, owner.post(fmt.Sprintf("/members/%d/avatar/remove", id), nil), 303)
	// Removing an avatar that is already gone is not an action worth logging.
	requireStatus(t, owner.post(fmt.Sprintf("/members/%d/avatar/remove", id), nil), 303)
	w := owner.post("/members/999/avatar/remove", nil)
	requireStatus(t, w, 404)
	if !strings.Contains(w.Body.String(), "We could not find that member.") {
		t.Fatal("unknown member was not named")
	}

	page := owner.request("GET", "/moderation", nil, nil).Body.String()
	for _, want := range []string{
		fmt.Sprintf(`Removed <a href="/topics/%d?page=1#post-%d">message %d in conversation %d</a> and replaced the conversation title`, topic, opening, opening, topic),
		fmt.Sprintf(`Removed <a href="/topics/%d?page=1#post-%d">message %d in conversation %d</a></p>`, topic, reply, reply, topic),
		"Removed the avatar of jules",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("owner activity is missing %q:\n%s", want, page)
		}
	}
	if strings.Count(page, "Removed the avatar of") != 1 || strings.Contains(page, "Removed Message") {
		t.Fatalf("owner activity is noisy or miscapitalised:\n%s", page)
	}
}

func TestOwnerAvatarRemovalIsAtomic(t *testing.T) {
	a, owner := newTestApp(t, false)
	ownerID := signInTest(t, a, owner, true)
	ctx := context.Background()
	member := testMember(t, a.store, "jules")
	if err := a.store.SaveAvatar(ctx, member, pngAvatarFixture(t, 64, 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.db.Exec("CREATE TRIGGER fail_write BEFORE INSERT ON community_events BEGIN SELECT RAISE(ABORT, 'injected failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.RemoveMemberAvatar(ctx, ownerID, member); err == nil {
		t.Fatal("failure ignored")
	}
	if has, err := a.store.HasAvatar(ctx, member); err != nil || !has {
		t.Fatalf("avatar removed without a log entry: %v", err)
	}
	if err := a.store.RemoveMemberAvatar(ctx, member, ownerID); !errors.Is(err, errOwner) {
		t.Fatalf("member removed an avatar: %v", err)
	}
}

func TestModerationLogMigrationKeepsAndLinksOldEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"001_initial.sql", "002_access_modes.sql", "003_imvault.sql", "004_invitations.sql", "005_board_access_and_edits.sql", "006_board_lifecycle.sql", "007_user_groups.sql", "008_invite_attribution.sql", "009_instance_branding.sql", "010_auth_tokens.sql", "011_user_email.sql", "012_user_avatars.sql", "013_community_care.sql"}
	for _, name := range names {
		migration, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 13;
		INSERT INTO users(id, username, password_hash, role, created_at) VALUES (1, 'owner', 'hash', 'owner', 1), (2, 'jules', 'hash', 'member', 1);
		INSERT INTO topics(id, board_id, author_id, title, audience, created_at, updated_at) VALUES (1, 1, 2, 'Conversation', 'members', 1, 1);
		INSERT INTO posts(id, topic_id, author_id, body, created_at, removed) VALUES (1, 1, 2, 'This message was removed by a site owner.', 1, 1);
		INSERT INTO community_events(id, actor_id, actor_name, action, subject, created_at) VALUES
			(1, 1, 'owner', 'suspend', 'jules', 10), (2, 1, 'owner', 'remove-message', 'Message 1 in conversation 1', 20),
			(3, 1, 'owner', 'remove-message', 'Message 99 in conversation 5', 30), (4, NULL, 'gone', 'restore', 'jules', 40)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	events, err := s.CommunityEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(events)
	want := "[{gone restore jules  false 40} {owner remove-message message 99 in conversation 5  false 30} {owner remove-message message 1 in conversation 1 /topics/1?page=1#post-1 false 20} {owner suspend jules  false 10}]"
	if got != want {
		t.Fatalf("migrated events:\n got %s\nwant %s", got, want)
	}
	for _, query := range []string{
		"INSERT INTO community_events(actor_name, action, subject, created_at) VALUES ('owner', 'delete-everything', 'x', 1)",
		"INSERT INTO community_events(actor_name, action, subject, title_replaced, created_at) VALUES ('owner', 'suspend', 'x', 1, 1)",
		"INSERT INTO community_events(actor_name, action, subject, post_id, created_at) VALUES ('owner', 'remove-message', 'x', 404, 1)",
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Errorf("invalid activity accepted: %s", query)
		}
	}
	if err := s.migrate(); err != nil {
		t.Fatal("migration is not repeatable:", err)
	}
}

// The removal placeholder lives in one Go constant; migration 013 must keep
// checking for that same text, and pages render it from the stored body.
func TestRemovedMessagePlaceholderMatchesTheSchema(t *testing.T) {
	migration, err := os.ReadFile("migrations/013_community_care.sql")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`removed = 0 OR body = '([^']*)'`).FindSubmatch(migration)
	if match == nil || string(match[1]) != removedMessage {
		t.Fatalf("schema placeholder %q differs from %q", match, removedMessage)
	}
	templates, err := os.ReadFile("templates/conversations.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(templates), removedMessage) {
		t.Fatal("the conversation template repeats the placeholder text")
	}
}
