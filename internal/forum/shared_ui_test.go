// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/ui"
)

func TestSharedAccountAssets(t *testing.T) {
	_, guest := newTestApp(t, false)
	request := func(path string) (int, string, string) {
		response := guest.request("GET", path, nil, nil)
		return response.Code, response.Header().Get("Content-Type"), response.Body.String()
	}
	want, err := fs.ReadFile(ui.Assets(), "accounts.css")
	if err != nil {
		t.Fatal(err)
	}
	status, contentType, body := request("/static/comfyware/accounts.css")
	if status != http.StatusOK || !strings.HasPrefix(contentType, "text/css") || body != string(want) {
		t.Fatalf("shared stylesheet unavailable or changed: %d %s", status, contentType)
	}
	status, _, body = request("/login")
	local, shared := strings.Index(body, `href="/static/style.css"`), strings.Index(body, `href="/static/comfyware/accounts.css"`)
	if status != http.StatusOK || local < 0 || shared <= local {
		t.Fatal("shared styles must follow application styles")
	}
	for _, path := range []string{"/static/comfyware/missing.css", "/static/comfyware/uitest/invitations.mjs", "/static/comfyware/assets.go"} {
		if status, _, _ := request(path); status != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", path, status)
		}
	}
}
