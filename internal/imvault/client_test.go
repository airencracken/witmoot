package imvault

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	c, err := New("https://vault.example/base")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Result(), nil
	})
	return c
}

var pngBytes = []byte("\x89PNG\r\n\x1a\nfixture")

func TestVersionedRenditionLinks(t *testing.T) {
	c, _ := New("https://vault.example/base")
	for _, rendition := range []string{"thumb", "preview"} {
		link := "https://vault.example/base/f/abc123/" + rendition + "?v=0123456789abcdef"
		if id, err := c.ParseLink(link); err != nil || id != "abc123" {
			t.Errorf("current imvault link rejected: %q %v", id, err)
		}
	}
	for _, suffix := range []string{
		"/thumb?v=", "/thumb?v=nope", "/thumb?v=0123456789abcdef&token=secret",
		"/thumb?v=0123456789abcdef&v=0123456789abcdef", "/thumb?v=%ZZ", "/thumb?v=0123456789abcdef;ignored=1",
		"/raw?v=0123456789abcdef", "?v=0123456789abcdef", "/thumb?v=0123456789abcdef#secret",
	} {
		if _, err := c.ParseLink("https://vault.example/base/f/abc123" + suffix); err == nil {
			t.Errorf("accepted unsupported query: %s", suffix)
		}
	}
}

func TestLinksStayOnConfiguredServer(t *testing.T) {
	c, _ := New("https://vault.example/base/")
	for _, suffix := range []string{"", "/raw", "/preview", "/thumb"} {
		id, err := c.ParseLink("https://vault.example/base/f/abc123" + suffix)
		if err != nil || id != "abc123" {
			t.Fatalf("link: %q %v", id, err)
		}
	}
	for _, link := range []string{
		"http://vault.example/base/f/abc", "https://evil.example/base/f/abc", "https://vault.example.evil/base/f/abc",
		"https://vault.example/base/f/abc?token=secret", "https://vault.example/base/f/abc#x", "https://user@vault.example/base/f/abc",
		"https://vault.example/f/abc", "https://vault.example/base/f/../secret", "https://vault.example/base/f/%2e%2e",
		"https://vault.example/base/f/a%2fb", "//vault.example/base/f/abc", "file:///etc/passwd", "https://vault.example/base/f/abc/other",
		"https://vault.example/base/f/", "https://vault.example/base/f/" + strings.Repeat("a", 65),
	} {
		if _, err := c.ParseLink(link); err == nil {
			t.Errorf("accepted %q", link)
		}
	}
	for _, base := range []string{"", "ftp://vault.example", "https://key@vault.example", "https://vault.example?key=x", "https://vault.example/#x"} {
		if _, err := New(base); err == nil {
			t.Errorf("accepted base %q", base)
		}
	}
}

func TestAccountLibraryAndPrivateUploadContract(t *testing.T) {
	ctx := context.Background()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Cookie") != "" {
			t.Fatal("credential contract")
		}
		switch r.URL.Path {
		case "/base/api/v1/me":
			io.WriteString(w, `{"username":"alex"}`)
		case "/base/api/v1/files":
			if r.URL.Query().Get("q") != "tea & cake" || r.URL.Query().Get("limit") != "12" || r.URL.Query().Get("offset") != "12" {
				t.Fatal("library query")
			}
			io.WriteString(w, `{"files":[{"id":"photo","kind":"image"},{"id":"clip","kind":"video"}],"total":30}`)
		case "/base/api/v1/files/photo":
			io.WriteString(w, `{"id":"photo","kind":"image","name":"tea.png"}`)
		case "/base/api/v1/upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			if r.PostForm.Get("visibility") != "private" || r.PostForm.Get("metadata") != "hidden" {
				t.Fatal("unsafe upload defaults")
			}
			f, _, err := r.FormFile("files")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			data, _ := io.ReadAll(f)
			if !bytes.Equal(data, pngBytes) {
				t.Fatal("changed upload")
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"files":[{"id":"upload","kind":"image","visibility":"private"}]}`)
		case "/base/f/photo/preview":
			w.Header().Set("Content-Type", "text/html")
			w.Write(pngBytes)
		default:
			t.Fatalf("unexpected request %s", r.URL)
		}
	})
	if name, err := c.Me(ctx, "test-key"); err != nil || name != "alex" {
		t.Fatalf("me: %s %v", name, err)
	}
	if files, more, err := c.List(ctx, "test-key", "tea & cake", 12); err != nil || !more || len(files) != 1 {
		t.Fatalf("list: %+v %t %v", files, more, err)
	}
	if f, err := c.File(ctx, "test-key", "photo"); err != nil || f.Name != "tea.png" {
		t.Fatalf("file: %+v %v", f, err)
	}
	if f, err := c.Upload(ctx, "test-key", "tea.png", pngBytes); err != nil || f.ID != "upload" {
		t.Fatalf("upload: %+v %v", f, err)
	}
	if data, mime, err := c.Image(ctx, "test-key", "photo", "preview"); err != nil || mime != "image/png" || !bytes.Equal(data, pngBytes) {
		t.Fatalf("image: %s %v", mime, err)
	}
}

func TestUpstreamFailuresAndRedirects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"denied", 403, `{}`}, {"missing", 404, `{}`}, {"malformed", 200, `<html>`}, {"oversized", 200, strings.Repeat("x", (1<<20)+1)}, {"empty account", 200, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) })
			if _, err := c.Me(context.Background(), "key"); err == nil {
				t.Fatal("accepted bad upstream response")
			}
		})
	}
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "https://other.example/stolen")
		w.WriteHeader(302)
	})
	if _, err := c.Me(context.Background(), "secret"); err == nil || calls != 1 {
		t.Fatal("followed upstream redirect")
	}
	for _, body := range [][]byte{[]byte("<svg onload='x'>"), []byte("<html>"), append(pngBytes, make([]byte, MaxImageBytes)...)} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
		if _, _, err := c.Image(context.Background(), "", "photo", "thumb"); err == nil {
			t.Fatal("accepted invalid/oversized image")
		}
	}
}

func TestRejectedUploadsAndCleanup(t *testing.T) {
	calls, deleted := 0, ""
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == "DELETE" {
			deleted = r.URL.Path
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"files": []File{{ID: "newfile", Kind: "image", Visibility: "public"}}})
	})
	for _, data := range [][]byte{[]byte("not an image"), append(pngBytes, make([]byte, MaxImageBytes)...)} {
		if _, err := c.Upload(context.Background(), "key", "bad.png", data); err == nil {
			t.Fatal("accepted invalid upload")
		}
	}
	if calls != 0 {
		t.Fatal("invalid uploads reached upstream")
	}
	if _, err := c.Upload(context.Background(), "key", "good.png", pngBytes); err == nil {
		t.Fatal("accepted public upload")
	}
	if deleted != "/base/api/v1/files/newfile" || calls != 2 {
		t.Fatalf("cleanup: %s %d", deleted, calls)
	}
}
