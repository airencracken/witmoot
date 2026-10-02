// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"bufio"
	"context"
	"errors"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/airencracken/comfylib/smtp"
)

// lineBreaks are the characters that could end a mail header or a line of a
// page title, as an attacker would try them.
var lineBreaks = []string{"\r\n", "\n", "\r", "\u0085", "\x85", "\u2028", "\u2029", "\v", "\f", "\t", "\x00"}

func TestSingleLineRejectsEveryLineBreak(t *testing.T) {
	for _, value := range []string{"", "Our little board", "Café crew", "日本語の掲示板", "a - b: c", "=?utf-8?q?x?="} {
		if !SingleLine(value) {
			t.Errorf("rejected %q", value)
		}
	}
	for _, br := range lineBreaks {
		for _, value := range []string{"Picnic" + br + "Bcc: victim@example.org", br + "Picnic", "Picnic" + br} {
			if SingleLine(value) {
				t.Errorf("accepted %q", value)
			}
		}
	}
}

func TestBoardNamesAndCategoriesMustBeOneLine(t *testing.T) {
	app, client := newTestApp(t, false)
	ownerID := signInTest(t, app, client, true)
	form := func(name, category string) url.Values {
		return url.Values{"name": {name}, "category": {category}, "description": {"Line one\nline two"}, "visibility": {"site"}, "revision": {"0"}}
	}
	for _, br := range lineBreaks {
		w := client.post("/boards/new", form("Picnic"+br+"Subject: injected", "Friends"))
		requireStatus(t, w, 422)
		if !strings.Contains(w.Body.String(), "on one line") {
			t.Fatalf("name with %q: the reason was not shown: %s", br, w.Body.String())
		}
		requireStatus(t, client.post("/boards/new", form("Picnic", "Friends"+br+"Family")), 422)
		if _, err := app.store.SaveBoard(context.Background(), ownerID, Board{Name: "Picnic" + br, Category: "Friends"}, nil, nil); !errors.Is(err, errBoardDetails) {
			t.Fatalf("store accepted a name with %q: %v", br, err)
		}
	}
	// A multi-line description is still fine, and a valid board saves.
	requireStatus(t, client.post("/boards/new", form("Picnic planning", "Friends")), 303)
}

func TestSiteNamesMustBeOneLine(t *testing.T) {
	for _, br := range lineBreaks {
		name := "Our board" + br + "Bcc: victim@example.org"
		if err := CheckSiteName(name); err == nil {
			t.Errorf("CheckSiteName accepted %q", name)
		}
		if _, err := New(testStore(t), Config{Name: name}); err == nil || !strings.Contains(err.Error(), "WITMOOT_NAME") {
			t.Errorf("New accepted site name %q: %v", name, err)
		}
	}
	if err := CheckSiteName(strings.Repeat("é", 81)); err == nil {
		t.Error("CheckSiteName accepted 81 characters")
	}
	if err := CheckSiteName("\xff"); err == nil {
		t.Error("CheckSiteName accepted invalid UTF-8")
	}
	for _, name := range []string{"Witmoot", "Café crew", strings.Repeat("é", 80)} {
		if err := CheckSiteName(name); err != nil {
			t.Errorf("CheckSiteName refused %q: %v", name, err)
		}
	}

	app, client := newTestApp(t, false)
	signInTest(t, app, client, true)
	form := url.Values{"mode": {"private"}, "site_name": {"Our table"}, "source_url": {""}, "welcome_title": {"Welcome"}, "welcome_text": {"A place\nfor friends"}, "house_rules": {""}, "owner_contact": {""}}
	requireStatus(t, client.post("/settings", form), 303)
	for _, br := range lineBreaks {
		form.Set("site_name", "Our table"+br+"Bcc: victim@example.org")
		requireStatus(t, client.post("/settings", form), 422)
	}
	brand, err := app.store.LoadBranding(context.Background(), SiteBranding{})
	if err != nil || brand.Name != "Our table" {
		t.Fatalf("a multi-line site name was saved: %q %v", brand.Name, err)
	}
}

// Whatever site name the settings page or WITMOOT_NAME accepts, the reset
// email's subject stays on one line, so the mail sender never refuses it.
func TestAcceptedSiteNamesGiveOneLineSubjects(t *testing.T) {
	seed := rand.Uint64()
	t.Logf("seed %d", seed)
	random := rand.New(rand.NewPCG(seed, 0))
	alphabet := []string{"a", "B", " ", "é", "日", "-", ":", "<", ">", "\r", "\n", "\t", "\u0085", "\x85", "\u2028", "\u2029", "\x00", "\x7f", "=?", "\xff"}
	accepted := 0
	for range 20000 {
		var b strings.Builder
		for range random.IntN(12) {
			b.WriteString(alphabet[random.IntN(len(alphabet))])
		}
		name := b.String()
		branding, brandErr := cleanBranding(SiteBranding{Name: name})
		for _, candidate := range []struct {
			name string
			ok   bool
		}{{branding.Name, brandErr == nil}, {name, CheckSiteName(name) == nil}} {
			if !candidate.ok {
				continue
			}
			accepted++
			subject := ResetMessage(User{Username: "jules", Email: "jules@example.org"}, candidate.name, "https://board.example.org/reset/x", ResetLinkTTL).Subject
			if strings.ContainsAny(subject, "\r\n") {
				t.Fatalf("site name %q gives subject %q", candidate.name, subject)
			}
		}
	}
	if accepted < 1000 {
		t.Fatalf("only %d names were accepted", accepted)
	}
}

