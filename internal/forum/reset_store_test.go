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
	user, err := s.ResetPassword(ctx, TokenHash(token), "new-hash", time.Now())
	if err != nil || user.ID != owner {
		t.Fatalf("consume: %+v %v", user, err)
	}
	if _, err := s.ResetPassword(ctx, TokenHash(token), "replayed-hash", time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("replayed token accepted: %v", err)
	}
	if _, err := s.AuthTokenValid(ctx, TokenHash(token), TokenPasswordReset, time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("spent token still valid: %v", err)
	}

	// A token for another purpose, an unknown digest and an expired one are all
	// reported the same way.
	if _, err := s.ResetPassword(ctx, NewToken(), "unknown-hash", time.Now()); !errors.Is(err, errAuthToken) {
		t.Fatalf("unknown token accepted: %v", err)
	}
	expired := NewToken()
	if err := s.CreateAuthToken(ctx, owner, TokenPasswordReset, TokenHash(expired), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetPassword(ctx, TokenHash(expired), "expired-hash", time.Now()); !errors.Is(err, errAuthToken) {
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

func TestSetPasswordRollsBackWhenRevocationFails(t *testing.T) {
	for _, table := range []string{"sessions", "auth_tokens"} {
		t.Run(table, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			owner := testInvitationOwner(t, s, "owner")
			_, oldHash, err := s.Credentials(ctx, "owner")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.NewSession(ctx, "session", owner, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateAuthToken(ctx, owner, TokenPasswordReset, "token", time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("CREATE TRIGGER fail_revoke BEFORE DELETE ON " + table + " BEGIN SELECT RAISE(ABORT, 'injected failure'); END"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetPassword(ctx, owner, "new-hash"); err == nil {
				t.Error("password change ignored revocation failure")
			}
			if _, hash, err := s.Credentials(ctx, "owner"); err != nil || hash != oldHash {
				t.Errorf("failed password change was not rolled back: %v", err)
			}
			if session, err := s.Session(ctx, "session"); err != nil || session == nil {
				t.Errorf("failed password change ended the session: %v", err)
			}
			if _, err := s.AuthTokenValid(ctx, "token", TokenPasswordReset, time.Now()); err != nil {
				t.Errorf("failed password change revoked the link: %v", err)
			}
		})
	}
}

func TestResetPasswordSingleUseAndAccountIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := testInvitationOwner(t, s, "owner")
	other := testInvitationOwner(t, s, "other")
	for _, account := range []struct {
		id   int64
		name string
	}{{owner, "owner"}, {other, "other"}} {
		if err := s.NewSession(ctx, account.name, account.id, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateAuthToken(ctx, account.id, TokenPasswordReset, account.name, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		hash string
		err  error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, hash := range []string{"first-hash", "second-hash"} {
		go func() {
			<-start
			_, err := s.ResetPassword(ctx, "owner", hash, time.Now())
			results <- result{hash, err}
		}()
	}
	close(start)
	winner := ""
	for range 2 {
		result := <-results
		if result.err == nil {
			if winner != "" {
				t.Fatal("two concurrent resets redeemed the same token")
			}
			winner = result.hash
		} else if !errors.Is(result.err, errAuthToken) {
			t.Fatalf("unexpected reset error: %v", result.err)
		}
	}
	_, hash, err := s.Credentials(ctx, "owner")
	if err != nil || winner == "" || hash != winner {
		t.Fatalf("password does not match the successful reset: %v", err)
	}
	if session, err := s.Session(ctx, "owner"); err != nil || session != nil {
		t.Fatalf("reset did not revoke the owner's session: %v", err)
	}
	if session, err := s.Session(ctx, "other"); err != nil || session == nil {
		t.Fatalf("reset affected another account's session: %v", err)
	}
	if _, err := s.AuthTokenValid(ctx, "other", TokenPasswordReset, time.Now()); err != nil {
		t.Fatalf("reset revoked another account's link: %v", err)
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
