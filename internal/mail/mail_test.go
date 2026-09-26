package mail

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// received is one message captured by the fake relay.
type received struct {
	from string
	to   []string
	data string
}

// fakeSMTP is a minimal SMTP server: enough of the protocol to accept a message
// and hand it back for assertions.
type fakeSMTP struct {
	listener net.Listener
	messages chan received
	// advertiseStartTLS controls whether the server claims to support STARTTLS.
	// It is deliberately never honoured, so the test does not need a
	// certificate; the point is only to exercise the branch.
	advertiseStartTLS bool
	// requireAuth rejects mail unless an AUTH exchange happened.
	requireAuth bool
	authed      bool
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := &fakeSMTP{
		listener: listener,
		messages: make(chan received, 16),
	}

	go server.serve()

	t.Cleanup(func() { listener.Close() })
	return server
}

func (s *fakeSMTP) addr() (string, int) {
	host, port, _ := net.SplitHostPort(s.listener.Addr().String())
	number := 0
	for _, c := range port {
		number = number*10 + int(c-'0')
	}
	return host, number
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	reply := func(line string) {
		writer.WriteString(line + "\r\n")
		writer.Flush()
	}

	reply("220 fake ESMTP ready")

	var current received
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		upper := strings.ToUpper(command)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			reply("250-fake greets you")
			if s.advertiseStartTLS {
				reply("250-STARTTLS")
			}
			reply("250 AUTH PLAIN")

		case upper == "STARTTLS":
			// The test never negotiates real TLS.
			reply("454 TLS not available in this test")

		case strings.HasPrefix(upper, "AUTH"):
			s.authed = true
			reply("235 authenticated")

		case strings.HasPrefix(upper, "MAIL FROM:"):
			if s.requireAuth && !s.authed {
				reply("530 authentication required")
				continue
			}
			current = received{from: strings.TrimSpace(command[len("MAIL FROM:"):])}
			reply("250 ok")

		case strings.HasPrefix(upper, "RCPT TO:"):
			current.to = append(current.to, strings.TrimSpace(command[len("RCPT TO:"):]))
			reply("250 ok")

		case upper == "DATA":
			reply("354 end with <CRLF>.<CRLF>")
			body, err := textproto.NewReader(reader).ReadDotBytes()
			if err != nil {
				return
			}
			current.data = string(body)
			s.messages <- current
			current = received{}
			reply("250 queued")

		case upper == "QUIT":
			reply("221 bye")
			return

		case upper == "RSET":
			current = received{}
			reply("250 ok")

		default:
			reply("250 ok")
		}
	}
}

// wait returns the next captured message, or fails.
func (s *fakeSMTP) wait(t *testing.T) received {
	t.Helper()

	select {
	case msg := <-s.messages:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message arrived")
		return received{}
	}
}