// An unknown TLS mode is a startup error, never a quiet fall back to sending
// in plain text.
func TestUnknownMailTLSModeStopsStartup(t *testing.T) {
	relay := smtp.Config{Host: "relay.example.org", Port: 587, From: "Board <no-reply@example.org>"}
	for _, mode := range []smtp.TLSMode{"plaintext", "STARTTLS ", "tls", "off"} {
		relay.Mode = mode
		if _, err := New(testStore(t), Config{Mail: relay}); err == nil {
			t.Errorf("mode %q was accepted", mode)
		}
	}
	for _, mode := range []smtp.TLSMode{"", smtp.TLSStartTLS, smtp.TLSImplicit, smtp.TLSNone} {
		relay.Mode = mode
		if _, err := New(testStore(t), Config{Mail: relay}); err != nil {
			t.Errorf("mode %q: %v", mode, err)
		}
	}
	relay.Mode, relay.From = smtp.TLSStartTLS, "not an address"
	if _, err := New(testStore(t), Config{Mail: relay}); err == nil {
		t.Error("a malformed sender was accepted")
	}
}

// fakeRelay is a minimal SMTP server without TLS that records each message.
type fakeRelay struct {
	listener net.Listener
	mu       sync.Mutex
	messages []string
	done     sync.WaitGroup
}

func startFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	relay := &fakeRelay{listener: listener}
	relay.done.Add(1)
	go relay.serve()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		relay.done.Wait()
	})
	return relay
}

func (f *fakeRelay) port() int { return f.listener.Addr().(*net.TCPAddr).Port }

func (f *fakeRelay) serve() {
	defer f.done.Done()
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		f.converse(conn)
	}
}

// converse speaks just enough SMTP for net/smtp, then closes the connection.
// The relay is test scaffolding, so a failed write ends the conversation.
func (f *fakeRelay) converse(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	reply := func(line string) bool {
		_, err := conn.Write([]byte(line + "\r\n"))
		return err == nil
	}
	if !reply("220 relay.test ESMTP") {
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		switch verb {
		case "EHLO", "HELO", "MAIL", "RCPT", "RSET", "NOOP":
			if !reply("250 ok") {
				return
			}
		case "DATA":
			if !reply("354 go ahead") {
				return
			}
			var body strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" {
					break
				}
				body.WriteString(line)
			}
			f.mu.Lock()
			f.messages = append(f.messages, body.String())
			f.mu.Unlock()
			if !reply("250 queued") {
				return
			}
		case "QUIT":
			reply("221 bye")
			return
		default:
			if !reply("502 unsupported") {
				return
			}
		}
	}
}

func (f *fakeRelay) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

// The reset link travels through comfylib's sender to a real socket, with the
// site name as a one-line subject.
func TestResetMailReachesTheRelay(t *testing.T) {
	relay := startFakeRelay(t)
	app, err := New(testStore(t), Config{Name: "Café crew", Mail: smtp.Config{Host: "127.0.0.1", Port: relay.port(), From: "Board <no-reply@example.org>", Mode: smtp.TLSNone}})
	if err != nil {
		t.Fatal(err)
	}
	client := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	signInTest(t, app, client, true)
	memberID := testMember(t, app.store, "jules")
	if err := app.store.SetEmail(context.Background(), memberID, "jules@example.org"); err != nil {
		t.Fatal(err)
	}
	w := client.post("/members/"+strconv.FormatInt(memberID, 10)+"/reset", url.Values{"email": {"1"}})
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "emailed to") {
		t.Fatalf("the reset link was not emailed: %s", w.Body.String())
	}
	messages := relay.received()
	if len(messages) != 1 {
		t.Fatalf("relay received %d messages", len(messages))
	}
	headers, body, _ := strings.Cut(messages[0], "\r\n\r\n")
	subject := ""
	for _, line := range strings.Split(headers, "\r\n") {
		if value, ok := strings.CutPrefix(line, "Subject: "); ok {
			subject = value
		}
	}
	if decoded, err := new(mime.WordDecoder).DecodeHeader(subject); err != nil || decoded != "Choose a new password for Café crew" {
		t.Fatalf("subject %q decodes to %q (%v) in\n%s", subject, decoded, err, headers)
	}
	if !strings.Contains(headers, "To: jules@example.org\r\n") || !resetLinkPattern.MatchString(body) {
		t.Fatalf("message:\n%s", messages[0])
	}
}
