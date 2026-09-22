package forum

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func testMember(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	id, err := s.CreateUser(context.Background(), name, "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMigrationPersistenceAndConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private board?#.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user := testMember(t, s, "alex")
	id, err := s.CreateTopic(ctx, 1, user, "Sunday dinner", "Bring something good.")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	boards, err := s.Boards(ctx)
	if err != nil || len(boards) != 5 {
		t.Fatalf("boards=%v, err=%v", boards, err)
	}
	if boards[0].Topics != 1 || boards[0].Posts != 1 || boards[0].LastTopicID != id {
		t.Fatalf("incorrect board summary: %+v", boards[0])
	}
	if _, err := s.CreateTopic(ctx, 999, user, "Missing board", "hello"); err == nil {
		t.Fatal("missing board accepted")
	}
	if _, _, err := s.Reply(ctx, id, 999, "Unknown member"); err == nil {
		t.Fatal("missing author accepted")
	}
	if _, err := s.CreateUser(ctx, "ALEX", "hash"); !errors.Is(err, errUsernameTaken) {
		t.Fatalf("case-insensitive uniqueness: %v", err)
	}
}

func TestTopicCreationIsAtomic(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	user := testMember(t, s, "alex")
	if _, err := s.CreateTopic(ctx, 1, user, "Must roll back", ""); err == nil {
		t.Fatal("invalid first post accepted")
	}
	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Topics != 0 || stats.Posts != 0 {
		t.Fatalf("partial conversation left behind: %+v", stats)
	}
}

func TestInvitationIsSingleUseAtomicAndExpiring(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := testMember(t, s, "owner")
	if err := s.Invite(ctx, owner, "invitation"); err != nil {
		t.Fatal(err)
	}
	// A duplicate username must not spend a valid invitation.
	if _, err := s.Register(ctx, "OWNER", "hash", "invitation"); !errors.Is(err, errUsernameTaken) {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Register(ctx, fmt.Sprintf("friend%d", i), "hash", "invitation")
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success, denied := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, errInvitation) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatalf("success=%d denied=%d", success, denied)
	}
	if err := s.Invite(ctx, owner, "expired"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE invitations SET expires_at = ? WHERE token_hash = 'expired'", time.Now().Add(-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(ctx, "outsider", "hash", "expired"); !errors.Is(err, errInvitation) {
		t.Fatalf("expired invite accepted: %v", err)
	}
}

func TestSearchTreatsWildcardsAndSQLAsText(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	user := testMember(t, s, "alex")
	for _, title := range []string{"A 100% good recipe", "An ordinary dinner", "Under_score", "Literal ' OR 1=1 --"} {
		if _, err := s.CreateTopic(ctx, 1, user, title, "Soup and bread"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query string
		count int
	}{{"%", 1}, {"_", 1}, {"' OR 1=1 --", 1}, {"Soup", 4}, {"no match", 0}, {`\`, 0}} {
		t.Run(tc.query, func(t *testing.T) {
			topics, _, err := s.Topics(ctx, 0, tc.query, 20, 0)
			if err != nil || len(topics) != tc.count {
				t.Fatalf("got %d topics, err=%v", len(topics), err)
			}
		})
	}
}

func TestPaginationAndReplyOrder(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	user := testMember(t, s, "alex")
	id, err := s.CreateTopic(ctx, 1, user, "Our weekend", "First post")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 22; i++ {
		_, count, err := s.Reply(ctx, id, user, fmt.Sprintf("Reply %d", i))
		if err != nil || count != i+1 {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	first, more, err := s.Posts(ctx, id, 20, 0)
	if err != nil || !more || len(first) != 20 || first[0].Number != 1 {
		t.Fatalf("first page: %v %v %v", len(first), more, err)
	}
	second, more, err := s.Posts(ctx, id, 20, 20)
	if err != nil || more || len(second) != 3 || second[0].Number != 21 || second[2].Body != "Reply 22" {
		t.Fatalf("second page: %+v %v %v", second, more, err)
	}
	for i := 0; i < 22; i++ {
		if _, err := s.CreateTopic(ctx, 1, user, fmt.Sprintf("Topic %d", i), "Hi"); err != nil {
			t.Fatal(err)
		}
	}
	topics, more, err := s.Topics(ctx, 1, "", 20, 0)
	if err != nil || !more || len(topics) != 20 {
		t.Fatalf("topics pagination: %v %v %v", len(topics), more, err)
	}
}

func TestSessionsExpireAndLogoutInvalidates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	user := testMember(t, s, "alex")
	if err := s.NewSession(ctx, "expired", user, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if u, err := s.Session(ctx, "expired"); u != nil || err != nil {
		t.Fatalf("expired session: %v %v", u, err)
	}
	if err := s.NewSession(ctx, "valid", user, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if u, err := s.Session(ctx, "valid"); u == nil || err != nil {
		t.Fatalf("valid session: %v %v", u, err)
	}
	if err := s.DeleteSession(ctx, "valid"); err != nil {
		t.Fatal(err)
	}
	if u, err := s.Session(ctx, "valid"); u != nil || err != nil {
		t.Fatalf("deleted session: %v %v", u, err)
	}
}
