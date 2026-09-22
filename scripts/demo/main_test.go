package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDemoAccountsAudiencesAndCleanup(t *testing.T) {
	production := filepath.Join(t.TempDir(), "witmoot.db")
	if err := os.WriteFile(production, []byte("leave this board alone"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WITMOOT_DATA_DIR", filepath.Dir(production))
	t.Setenv("WITMOOT_ADDR", "0.0.0.0:1")
	t.Setenv("WITMOOT_BASE_URL", "https://production.example")
	t.Setenv("WITMOOT_SECURE_COOKIES", "true")
	t.Setenv("WITMOOT_IMVAULT_URL", "not a valid URL")
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	reader, writer := io.Pipe()
	defer reader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		err := run(ctx, 0, writer)
		writer.CloseWithError(err)
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		reader.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("demo did not stop")
		}
		if contents, err := os.ReadFile(production); err != nil || string(contents) != "leave this board alone" {
			t.Errorf("production data changed: %q (%v)", contents, err)
		}
		if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 0 {
			t.Errorf("demo did not clean up: %v (%v)", entries, err)
		}
	})
	ready := make(chan string, 1)
	go func() {
		base := ""
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "Open ") {
				base = strings.TrimPrefix(scanner.Text(), "Open ")
			}
			if strings.HasPrefix(scanner.Text(), "Press Ctrl-C") {
				ready <- base
				io.Copy(io.Discard, reader)
				return
			}
		}
		ready <- ""
	}()
	var base string
	select {
	case base = <-ready:
	case <-time.After(15 * time.Second):
		t.Fatal("demo did not become ready")
	}
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("demo listener must be loopback: %q", base)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if body := getPage(t, client, base+"/healthz", 200); strings.TrimSpace(body) != "ok" {
		t.Fatalf("health response: %q", body)
	}
	for _, tc := range []struct {
		name                      string
		members, owners, settings bool
	}{
		{"", false, false, false},
		{"freya", true, false, false},
		{"jules", true, false, false},
		{"demo", true, true, true},
	} {
		t.Run("reader="+tc.name, func(t *testing.T) {
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
			if tc.name != "" {
				login(t, client, base, tc.name)
				status := http.StatusForbidden
				if tc.settings {
					status = http.StatusOK
				}
				getPage(t, client, base+"/settings", status)
			}
			recent := getPage(t, client, base+"/recent", 200)
			if strings.Contains(recent, "A little surprise for Sunday") != (tc.name != "") {
				t.Fatal("private demo board has the wrong audience")
			}
			home := getPage(t, client, base+"/", 200)
			if strings.Contains(home, "The planning nook") != (tc.name != "") || !strings.Contains(home, "Public browsing") {
				t.Fatal("private demo board or site status is not shown correctly")
			}
			if tc.name != "" {
				private := getPage(t, client, base+"/topics/8", 200)
				if !strings.Contains(private, "class=\"edited\"") || strings.Contains(private, "Post reply</button>") != (tc.name != "jules") {
					t.Fatal("demo edit marker or read-only permissions missing")
				}
			}
			for _, title := range []string{"Pull up a chair", "Sunday soup and board games", "A birdhouse with a slightly wonky roof", "Good books for a rainy afternoon", "How we use this little corner"} {
				if !strings.Contains(recent, title) {
					t.Errorf("missing sample conversation %q", title)
				}
			}
			if strings.Contains(recent, "Dinner plans for the regulars") != tc.members || strings.Contains(recent, "A little note for the host") != tc.owners {
				t.Fatal("sample audiences do not match the reader")
			}
			search := getPage(t, client, base+"/search?q=rainy", 200)
			if !strings.Contains(search, "Good books for a rainy afternoon") {
				t.Fatal("sample conversations are not searchable")
			}
		})
	}
}

func login(t *testing.T, client *http.Client, base, username string) {
	t.Helper()
	getPage(t, client, base+"/login", 200)
	origin, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	csrf := ""
	for _, cookie := range client.Jar.Cookies(origin) {
		if cookie.Name == "witmoot_csrf" {
			csrf = cookie.Value
		}
	}
	response, err := client.PostForm(base+"/login", url.Values{"username": {username}, "password": {"demo-password"}, "csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Request.URL.Path != "/" {
		t.Fatalf("demo login failed: %d %s", response.StatusCode, response.Request.URL)
	}
}

func getPage(t *testing.T, client *http.Client, address string, status int) string {
	t.Helper()
	response, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("GET %s: status=%d, err=%v", address, response.StatusCode, err)
	}
	return string(body)
}

func TestDemoRefusesOccupiedPortWithoutCreatingData(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	err = run(context.Background(), listener.Addr().(*net.TCPAddr).Port, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "PORT=9000") {
		t.Fatalf("expected helpful occupied-port failure, got %v", err)
	}
	if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 0 {
		t.Fatalf("failed startup created data: %v (%v)", entries, err)
	}
}
