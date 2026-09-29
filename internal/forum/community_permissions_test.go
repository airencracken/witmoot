package forum

import (
	"context"
	"math/rand"
	"testing"
)

func TestSuspensionPreservesGrantsAndExplainsEffectiveGroupAccess(t *testing.T) {
	f := privateBoardFixture(t)
	ctx := context.Background()
	g := createTestGroup(t, f, "Friends", f.writer.ID, f.reader.ID)
	setTestGrants(t, f, map[int64]string{f.reader.ID: "read"}, map[int64]string{g.ID: "write"})
	s := f.app.store
	writer, err := s.UserByID(ctx, f.writer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeMember(ctx, f.owner.ID, f.writer.ID, 0, "suspend", writer.Username); err != nil {
		t.Fatal(err)
	}
	boards, err := s.GroupBoards(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range boards {
		for _, m := range b.Members {
			if m.Username == writer.Username && m.Effective != "none" {
				t.Fatal("group report claims suspended member has access")
			}
		}
	}
	if err := s.ChangeMember(ctx, f.owner.ID, f.writer.ID, 1, "restore", writer.Username); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     int64
		access string
	}{{f.writer.ID, "write"}, {f.reader.ID, "read"}} {
		user, err := s.UserByID(ctx, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Board(ctx, f.boardID, &user)
		if err != nil || b.Access != tc.access {
			t.Fatalf("restoration changed permissions: %+v %v", b, err)
		}
	}
}

func TestMemberActionSequencesPreserveRevisionAndActivity(t *testing.T) {
	f := newCommunityFixture(t)
	ctx := context.Background()
	random := rand.New(rand.NewSource(42))
	var suspended bool
	var revision int64
	var successful int
	for i := 0; i < 80; i++ {
		action, nextState := "restore", false
		if random.Intn(2) == 1 {
			action, nextState = "suspend", true
		}
		requestedRevision := revision
		if random.Intn(4) == 0 {
			requestedRevision++
		}
		confirmation := "jules"
		if random.Intn(4) == 0 {
			confirmation = "incorrect"
		}
		wantSuccess := nextState != suspended && requestedRevision == revision && confirmation == "jules"
		err := f.s.ChangeMember(ctx, f.owner, f.member, requestedRevision, action, confirmation)
		if (err == nil) != wantSuccess {
			t.Fatalf("step %d: success=%v error=%v", i, wantSuccess, err)
		}
		if wantSuccess {
			suspended = nextState
			revision++
			successful++
		}
		u, err := f.s.UserByID(ctx, f.member)
		if err != nil || u.Suspended != suspended || u.SuspensionRevision != revision {
			t.Fatalf("state diverged at step %d: %+v %v", i, u, err)
		}
	}
	events, err := f.s.CommunityEvents(ctx)
	if err != nil || len(events) != successful {
		t.Fatalf("activity not atomic with state: %d want %d error=%v", len(events), successful, err)
	}
}
