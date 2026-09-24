package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInvitationOptionsAndRevocation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := testInvitationOwner(t, s, "owner")
	id, err := s.CreateInvitation(ctx, owner, "unlimited", "code-prefix", InvitationOptions{Label: "The whole crew", MaxUses: 0})
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		if _, err := s.Register(ctx, fmt.Sprintf("friend%d", n), "hash", "unlimited"); err != nil {
			t.Fatal(err)
		}
	}
	list, more, err := s.Invitations(ctx, 20, 0)
	if err != nil || more || len(list) != 1 || list[0].Uses != 3 || list[0].ExpiresAt.Valid || list[0].Status() != "open" || list[0].Label != "The whole crew" {
		t.Fatalf("invitation list: %+v %v", list, err)
	}
	for n := 0; n < 2; n++ {
		if err := s.RevokeInvitation(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Register(ctx, "latecomer", "hash", "unlimited"); !errors.Is(err, errInvitation) {
		t.Fatalf("revoked invitation accepted: %v", err)
	}
	list, _, _ = s.Invitations(ctx, 20, 0)
	if list[0].Status() != "revoked" || list[0].Uses != 3 {
		t.Fatal("revocation lost history")
	}
	if err := s.RevokeInvitation(ctx, id+1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing invitation: %v", err)
	}
	for n := 0; n < 21; n++ {
		if _, err := s.CreateInvitation(ctx, owner, fmt.Sprintf("page%d", n), "", InvitationOptions{MaxUses: 1}); err != nil {
			t.Fatal(err)
		}
	}
	first, more, err := s.Invitations(ctx, 20, 0)
	if err != nil || !more || len(first) != 20 {
		t.Fatal("first invitation page")
	}
	last, more, err := s.Invitations(ctx, 20, 20)
	if err != nil || more || len(last) != 2 || last[0].ID >= first[len(first)-1].ID {
		t.Fatal("invitation pagination skipped or duplicated rows")
	}
}

func TestMultiUseInvitationRaceAndRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := testInvitationOwner(t, s, "owner")
	if _, err := s.CreateInvitation(ctx, owner, "group", "", InvitationOptions{MaxUses: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(ctx, "OWNER", "hash", "group"); !errors.Is(err, errUsernameTaken) {
		t.Fatalf("duplicate registration: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_join BEFORE INSERT ON users BEGIN SELECT RAISE(ABORT,'test write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(ctx, "failed", "hash", "group"); err == nil {
		t.Fatal("failed user write accepted")
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_join"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := s.Register(ctx, fmt.Sprintf("friend%d", n), "hash", "group")
			results <- err
		}(n)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, errInvitation) {
			t.Fatal(err)
		}
	}
	list, _, err := s.Invitations(ctx, 20, 0)
	if err != nil || accepted != 2 || list[0].Uses != 2 || list[0].Status() != "used up" {
		t.Fatalf("accepted %d; list %+v; err %v", accepted, list, err)
	}
}

func testInvitationOwner(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	if err := s.CreateOwner(context.Background(), name, "test-hash"); err != nil {
		t.Fatal(err)
	}
	user, _, err := s.Credentials(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func TestInvitationMigrationPreservesExistingLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, file := range []string{"001_initial.sql", "002_access_modes.sql", "003_imvault.sql"} {
		data, err := migrations.ReadFile("migrations/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users(id, username, password_hash, role, created_at) VALUES (1,'owner','hash','owner',1); PRAGMA user_version=3`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		hash    string
		expires int64
	}{{"old", time.Now().Add(time.Hour).Unix()}, {"expired", time.Now().Add(-time.Hour).Unix()}} {
		if _, err := db.Exec("INSERT INTO invitations(token_hash, created_by, expires_at) VALUES (?,1,?)", tc.hash, tc.expires); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.Register(ctx, "friend", "hash", "old"); err != nil {
		t.Fatalf("old invitation stopped working: %v", err)
	}
	for _, hash := range []string{"old", "expired"} {
		if _, err := s.Register(ctx, "outsider", "hash", hash); !errors.Is(err, errInvitation) {
			t.Fatalf("old invite gained uses or lifetime: %s %v", hash, err)
		}
	}
	list, _, err := s.Invitations(ctx, 20, 0)
	if err != nil || len(list) != 2 || list[0].MaxUses != 1 || !list[0].ExpiresAt.Valid {
		t.Fatalf("migration lost invitation settings: %+v %v", list, err)
	}
}

func TestInvitationManagementRoutes(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	a.config.BaseURL = "https://board.example.org"
	form := url.Values{"label": {"Friends <script>"}, "max_uses": {"2"}, "expires_days": {""}}
	w := owner.post("/invites", form)
	requireStatus(t, w, 200)
	match := regexp.MustCompile(`https://board\.example\.org/join\?invite=([a-f0-9]{64})`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 || strings.Contains(w.Body.String(), "Friends <script>") {
		t.Fatal("missing absolute invite link or unescaped label")
	}
	token := match[1]
	list, _, err := a.store.Invitations(context.Background(), 20, 0)
	if err != nil || len(list) != 1 || list[0].MaxUses != 2 || list[0].ExpiresAt.Valid {
		t.Fatal("invitation controls not saved")
	}
	w = owner.request("GET", "/invites", nil, nil)
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), token) || !strings.Contains(w.Body.String(), token[:12]) {
		t.Fatal("list exposed full token or lost identifying prefix")
	}
	path := fmt.Sprintf("/invites/%d/revoke", list[0].ID)
	requireStatus(t, owner.request("POST", path, url.Values{}, nil), 403)
	member := memberClient(t, a, "jules")
	requireStatus(t, member.post(path, nil), 403)
	guest := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	guest.request("GET", "/join", nil, nil)
	requireStatus(t, guest.post(path, nil), 303)
	requireStatus(t, owner.request("GET", path, nil, nil), 405)
	requireStatus(t, owner.post(path, nil), 303)
	requireStatus(t, owner.post("/invites/9999/revoke", nil), 404)
	w = owner.request("GET", "/invites", nil, nil)
	if !strings.Contains(w.Body.String(), "revoked") {
		t.Fatal("revocation not visible")
	}
	requireStatus(t, guest.post("/join", url.Values{"username": {"newfriend"}, "password": {"a long test password"}, "invite": {token}}), 422)
}

func TestInvitationInputValidationAndURL(t *testing.T) {
	a, c := newTestApp(t, false)
	signInTest(t, a, c, true)
	for _, form := range []url.Values{
		{"max_uses": {"-1"}}, {"max_uses": {"10001"}}, {"max_uses": {"1.5"}},
		{"expires_days": {"-1"}}, {"expires_days": {"3651"}}, {"expires_days": {"forever"}},
		{"label": {strings.Repeat("界", 65)}}, {"label": {"hello\nworld"}},
	} {
		requireStatus(t, c.post("/invites", form), 422)
	}
	list, _, err := a.store.Invitations(context.Background(), 20, 0)
	if err != nil || len(list) != 0 {
		t.Fatal("invalid form created invitations")
	}
	w := c.post("/invites", nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `value="http://example.com/join?invite=`) {
		t.Fatal("default invite link requires JavaScript to become absolute")
	}
	list, _, _ = a.store.Invitations(context.Background(), 20, 0)
	if list[0].MaxUses != 1 || !list[0].ExpiresAt.Valid || list[0].ExpiresAt.Int64 < time.Now().Add(6*24*time.Hour).Unix() {
		t.Fatal("default single-use seven-day policy changed")
	}
	a.config.SecureCookies = true
	r := httptest.NewRequest("GET", "http://board.example.org/invites", nil)
	if got := a.inviteURL(r, "code"); got != "https://board.example.org/join?invite=code" {
		t.Fatalf("HTTPS invite link: %s", got)
	}
	for _, invalid := range []string{"//board.example.org", "javascript:alert(1)", "https://user@board.example.org", "https://board.example.org/path", "https://board.example.org?x=y", "https://board.example.org/#part"} {
		if _, err := canonicalBaseURL(invalid); err == nil {
			t.Errorf("accepted base URL: %s", invalid)
		}
	}
	for _, value := range []string{"https://board.example.org/", "https://board.example.org/#"} {
		if got, err := canonicalBaseURL(value); err != nil || got != "https://board.example.org" {
			t.Fatalf("canonical origin: %s %v", got, err)
		}
	}
}

func TestDelegatedInvitationsTrackAndRestrictCustody(t *testing.T) {
	a, owner := newTestApp(t, false)
	ownerID := signInTest(t, a, owner, true)
	delegate := memberClient(t, a, "jules")
	delegateUser, _, err := a.store.Credentials(context.Background(), "jules")
	if err != nil {
		t.Fatal(err)
	}

	requireStatus(t, delegate.request("GET", "/invites", nil, nil), 403)
	requireStatus(t, owner.post(fmt.Sprintf("/invites/members/%d/permission", delegateUser.ID), url.Values{"enabled": {"1"}}), 303)
	requireStatus(t, delegate.request("GET", "/invites", nil, nil), 200)

	w := delegate.post("/invites", url.Values{"label": {"From Jules"}, "max_uses": {"1"}, "expires_days": {"7"}})
	requireStatus(t, w, 200)
	match := regexp.MustCompile(`/join\?invite=([a-f0-9]{64})`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatal("delegated invitation link missing")
	}
	list, _, err := a.store.InvitationsByCreator(context.Background(), delegateUser.ID, 20, 0)
	if err != nil || len(list) != 1 || list[0].Creator != "jules" {
		t.Fatalf("delegated invitations: %+v, %v", list, err)
	}
	newcomer := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	newcomer.request("GET", "/join", nil, nil)
	requireStatus(t, newcomer.post("/join", url.Values{"username": {"sam"}, "password": {"a long test password"}, "invite": {match[1]}}), 303)

	joined, _, err := a.store.Credentials(context.Background(), "sam")
	if err != nil || joined.InvitedBy != delegateUser.ID || joined.InvitedByName != "jules" || joined.InvitationID != list[0].ID {
		t.Fatalf("invite attribution: %+v, %v", joined, err)
	}
	if !strings.Contains(owner.request("GET", "/invites", nil, nil).Body.String(), "Joined from an invitation by jules") {
		t.Fatal("owner could not inspect invitation attribution")
	}
	if ownerID == 0 {
		t.Fatal("owner id was not set")
	}
}
