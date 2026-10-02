package forum

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// slowPost sends a request whose body arrives in two halves, pausing between
// them, and returns the status line or the connection error.
func slowPost(t *testing.T, address, path, contentType, body string, pause time.Duration) (string, error) {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Log(err)
		}
	}()
	if _, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: board.test\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n%s", path, contentType, len(body), body[:len(body)/2]); err != nil {
		return "", err
	}
	time.Sleep(pause)
	if _, err := conn.Write([]byte(body[len(body)/2:])); err != nil {
		return "", err
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return bufio.NewReader(conn).ReadString('\n')
}

func TestRequestBodyDeadlinesFollowTheRoute(t *testing.T) {
	form, upload := formTimeout, uploadTimeout
	formTimeout, uploadTimeout = 300*time.Millisecond, 5*time.Second
	t.Cleanup(func() { formTimeout, uploadTimeout = form, upload })
	app, _ := newTestApp(t, false)
	server := httptest.NewUnstartedServer(app)
	server.Config.ReadHeaderTimeout = time.Second
	server.Start()
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")

	// A slow ordinary form is cut off; the handler sees an unreadable form.
	status, err := slowPost(t, address, "/login", "application/x-www-form-urlencoded", "username=alex&password=a+long+test+password", time.Second)
	if err == nil && !strings.Contains(status, " 400 ") {
		t.Fatalf("a slow form body was accepted: %q", status)
	}
	// A slow upload of the same pace is still read. The missing CSRF token
	// proves the whole form arrived and was parsed.
	body := "--b\r\nContent-Disposition: form-data; name=\"note\"\r\n\r\nhello\r\n--b--\r\n"
	status, err = slowPost(t, address, "/account/avatar", "multipart/form-data; boundary=b", body, time.Second)
	if err != nil || !strings.Contains(status, fmt.Sprintf(" %d ", http.StatusForbidden)) {
		t.Fatalf("a slow upload was cut off: %q %v", status, err)
	}
}
