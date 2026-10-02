package admin

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/smtp"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/bcrypt"

	"witmoot/internal/forum"
)

type fakeMailer struct {
	enabled bool
	sent    []smtp.Message
}

func (f *fakeMailer) Enabled() bool { return f.enabled }

func (f *fakeMailer) Send(_ context.Context, msg smtp.Message) error {
	f.sent = append(f.sent, msg)
	return nil
}

func testStore(t *testing.T) *forum.Store {
	t.Helper()
	store, err := forum.OpenStore(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTest(t, store) })
	return store
}

func testModel(t *testing.T, store *forum.Store, opts Options) Model {
	t.Helper()
	if opts.SiteName == "" {
		opts.SiteName = "Test table"
	}
	m, err := New(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func press(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

func send(t *testing.T, m *Model, keys ...tea.KeyMsg) {
	t.Helper()
	for _, key := range keys {
		updated, _ := m.Update(key)
		model, ok := updated.(Model)
		if !ok {
			t.Fatalf("Update returned %T", updated)
		}
		*m = model
	}
}

func TestAdminCreatesAnOwner(t *testing.T) {
	store := testStore(t)
	m := testModel(t, store, Options{})
	send(t, &m, press("n"))
	if m.screen != screenInput {
		t.Fatalf("new owner did not open a form: screen=%v", m.screen)
	}
	send(t, &m, press("alex"), press("tab"), press("a long test password"), press("tab"), press("a long test password"), press("enter"))
	if m.screen != screenList {
		t.Fatalf("form did not close on save: %v (%v)", m.screen, m.err)
	}
	user, _, err := store.Credentials(context.Background(), "alex")
	if err != nil || user.Role != "owner" {
		t.Fatalf("owner not created: %+v %v", user, err)
	}
	if !strings.Contains(m.flash, "created") {
		t.Errorf("missing confirmation: %q", m.flash)
	}
}

func TestAdminRejectsAMismatchedPassword(t *testing.T) {
	store := testStore(t)
	m := testModel(t, store, Options{})
	send(t, &m, press("n"), press("alex"), press("tab"), press("a long test password"), press("tab"), press("a different password"), press("enter"))
	if m.err == nil || m.screen != screenInput {
		t.Fatalf("mismatch accepted: err=%v screen=%v", m.err, m.screen)
	}
	if _, _, err := store.Credentials(context.Background(), "alex"); err == nil {
		t.Fatal("a rejected form still created the account")
	}
}

func TestAdminShowsSuspensionAndOffersNoResetLink(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.CreateOwner(ctx, "owner", "hash"); err != nil {
		t.Fatal(err)
	}
	owner, _, err := store.Credentials(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.CreateUser(ctx, "jules", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ChangeMember(ctx, owner.ID, id, 0, "suspend", "jules"); err != nil {
		t.Fatal(err)
	}
	m := testModel(t, store, Options{})
	var found bool
	for i, member := range m.members {
		if member.ID == id {
			found = true
			if m.table.Rows()[i][5] != "suspended" {
				t.Fatal("suspended account shown as active")
			}
			m.target = member
			for _, choice := range m.memberActions() {
				if choice.kind == actionReset || choice.kind == actionResetEmail {
					t.Fatal("suspended account offered reset link")
				}
			}
		}
	}
	if !found {
		t.Fatal("suspended account missing from list")
	}
}

func TestAdminIssuesAndCancelsAResetLink(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreateUser(context.Background(), "jules", "hash"); err != nil {
		t.Fatal(err)
	}
	m := testModel(t, store, Options{BaseURL: "https://board.example.org"})

	// Select the member, then the first action, which creates a link.
	send(t, &m, press("enter"), press("enter"))
	if m.screen != screenResult {
		t.Fatalf("reset link did not open a result view: %v (%v)", m.screen, m.err)
	}
	if !strings.HasPrefix(m.link, "https://board.example.org/reset/") {
		t.Fatalf("link is not complete: %q", m.link)
	}
	token := m.link[strings.LastIndex(m.link, "/")+1:]
	if _, err := store.AuthTokenValid(context.Background(), forum.TokenHash(token), forum.TokenPasswordReset, time.Now()); err != nil {
		t.Fatalf("issued link is not usable: %v", err)
	}

	// Back to the list: the member now shows a waiting link.
	send(t, &m, press("esc"))
	if len(m.members) != 1 || !m.members[0].Pending {
		t.Fatalf("pending link not reported: %+v", m.members)
	}

	// Open the actions again and cancel the link.
	send(t, &m, press("enter"), press("down"), press("enter"))
	if m.screen != screenConfirm {
		t.Fatalf("cancel did not ask for confirmation: %v", m.screen)
	}
	send(t, &m, press("y"))
	if m.err != nil || m.screen != screenList {
		t.Fatalf("cancel failed: %v %v", m.err, m.screen)
	}
	if m.members[0].Pending {
		t.Fatal("cancelled link still pending")
	}
}

func TestAdminSetsAPasswordAndEndsSessions(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID, err := store.CreateUser(ctx, "jules", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewSession(ctx, "session", userID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	m := testModel(t, store, Options{})

	// Actions without a pending link: reset, set password, back.
	send(t, &m, press("enter"), press("down"), press("enter"))
	if m.screen != screenInput || !strings.Contains(m.title, "jules") {
		t.Fatalf("password form did not open: %v %q", m.screen, m.title)
	}
	if err := store.CreateAuthToken(ctx, userID, forum.TokenPasswordReset, "reset-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	send(t, &m, press("a brand new password"), press("tab"), press("a brand new password"), press("enter"))
	if m.err != nil || m.screen != screenList {
		t.Fatalf("password change failed: %v %v", m.err, m.screen)
	}
	_, hash, err := store.Credentials(ctx, "jules")
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte("a brand new password")) != nil {
		t.Fatalf("password not stored: %v", err)
	}
	if session, err := store.Session(ctx, "session"); session != nil || err != nil {
		t.Fatalf("session survived: %v %v", session, err)
	}
	if _, err := store.AuthTokenValid(ctx, "reset-hash", forum.TokenPasswordReset, time.Now()); err == nil {
		t.Fatal("reset link survived the password change")
	}
}

func TestAdminEmailsAResetLinkWhenConfigured(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID, err := store.CreateUser(ctx, "jules", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEmail(ctx, userID, "jules@example.org"); err != nil {
		t.Fatal(err)
	}
	relay := &fakeMailer{enabled: true}
	m := testModel(t, store, Options{BaseURL: "https://board.example.org", Mailer: relay})

	// With mail, the second action is "Email a reset link".
	send(t, &m, press("enter"), press("down"), press("enter"))
	if m.screen != screenResult || !m.emailed {
		t.Fatalf("email action failed: screen=%v emailed=%v err=%v", m.screen, m.emailed, m.err)
	}
	if len(relay.sent) != 1 || relay.sent[0].To != "jules@example.org" || !strings.Contains(relay.sent[0].Body, "/reset/") {
		t.Fatalf("message not sent: %+v", relay.sent)
	}
}

func TestAdminQuitsFromTheList(t *testing.T) {
	store := testStore(t)
	m := testModel(t, store, Options{})
	updated, cmd := m.Update(press("q"))
	model := updated.(Model)
	if !model.quitting || cmd == nil {
		t.Fatal("q did not quit")
	}
}

func TestAdminHandlesAnEmptyBoard(t *testing.T) {
	store := testStore(t)
	m := testModel(t, store, Options{})
	// Enter on an empty list must not panic or change screens.
	send(t, &m, press("enter"))
	if m.screen != screenList {
		t.Fatalf("empty list opened a screen: %v", m.screen)
	}
}

func memberWithEmail(t *testing.T, store *forum.Store) {
	t.Helper()
	ctx := context.Background()
	userID, err := store.CreateUser(ctx, "jules", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEmail(ctx, userID, "jules@example.org"); err != nil {
		t.Fatal(err)
	}
}

func TestAdminNeverEmailsARelativeLink(t *testing.T) {
	store := testStore(t)
	memberWithEmail(t, store)
	relay := &fakeMailer{enabled: true}
	m := testModel(t, store, Options{Mailer: relay})
	send(t, &m, press("enter"))
	for _, choice := range m.items {
		if choice.kind == actionResetEmail {
			t.Fatal("offered to email a link without WITMOOT_BASE_URL")
		}
	}
	send(t, &m, press("enter"))
	if m.screen != screenResult || !strings.HasPrefix(m.link, "/reset/") {
		t.Fatalf("relative link not shown: %v %q", m.screen, m.link)
	}
	send(t, &m, press("e"))
	if len(relay.sent) != 0 || m.emailed || strings.Contains(m.resultView(), "Press e") {
		t.Fatalf("relative link was emailed or offered: %+v", relay.sent)
	}
	// Even if asked directly, the path is refused rather than mailed.
	m.sendReset()
	if !errors.Is(m.err, errRelativeLink) || len(relay.sent) != 0 {
		t.Fatalf("relative link sent: %v %+v", m.err, relay.sent)
	}
}

func TestAdminEmailMatchesTheWebEmailAndRealExpiry(t *testing.T) {
	store := testStore(t)
	memberWithEmail(t, store)
	relay := &fakeMailer{enabled: true}
	m := testModel(t, store, Options{BaseURL: "https://board.example.org", Mailer: relay})
	send(t, &m, press("enter"), press("down"), press("enter"))
	if len(relay.sent) != 1 {
		t.Fatalf("message not sent: %v", m.err)
	}
	want := forum.ResetMessage(m.target.User, "Test table", m.link, forum.ResetLinkTTL)
	if relay.sent[0] != want {
		t.Fatalf("admin email differs from the web email:\n%+v\n%+v", relay.sent[0], want)
	}
	expiry := "expires in " + forum.HumanDuration(forum.ResetLinkTTL)
	if !strings.Contains(relay.sent[0].Body, expiry) || !strings.Contains(m.resultView(), expiry) {
		t.Fatalf("stated expiry does not match the link lifetime %s", forum.ResetLinkTTL)
	}
	token := m.link[strings.LastIndex(m.link, "/")+1:]
	user, err := store.AuthTokenValid(context.Background(), forum.TokenHash(token), forum.TokenPasswordReset, time.Now().Add(forum.ResetLinkTTL-time.Minute))
	if err != nil || user.Username != "jules" {
		t.Fatalf("link expires before its stated lifetime: %v", err)
	}
	if _, err := store.AuthTokenValid(context.Background(), forum.TokenHash(token), forum.TokenPasswordReset, time.Now().Add(forum.ResetLinkTTL+time.Minute)); err == nil {
		t.Fatal("link outlives its stated lifetime")
	}
}

// closeTest closes a test resource and reports a failure.
func closeTest(t testing.TB, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Error(err)
	}
}
