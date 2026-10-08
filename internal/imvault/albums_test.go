// SPDX-License-Identifier: AGPL-3.0-or-later

package imvault

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestAlbumLinksAndPreviewContract(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/base/api/v1/albums/summer/preview" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal(r.URL, r.Header)
		}
		fmt.Fprint(w, `{"title":"Summer","slug":"summer","visibility":"public","image_count":3}`)
	})
	if slug, err := c.ParseAlbumLink("https://vault.example/base/a/summer"); err != nil || slug != "summer" {
		t.Fatal(slug, err)
	}
	a, err := c.AlbumPreview(t.Context(), "secret", "summer")
	if err != nil || a.ImageCount != 3 {
		t.Fatal(a, err)
	}
	for _, raw := range []string{"https://other/base/a/summer", "https://vault.example/base/a/summer?token=x", "https://vault.example/base/a/../private", "https://user@vault.example/base/a/summer", "https://vault.example/base/a/summer#x"} {
		if _, err := c.ParseAlbumLink(raw); err == nil {
			t.Fatal(raw)
		}
	}
}
func TestPrivateMalformedAndUnavailableAlbumPreviews(t *testing.T) {
	for _, payload := range []string{`{"title":"secret","slug":"summer","visibility":"private","image_count":2}`, `{"title":"secret","slug":"summer","visibility":"members","image_count":2}`, `{"title":"x","slug":"other","visibility":"public","image_count":2}`, `{"title":"x","slug":"summer","visibility":"public","image_count":-1}`, `not-json`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, payload) })
		if a, err := c.AlbumPreview(t.Context(), "key", "summer"); err == nil || a.Title != "" {
			t.Fatal("metadata leaked", a, err)
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	if _, err := c.AlbumPreview(t.Context(), "key", "summer"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
