// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every view the handlers set must map to a template that exists, and any
// other view must fall back to the plain error page instead of a helper.
func TestPageTemplatesCoverEveryViewAndRejectOthers(t *testing.T) {
	app, _ := newTestApp(t, false)
	for view, name := range pageTemplates {
		if app.templates.Lookup(name) == nil {
			t.Errorf("view %q names missing template %q", view, name)
		}
	}
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	assigned := regexp.MustCompile(`View(?:: *|, [^=]*= *|\s*=\s*)"([a-z-]+)"`)
	seen := map[string]bool{}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range assigned.FindAllStringSubmatch(string(content), -1) {
			seen[match[1]] = true
		}
	}
	if len(seen) < 30 {
		t.Fatalf("found only %d views; the source pattern no longer matches", len(seen))
	}
	for view := range seen {
		if _, ok := pageTemplates[view]; !ok && view != "error" {
			t.Errorf("handlers set view %q, which has no template", view)
		}
	}
	for _, view := range []string{"", "error", "csrf", "layout", "export", "theme-picker", "error-page", "../home", "HOME", "home ", "<script>"} {
		if got := pageTemplate(view); got != "error-page" && pageTemplates[view] != got {
			t.Errorf("view %q drew %q, want error-page", view, got)
		}
		if _, ok := pageTemplates[view]; ok {
			t.Errorf("helper %q must not be a page view", view)
		}
	}
}

func TestUnknownViewRendersEscapedErrorPage(t *testing.T) {
	app, _ := newTestApp(t, false)
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), stateKey{}, requestState{Mode: ModePrivate}))
	w := httptest.NewRecorder()
	app.render(w, r, http.StatusTeapot, Page{View: "csrf", Title: "Odd <b>page</b>", Error: "<script>alert(1)</script>"})
	body := w.Body.String()
	if w.Code != http.StatusTeapot || !strings.Contains(body, "<h1>Odd &lt;b&gt;page&lt;/b&gt;</h1>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") || strings.Contains(body, "<script>alert") {
		t.Fatalf("unexpected error page: %d\n%s", w.Code, body)
	}
	if !strings.Contains(body, `<a href="/">Back to our board</a>`) {
		t.Fatal("error page lost its way home")
	}
}

// One partial draws the theme picker for visitors and members alike.
func TestThemePickerComesFromOnePartial(t *testing.T) {
	layout, err := os.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(layout, []byte("data-theme-select")); n != 1 {
		t.Fatalf("layout has %d theme selects, want one partial", n)
	}
	app, visitor := newTestApp(t, false)
	signInTest(t, app, &testClient{app: app, cookies: map[string]*http.Cookie{}}, true)
	if body := visitor.request("GET", "/login", nil, nil).Body.String(); strings.Count(body, "data-theme-select") != 1 {
		t.Fatal("visitor page should render the theme picker once")
	}
	member := memberClient(t, app, "jules")
	if body := member.request("GET", "/account", nil, nil).Body.String(); strings.Count(body, "data-theme-select") != 1 {
		t.Fatal("member page should render the theme picker once")
	}
}

// Templates indent with tabs only, and files are named after what they draw.
func TestTemplatesUseTabsAndNamedFiles(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(strings.TrimLeft(line, "\t"), " ") && strings.TrimSpace(line) != "" {
				t.Errorf("%s:%d indents with spaces", file, i+1)
			}
		}
	}
	for file, define := range map[string]string{"templates/account.html": `{{define "account"}}`, "templates/moderation.html": `{{define "moderation"}}`} {
		content, err := os.ReadFile(file)
		if err != nil || !bytes.Contains(content, []byte(define)) {
			t.Errorf("%s should define %s: %v", file, define, err)
		}
	}
	for _, old := range []string{"templates/resets.html", "templates/community.html"} {
		if _, err := os.Stat(old); err == nil {
			t.Errorf("%s should have been renamed", old)
		}
	}
}
