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

// delegateFixture is an owner, a member allowed to invite, and one open and
// one already used invitation from that member.
type delegateFixture struct {
	s                *Store
	owner, delegate  int64
	open, used, gone string
}

func newDelegateFixture(t *testing.T) delegateFixture {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	f := delegateFixture{s: s, owner: testInvitationOwner(t, s, "owner"), delegate: testMember(t, s, "jules"), open: "open-code", used: "used-code", gone: "expired-code"}
	if err := s.SetInvitePermission(ctx, f.delegate, true); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	for _, invite := range []struct {
		hash string
		opts InvitationOptions
	}{{f.open, InvitationOptions{MaxUses: 0}}, {f.used, InvitationOptions{MaxUses: 1}}, {f.gone, InvitationOptions{MaxUses: 1, ExpiresAt: &past}}} {
		if _, err := s.CreateInvitation(ctx, f.delegate, invite.hash, "", invite.opts); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Register(ctx, "first", "hash", f.used); err != nil {
		t.Fatal(err)
	}
	return f
}

func invitationStatuses(t *testing.T, s *Store, creator int64) map[string]string {
	t.Helper()
	list, _, err := s.InvitationsByCreator(context.Background(), creator, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, invitation := range list {
		var hash string
		if err := s.db.QueryRow("SELECT token_hash FROM invitations WHERE id = ?", invitation.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		statuses[hash] = invitation.Status()
	}
	return statuses
}

func TestRemovingInvitePermissionRevokesOpenInvitations(t *testing.T) {
	f := newDelegateFixture(t)
	ctx := context.Background()
	if err := f.s.SetInvitePermission(ctx, f.delegate, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Register(ctx, "late", "hash", f.open); !errors.Is(err, errInvitation) {
		t.Fatalf("invitation outlived its creator's permission: %v", err)
	}
	// History stays accurate: only the open invitation becomes revoked.
	want := map[string]string{f.open: "revoked", f.used: "used up", f.gone: "expired"}
	if got := invitationStatuses(t, f.s, f.delegate); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("statuses after removing permission: %v want %v", got, want)
	}
	// Granting the permission again does not revive old links.
	if err := f.s.SetInvitePermission(ctx, f.delegate, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Register(ctx, "later", "hash", f.open); !errors.Is(err, errInvitation) {
		t.Fatalf("restored permission revived a revoked invitation: %v", err)
	}
	if err := f.s.SetInvitePermission(ctx, f.owner, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owner invite permission changed: %v", err)
	}
	if err := f.s.SetInvitePermission(ctx, 9999, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown member accepted: %v", err)
	}
}

func TestRegisterRequiresInviterPermission(t *testing.T) {
	f := newDelegateFixture(t)
	ctx := context.Background()
	// Simulate a permission removed by an older release, which left the
	// invitation open: the inviter's current permission still decides.
	if _, err := f.s.db.Exec("UPDATE users SET can_invite = 0 WHERE id = ?", f.delegate); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Register(ctx, "late", "hash", f.open); !errors.Is(err, errInvitation) {
		t.Fatalf("member without invite permission admitted someone: %v", err)
	}
	var uses int
	if err := f.s.db.QueryRow("SELECT uses FROM invitations WHERE token_hash = ?", f.open).Scan(&uses); err != nil || uses != 0 {
		t.Fatalf("rejected registration spent a use: %d %v", uses, err)
	}
	ownerCode := "owner-code"
	if _, err := f.s.CreateInvitation(ctx, f.owner, ownerCode, "", InvitationOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Register(ctx, "welcome", "hash", ownerCode); err != nil {
		t.Fatalf("owner invitation rejected: %v", err)
	}
}

func TestInvitePermissionRemovalIsAtomic(t *testing.T) {
	for _, stage := range []struct{ name, event, table string }{
		{"permission", "UPDATE", "users"}, {"invitations", "UPDATE", "invitations"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			f := newDelegateFixture(t)
			if _, err := f.s.db.Exec(fmt.Sprintf("CREATE TRIGGER fail_write BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'injected failure'); END", stage.event, stage.table)); err != nil {
				t.Fatal(err)
			}
			if err := f.s.SetInvitePermission(context.Background(), f.delegate, false); err == nil {
				t.Fatal("failure ignored")
			}
			user, err := f.s.UserByID(context.Background(), f.delegate)
			if err != nil || !user.CanInvite {
				t.Fatalf("permission removed without revoking invitations: %+v %v", user, err)
			}
			if got := invitationStatuses(t, f.s, f.delegate); got[f.open] != "open" {
				t.Fatalf("invitation revoked while permission stayed: %v", got)
			}
		})
	}
}

func TestInvitePermissionHTTPRevokesAndExplains(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	delegate := memberClient(t, a, "jules")
	jules, _, err := a.store.Credentials(context.Background(), "jules")
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, owner.post(fmt.Sprintf("/invites/members/%d/permission", jules.ID), url.Values{"enabled": {"1"}}), 303)
	w := delegate.post("/invites", url.Values{"max_uses": {"5"}, "expires_days": {"7"}})
	requireStatus(t, w, 200)
	code := regexp.MustCompile(`id="invite-code" type="text" readonly value="([0-9a-f]{64})"`).FindStringSubmatch(w.Body.String())
	if code == nil {
		t.Fatal("no invitation code")
	}
	page := owner.request("GET", "/invites", nil, nil).Body.String()
	if !strings.Contains(page, "also revokes their open invitations") {
		t.Fatal("invite access copy does not explain what removal does")
	}
	requireStatus(t, owner.post(fmt.Sprintf("/invites/members/%d/permission", jules.ID), url.Values{"enabled": {"0"}}), 303)
	requireStatus(t, delegate.request("GET", "/invites", nil, nil), 403)
	newcomer := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	newcomer.request("GET", "/join", nil, nil)
	w = newcomer.post("/join", url.Values{"username": {"sam"}, "password": {"a long test password"}, "invite": {code[1]}})
	requireStatus(t, w, 422)
	if !strings.Contains(w.Body.String(), errInvitation.Error()) {
		t.Fatal("revoked invitation did not explain itself")
	}
	requireStatus(t, owner.post("/invites/members/999/permission", url.Values{"enabled": {"0"}}), 404)
	requireStatus(t, delegate.post(fmt.Sprintf("/invites/members/%d/permission", jules.ID), url.Values{"enabled": {"1"}}), 403)
}

func TestInvitationExpiryNamesItsTimeZoneOnce(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	requireStatus(t, owner.post("/invites", url.Values{"max_uses": {"1"}, "expires_days": {"7"}}), 200)
	page := owner.request("GET", "/invites", nil, nil).Body.String()
	if !regexp.MustCompile(`Expires <time datetime="[^"]+">[A-Z][a-z]{2} \d{1,2}, \d{4} · \d{2}:\d{2} UTC</time>`).MatchString(page) {
		t.Fatalf("invitation expiry is not shown once in UTC: %s", page)
	}
	if strings.Contains(page, "UTC UTC") {
		t.Fatal("invitation expiry repeats its time zone")
	}
}
