package imvault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestOwnershipCacheIsBriefBoundedAndCredentialScoped(t *testing.T) {
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer owner" {
			w.WriteHeader(403)
			return
		}
		_ = json.NewEncoder(w).Encode(File{ID: "photo", Kind: "image"})
	})
	for i := 0; i < 3; i++ {
		if _, err := client.CachedFile(t.Context(), "owner", "photo"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("repeated ownership checks=%d", calls)
	}
	for i := 0; i < 2; i++ {
		if _, err := client.CachedFile(t.Context(), "other-account", "photo"); err == nil {
			t.Fatal("credential change inherited ownership")
		}
	}
	if calls != 3 {
		t.Fatal("ownership failures were cached")
	}
	key := sha256.Sum256([]byte("owner\x00photo"))
	client.cacheMu.Lock()
	entry := client.ownership[key]
	entry.expires = time.Now().Add(-time.Second)
	client.ownership[key] = entry
	client.cacheMu.Unlock()
	if _, err := client.CachedFile(t.Context(), "owner", "photo"); err != nil || calls != 4 {
		t.Fatal("expired ownership not rechecked", err, calls)
	}
	client.cacheMu.Lock()
	delete(client.ownership, key)
	for i := 0; i < 1024; i++ {
		client.ownership[sha256.Sum256([]byte{byte(i), byte(i >> 8)})] = entry
	}
	client.cacheMu.Unlock()
	if _, err := client.CachedFile(t.Context(), "owner", "photo"); err != nil {
		t.Fatal(err)
	}
	if len(client.ownership) > 1024 {
		t.Fatalf("unbounded ownership cache: %d", len(client.ownership))
	}
}

type countBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *countBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *countBody) Close() error { b.closed = true; return nil }

func TestImagesStreamAndEnforceUnknownLengthLimit(t *testing.T) {
	for _, size := range []int{100_000, MaxImageBytes + 1} {
		data := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, size-len(pngBytes))...)
		body := &countBody{Reader: bytes.NewReader(data)}
		client, _ := New("https://vault.example")
		client.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header), ContentLength: -1}, nil
		})
		stream, mime, err := client.StreamImage(context.Background(), "owner", "photo", "preview")
		if err != nil || mime != "image/png" || body.read != 512 {
			t.Fatalf("stream buffered the body or missed MIME: %d %q %v", body.read, mime, err)
		}
		n, err := io.Copy(io.Discard, stream)
		if size <= MaxImageBytes && (err != nil || n != int64(size)) {
			t.Fatalf("valid stream %d %v", n, err)
		}
		if size > MaxImageBytes && (err == nil || n > MaxImageBytes) {
			t.Fatalf("oversize stream escaped limit %d %v", n, err)
		}
		if err := stream.Close(); err != nil || !body.closed {
			t.Fatal("upstream connection not closed")
		}
	}
}