func TestSendDeliversThroughARelay(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.addr()

	sender := NewSMTP(Config{
		Host: host,
		Port: port,
		From: "Witmoot <no-reply@example.org>",
		Mode: TLSNone,
	})

	if !sender.Enabled() {
		t.Fatal("a configured sender should report as enabled")
	}

	err := sender.Send(context.Background(), Message{
		To:      "alice@example.org",
		Subject: "Reset your password",
		Body:    "Open this link: https://board.example.org/reset/abc123\n",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	msg := server.wait(t)
	if msg.from != "<no-reply@example.org>" {
		t.Errorf("envelope sender = %q, want the address from the From header", msg.from)
	}
	if len(msg.to) != 1 || msg.to[0] != "<alice@example.org>" {
		t.Errorf("recipients = %v", msg.to)
	}

	for _, want := range []string{
		"From: Witmoot <no-reply@example.org>",
		"To: alice@example.org",
		"Subject: Reset your password",
		"Content-Type: text/plain",
		"https://board.example.org/reset/abc123",
	} {
		if !strings.Contains(msg.data, want) {
			t.Errorf("message is missing %q:\n%s", want, msg.data)
		}
	}
}

func TestSendAuthenticatesWhenCredentialsAreSet(t *testing.T) {
	server := newFakeSMTP(t)
	server.requireAuth = true
	host, port := server.addr()

	sender := NewSMTP(Config{
		Host:     host,
		Port:     port,
		Username: "relay-user",
		Password: "relay-pass",
		From:     "no-reply@example.org",
		Mode:     TLSNone,
	})

	// PlainAuth only hands over credentials on an unencrypted connection when
	// the relay is on the loopback interface, which this one is.
	if err := sender.Send(context.Background(), Message{
		To:      "alice@example.org",
		Subject: "Hello",
		Body:    "Body",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if !server.authed {
		t.Error("the relay was never sent an AUTH exchange")
	}
	server.wait(t)
}

func TestSendReportsDialFailure(t *testing.T) {
	// Nothing is listening on this port.
	sender := NewSMTP(Config{
		Host:    "127.0.0.1",
		Port:    1,
		From:    "no-reply@example.org",
		Mode:    TLSNone,
		Timeout: time.Second,
	})

	if err := sender.Send(context.Background(), Message{
		To:      "alice@example.org",
		Subject: "Hello",
		Body:    "Body",
	}); err == nil {
		t.Fatal("expected a dial failure")
	}
}

func TestSendHonoursContextCancellation(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.addr()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sender := NewSMTP(Config{Host: host, Port: port, From: "a@example.org", Mode: TLSNone})
	if err := sender.Send(ctx, Message{To: "b@example.org", Subject: "x", Body: "y"}); err == nil {
		t.Fatal("expected a cancelled context to stop the send")
	}
}

func TestDisabledSenderReportsItself(t *testing.T) {
	sender := Disabled{}
	if sender.Enabled() {
		t.Error("a disabled sender must not claim to be enabled")
	}
	if err := sender.Send(context.Background(), Message{To: "a@example.org", Subject: "x"}); err != nil {
		t.Errorf("a disabled sender should not error: %v", err)
	}
}

func TestHeadersCannotBeInjected(t *testing.T) {
	raw := buildMessage("no-reply@example.org", Message{
		To:      "alice@example.org\r\nBcc: attacker@example.org",
		Subject: "Reset\r\nX-Injected: yes",
		Body:    "Body",
	})
	message := string(raw)

	// Header section and body are separated by exactly one blank line.
	head, body, found := strings.Cut(message, "\r\n\r\n")
	if !found {
		t.Fatal("no header/body separator")
	}

	// The property that matters is that no *header line* was introduced. The
	// injected text may survive inside the original value, which is harmless.
	allowed := map[string]bool{
		"From": true, "To": true, "Subject": true, "Date": true,
		"MIME-Version": true, "Content-Type": true,
		"Content-Transfer-Encoding": true,
	}

	lines := strings.Split(head, "\r\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("header line without a colon: %q", line)
		}
		names = append(names, name)
		if !allowed[name] {
			t.Errorf("an unexpected header was injected: %q", name)
		}
	}

	for _, injected := range []string{"Bcc", "X-Injected"} {
		for _, name := range names {
			if name == injected {
				t.Errorf("%s became a real header", injected)
			}
		}
	}
	if len(names) != len(allowed) {
		t.Errorf("header count = %d, want %d: %v", len(names), len(allowed), names)
	}

	if body != "Body" {
		t.Errorf("body = %q, want %q", body, "Body")
	}
}

func TestBodyLineEndings(t *testing.T) {
	message := string(buildMessage("a@example.org", Message{
		To:      "b@example.org",
		Subject: "s",
		Body:    "line one\n.hidden\nline three\r\nline four",
	}))

	if strings.Contains(message, "\n") && strings.Contains(strings.ReplaceAll(message, "\r\n", ""), "\n") {
		t.Error("a bare newline survived into the message")
	}
	if !strings.Contains(message, "\r\n.hidden\r\n") {
		t.Errorf("a leading dot was changed before SMTP transport:\n%q", message)
	}
	if !strings.Contains(message, "line four") {
		t.Error("the last line was lost")
	}
}

func TestSendPreservesLeadingDots(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.addr()
	sender := NewSMTP(Config{Host: host, Port: port, From: "a@example.org", Mode: TLSNone})
	body := ".first\n.\n..third\nlast\n"
	if err := sender.Send(context.Background(), Message{To: "b@example.org", Subject: "Dots", Body: body}); err != nil {
		t.Fatal(err)
	}
	_, receivedBody, ok := strings.Cut(server.wait(t).data, "\n\n")
	if !ok || receivedBody != body {
		t.Fatalf("received body = %q, want %q", receivedBody, body)
	}
}

func TestStartTLSRefusesUnencryptedRelay(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.addr()
	sender := NewSMTP(Config{Host: host, Port: port, From: "a@example.org", Mode: TLSStartTLS})
	if err := sender.Send(context.Background(), Message{To: "b@example.org", Body: "reset secret"}); err == nil {
		t.Fatal("STARTTLS mode delivered a reset secret over plaintext")
	}
	select {
	case <-server.messages:
		t.Fatal("message reached an unencrypted relay")
	default:
	}
}

func TestSendBoundsStalledConnections(t *testing.T) {
	for _, mode := range []TLSMode{TLSNone, TLSImplicit} {
		for _, cancelAfterConnect := range []bool{false, true} {
			name := string(mode) + "/timeout"
			if cancelAfterConnect {
				name = string(mode) + "/cancel"
			}
			t.Run(name, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				accepted := make(chan net.Conn, 1)
				go func() {
					conn, err := listener.Accept()
					if err == nil {
						accepted <- conn
					}
				}()
				address := listener.Addr().(*net.TCPAddr)
				timeout := 50 * time.Millisecond
				if cancelAfterConnect {
					timeout = time.Minute
				}
				sender := NewSMTP(Config{Host: "127.0.0.1", Port: address.Port, From: "a@example.org", Mode: mode, Timeout: timeout})
				// A longer caller deadline must not disable the sender's timeout.
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- sender.Send(ctx, Message{To: "b@example.org", Body: "secret"}) }()
				select {
				case conn := <-accepted:
					defer conn.Close()
				case <-time.After(time.Second):
					t.Fatal("sender did not connect")
				}
				if cancelAfterConnect {
					cancel()
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("stalled send succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("send ignored its timeout or cancellation while waiting for the relay")
				}
			})
		}
	}
}

func TestEnvelopeSenderExtractsTheAddress(t *testing.T) {
	tests := map[string]string{
		"Witmoot <no-reply@example.org>": "no-reply@example.org",
		"no-reply@example.org":           "no-reply@example.org",
		"  spaced@example.org  ":         "spaced@example.org",
	}
	for in, want := range tests {
		if got := envelopeSender(in); got != want {
			t.Errorf("envelopeSender(%q) = %q, want %q", in, got, want)
		}
	}
}

// Ensure the fake server satisfies the parts of net.Conn we rely on.
var _ io.Closer = (*net.TCPConn)(nil)
