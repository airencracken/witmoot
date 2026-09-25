package forum

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestResetTokenLifecycleAndSingleUse(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := testInvitationOwner(t, s, "owner")

	token := NewToken()
	if err := s.CreateAuthToken(ctx, owner, TokenPasswordReset, TokenHash(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The digest is what is stored, never the token itself.
	var stored string
	if err := s.db.QueryRow("SELECT token_hash FROM auth_tokens").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != TokenHash(token) {
		t.Fatal("reset token should be stored only as a digest")
	}

	// A valid link can be checked without spending it, which a double-loaded
	// form depends on.
	if user, err := s.AuthTokenValid(ctx, TokenHash(token), TokenPasswordReset, time.Now()); err != nil || user.ID != owner {
		t.Fatalf("valid token lookup: %+v %v", user, err)
	}
	user, err := s.ConsumeAuthToken(ctx, TokenHash(token), TokenPasswordReset, time.Now())
	if err != nil || user.ID != owner {
		t.Fatalf("consume: %+v %v", user, err)
	}
	if _, err := s.ConsumeAuthToken(ctx, TokenHash(token), TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("replayed token accepted: %v", err)
	}
	if _, err := s.AuthTokenValid(ctx, TokenHash(token), TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("spent token still valid: %v", err)
	}

	// A token for another purpose, an unknown digest and an expired one are all
	// reported the same way.
	if _, err := s.ConsumeAuthToken(ctx, NewToken(), TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("unknown token accepted: %v", err)
	}
	expired := NewToken()
	if err := s.CreateAuthToken(ctx, owner, TokenPasswordReset, TokenHash(expired), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeAuthToken(ctx, TokenHash(expired), TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("expired token accepted: %v", err)
	}
}

func TestOnlyNewestResetTokenWorks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := testInvitationOwner(t, s, "owner")
	for _, token := range []string{"first", "second"} {
		if err := s.CreateAuthToken(ctx, owner, TokenPasswordReset, TokenHash(token), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AuthTokenValid(ctx, TokenHash("first"), TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("older token should have been replaced: %v", err)
	}
	if _, err := s.AuthTokenValid(ctx, TokenHash("second"), TokenPasswordReset, time.Now()); err != nil {
		t.Fatalf("newest token rejected: %v", err)
	}
	if err := s.DeleteAuthTokensForUser(ctx, owner, TokenPasswordReset); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthTokenValid(ctx, TokenHash("second"), TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("revoked token still valid: %v", err)
	}
}

func TestSetPasswordEndsSessionsAndMembersReportPending(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := testInvitationOwner(t, s, "owner")
	if err := s.NewSession(ctx, "session", owner, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(ctx, owner, "new-hash"); err != nil {
		t.Fatal(err)
	}
	_, hash, err := s.Credentials(ctx, "owner")
	if err != nil || hash != "new-hash" {
		t.Fatalf("password not replaced: %q %v", hash, err)
	}
	if err := s.DeleteSessionsForUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if session, err := s.Session(ctx, "session"); session != nil || err != nil {
		t.Fatalf("session survived a password change: %v %v", session, err)
	}
	if err := s.SetPassword(ctx, owner+999, "hash"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing account: %v", err)
	}

	// The member list shows whether a reset link is waiting.
	members, err := s.Members(ctx)
	if err != nil || len(members) != 1 || members[0].Pending {
		t.Fatalf("members before a token: %+v %v", members, err)
	}
	if err := s.CreateAuthToken(ctx, owner, TokenPasswordReset, TokenHash("waiting"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	members, err = s.Members(ctx)
	if err != nil || !members[0].Pending {
		t.Fatalf("members did not report a pending link: %+v %v", members, err)
	}
}

func TestSetEmailIsOptionalAndValidatedByCallers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := testInvitationOwner(t, s, "owner")
	if err := s.SetEmail(ctx, owner, "owner@example.org"); err != nil {
		t.Fatal(err)
	}
	user, err := s.UserByID(ctx, owner)
	if err != nil || user.Email != "owner@example.org" {
		t.Fatalf("email not saved: %+v %v", user, err)
	}
	if err := s.SetEmail(ctx, owner+999, "x@example.org"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing account: %v", err)
	}
}
