package forum

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountDeletionErasesOwnDataAndPreservesReplies(t *testing.T) {
	app, owner := newTestApp(t, false)
	ownerID := signInTest(t, app, owner, true)
	member := memberClient(t, app, "leaving")
	user, hash, err := app.store.Credentials(t.Context(), "leaving")
	if err != nil {
		t.Fatal(err)
	}
	topic, err := app.store.CreateTopic(t.Context(), 1, user.ID, "My personal title", "My personal message", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = app.store.Reply(t.Context(), topic, ownerID, "Someone else's reply")
	if err != nil {
		t.Fatal(err)
	}
	var postID int64
	if err := app.store.db.QueryRow("SELECT id FROM posts WHERE author_id=?", user.ID).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	addAttachment(t, app, postID, user.ID, "private.png")
	if _, err := app.store.db.Exec("UPDATE users SET email='private@example.org',can_invite=1 WHERE id=?", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := testInvite(app.store, user.ID, "invite-to-revoke"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.NewSession(t.Context(), "second-session", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	deletionPage := member.request("GET", "/account/delete", nil, nil)
	requireStatus(t, deletionPage, 200)
	for _, control := range []string{`action="/account/delete"`, `name="confirmation"`, `name="current_password"`, `name="csrf"`, "Delete my account"} {
		if !strings.Contains(deletionPage.Body.String(), control) {
			t.Fatalf("deletion page is missing %s", control)
		}
	}
	requireStatus(t, member.request("POST", "/account/delete", url.Values{"confirmation": {"leaving"}, "current_password": {"a long test password"}}, nil), 403)
	requireStatus(t, member.post("/account/delete", url.Values{"confirmation": {"wrong"}, "current_password": {"a long test password"}}), 422)
	requireStatus(t, member.post("/account/delete", url.Values{"confirmation": {"leaving"}, "current_password": {"wrong"}}), 403)
	requireStatus(t, member.post("/account/delete", url.Values{"confirmation": {"leaving"}, "current_password": {"a long test password"}}), 303)
	requireStatus(t, member.request("GET", "/account/export", nil, nil), 303)
	if _, _, err := app.store.Credentials(t.Context(), "leaving"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old account remains: %v", err)
	}
	var deleted bool
	var storedName, storedHash, email string
	if err := app.store.db.QueryRow("SELECT deleted,username,password_hash,email FROM users WHERE id=?", user.ID).Scan(&deleted, &storedName, &storedHash, &email); err != nil {
		t.Fatal(err)
	}
	if !deleted || storedHash != "" || email != "" || storedName == "leaving" {
		t.Fatal("profile not erased")
	}
	posts, _, err := app.store.Posts(t.Context(), topic, 20, 0, &User{ID: ownerID, Role: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 || posts[0].Body != deletedMessage || posts[0].Author != "Former member" || posts[1].Body != "Someone else's reply" || posts[1].Number != 2 {
		t.Fatalf("conversation damaged: %+v", posts)
	}
	page := owner.request("GET", posts[0].URL(), nil, nil).Body.String()
	if strings.Contains(page, "1970") || strings.Contains(page, "My personal") || strings.Contains(page, "~d") {
		t.Fatal("deleted profile leaks or awkward timestamp remains")
	}
	for _, query := range []string{"SELECT count(*) FROM sessions WHERE user_id=?", "SELECT count(*) FROM invitations WHERE created_by=?", "SELECT count(*) FROM attachments WHERE credential_user_id=?"} {
		var count int
		if err := app.store.db.QueryRow(query, user.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("account artifacts survived: %s %d %v", query, count, err)
		}
	}
	if err := app.store.DeleteAccount(t.Context(), user.ID, "leaving", hash); err == nil {
		t.Fatal("deleted account can be used again")
	}
}

func TestAccountDeletionLastOwnerAndRollback(t *testing.T) {
	app, owner := newTestApp(t, false)
	ownerID := signInTest(t, app, owner, true)
	requireStatus(t, owner.request("GET", "/account/delete", nil, nil), 200)
	requireStatus(t, owner.post("/account/delete", url.Values{"confirmation": {"alex"}, "current_password": {"a long test password"}}), 409)
	member := memberClient(t, app, "leaving")
	user, hash, _ := app.store.Credentials(t.Context(), "leaving")
	topic, err := app.store.CreateTopic(t.Context(), 1, user.ID, "Keep it intact", "My words", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.db.Exec(`CREATE TRIGGER refuse_deletion BEFORE UPDATE OF deleted ON users WHEN NEW.deleted=1 BEGIN SELECT RAISE(ABORT,'injected deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := app.store.DeleteAccount(t.Context(), user.ID, user.Username, hash); err == nil {
		t.Fatal("injected failure ignored")
	}
	posts, _, err := app.store.Posts(t.Context(), topic, 20, 0, &User{ID: ownerID, Role: "owner"})
	if err != nil || len(posts) != 1 || posts[0].Body != "My words" {
		t.Fatalf("failed deletion did not roll back: %+v %v", posts, err)
	}
	requireStatus(t, member.request("GET", "/account", nil, nil), 200)
	if err := app.store.DeleteAccount(t.Context(), user.ID, user.Username, "stale hash"); !errors.Is(err, errAccountChanged) {
		t.Fatalf("stale credentials admitted: %v", err)
	}
}

func TestConcurrentOwnerDeletionsLeaveOneOwner(t *testing.T) {
	store := testStore(t)
	for _, name := range []string{"first", "second"} {
		if err := store.CreateOwner(t.Context(), name, "verified hash"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"first", "second"} {
		user, hash, _ := store.Credentials(t.Context(), name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- store.DeleteAccount(context.Background(), user.ID, user.Username, hash)
		}()
	}
	wg.Wait()
	close(results)
	succeeded, rejected := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, errLastOwner) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("owners deleted=%d refused=%d", succeeded, rejected)
	}
}
