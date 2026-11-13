// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/ui"
)

// The home page explains access in one plain sidebar sentence, for every mode,
// without a separate site access block.
func TestHomeExplainsAccessOnceInTheSidebar(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	member := memberClient(t, app, "jules")
	for mode, sentence := range map[Mode]string{
		ModePrivate:  "Members can read and join the shared conversations here. New people join by invitation.",
		ModeOpen:     "Public conversations are open to everyone. Create an account to take part.",
		ModePersonal: "Only owners can use this board.",
	} {
		selectMode(t, owner, mode)
		for name, client := range map[string]*testClient{"owner": owner, "member": member} {
			w := client.request("GET", "/", nil, nil)
			if mode == ModePersonal && name == "member" {
				continue
			}
			body := w.Body.String()
			if strings.Contains(body, "Site access") || strings.Contains(body, "site-access") {
				t.Errorf("%s in %s mode still sees the site access block", name, mode)
			}
			if strings.Count(body, sentence) != 1 {
				t.Errorf("%s in %s mode: want the sidebar sentence %q once", name, mode, sentence)
			}
		}
	}
}

// Group and individual grants are hidden until Selected members is chosen.
// The hiding is CSS alone, so the fields stay in the form and saving keeps
// working without JavaScript or :has() support.
func TestBoardGrantsWaitForSelectedMembers(t *testing.T) {
	f := privateBoardFixture(t)
	g := createTestGroup(t, f, "Friends", f.writer.ID)
	setTestGrants(t, f, map[int64]string{f.writer.ID: "write"}, map[int64]string{g.ID: "read"})
	owner := sessionClient(t, f.app, f.owner)
	body := owner.request("GET", fmt.Sprintf("/boards/%d/settings", f.boardID), nil, nil).Body.String()
	start, end := strings.Index(body, `<div class="selected-access">`), strings.Index(body, `<div class="form-footer">`)
	if start < 0 || end < start {
		t.Fatalf("grant fields are not wrapped:\n%s", body)
	}
	wrapped := body[start:end]
	for _, field := range []string{"<legend>Group permissions</legend>", "<legend>Individual overrides</legend>", fmt.Sprintf(`name="group_%d"`, g.ID), fmt.Sprintf(`name="access_%d"`, f.writer.ID)} {
		if !strings.Contains(wrapped, field) {
			t.Errorf("%s is outside the selected-members wrapper", field)
		}
	}
	for _, help := range []string{"Someone in several groups gets the most generous grant.", "Any choice other than Use groups applies to that person whatever their groups say."} {
		if !strings.Contains(wrapped, help) {
			t.Errorf("missing help %q", help)
		}
	}
	css, err := assets.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), `.settings-form:has(input[name="visibility"][value="site"]:checked) .selected-access { display: none; }`) {
		t.Fatal("the selected-members rule is missing from the stylesheet")
	}
	group := owner.request("GET", fmt.Sprintf("/groups/%d", g.ID), nil, nil).Body.String()
	if !strings.Contains(group, "Individual overrides beat group grants, and the most generous group grant wins.") || !strings.Contains(group, `class="access-report"`) {
		t.Fatal("restricted board report lost its table or help")
	}
	b, err := f.app.store.Board(context.Background(), f.boardID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"revision": {fmt.Sprint(b.Revision)}, "name": {b.Name}, "category": {b.Category}, "visibility": {"site"}, fmt.Sprintf("group_%d", g.ID): {"write"}}
	requireStatus(t, owner.post(fmt.Sprintf("/boards/%d/settings", f.boardID), form), 303)
	group = owner.request("GET", fmt.Sprintf("/groups/%d", g.ID), nil, nil).Body.String()
	if strings.Contains(group, `class="access-report"`) || !strings.Contains(group, "group grants wait until it switches to Selected members only") {
		t.Fatalf("site-rules board should show only the waiting note:\n%s", group)
	}
}

// Settings lead with identity and mode, and keep server details at the bottom.
func TestSettingsOrderAndServerSection(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	body := owner.request("GET", "/settings", nil, nil).Body.String()
	order := []string{"<h1>Site settings</h1>", "<legend>Make this place yours</legend>", "<legend>Who is this site for?</legend>", "<legend>House rules and owners</legend>", "<h2>Favicon and mascot</h2>", `<h2 id="server-heading">Server</h2>`, "Running Witmoot devel.", `<a href="/admin/metrics">Instance metrics and health</a>`, `<h3 id="backup-heading">Automatic backups</h3>`, "Automatic backups are off."}
	last := -1
	for _, part := range order {
		at := strings.Index(body, part)
		if at <= last {
			t.Fatalf("%q is missing or out of order (at %d, after %d)", part, at, last)
		}
		last = at
	}
	for _, gone := range []string{"service worker", "What happens to the conversations already here?"} {
		if strings.Contains(body, gone) {
			t.Errorf("settings still say %q", gone)
		}
	}
	css, err := assets.ReadFile("static/style.css")
	if err != nil || !strings.Contains(string(css), ".server-section code { overflow-wrap: anywhere; }") || !strings.Contains(body, `class="comfy-admin-section paper stack server-section"`) {
		t.Fatal("long backup paths in the Server section must wrap on small screens")
	}
	heading := body[strings.Index(body, `<div class="page-heading">`):strings.Index(body, "<legend>Make this place yours</legend>")]
	if strings.Contains(heading, "/admin/metrics") || strings.Contains(heading, "Running Witmoot") {
		t.Fatal("server details are back in the page heading")
	}
}

// Headings are labels, not sentences, so none ends with a period.
func TestHeadingsHaveNoTrailingPeriods(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	heading := regexp.MustCompile(`(?s)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	action := regexp.MustCompile(`{{[^}]*}}`)
	tag := regexp.MustCompile(`<[^>]*>`)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range heading.FindAllStringSubmatch(string(content), -1) {
			// Each branch of a conditional heading is checked on its own.
			for _, part := range action.Split(match[2], -1) {
				text := strings.TrimSpace(tag.ReplaceAllString(part, ""))
				if strings.HasSuffix(text, ".") {
					t.Errorf("%s: heading %q ends with a period", file, match[2])
				}
			}
		}
	}
}

// The offline archive inlines the sage palette, so an export still looks like
// Witmoot without a server.
func TestExportUsesTheFamilyPalette(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	entries, _, _ := readExport(t, client)
	page := string(entries["archive.html"])
	palettes, err := fs.ReadFile(ui.Assets(), "palettes.css")
	if err != nil {
		t.Fatal(err)
	}
	sage := string(palettes)
	sage = sage[strings.Index(sage, `:root[data-palette="sage"] {`):]
	sage = sage[:strings.Index(sage, "}")]
	token := regexp.MustCompile(`(--comfy-[a-z-]+): ([^;]+);`)
	shared := map[string]string{}
	for _, match := range token.FindAllStringSubmatch(sage, -1) {
		shared[match[1]] = match[2]
	}
	found := token.FindAllStringSubmatch(page, -1)
	if len(found) < 7 {
		t.Fatalf("archive defines only %d palette tokens", len(found))
	}
	for _, match := range found {
		if shared[match[1]] != match[2] {
			t.Errorf("%s is %q in the archive but %q in the sage palette", match[1], match[2], shared[match[1]])
		}
	}
	for _, want := range []string{"font-family: var(--comfy-heading-font)", "color: var(--comfy-text)", "background: var(--comfy-surface)"} {
		if !strings.Contains(page, want) {
			t.Errorf("archive lacks %q", want)
		}
	}
	if strings.Contains(page, "system-ui") || strings.Contains(page, "#14161a") {
		t.Error("archive still uses the old blue-grey look")
	}
}

// A one-time link is shown once, in a read-only field marked for the shared
// Copy link button.
func TestOneTimeLinksHaveOneFieldAndACopyButton(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	memberClient(t, app, "jules")
	jules, _, err := app.store.Credentials(context.Background(), "jules")
	if err != nil {
		t.Fatal(err)
	}
	invite := owner.post("/invites", url.Values{"label": {"Sam"}, "max_uses": {"1"}, "expires_days": {"7"}})
	requireStatus(t, invite, 200)
	reset := owner.post(fmt.Sprintf("/members/%d/reset", jules.ID), nil)
	if reset.Code == 303 {
		reset = owner.request("GET", reset.Header().Get("Location"), nil, nil)
	}
	link := regexp.MustCompile(`<input id="(invite-link|reset-link)" type="text" readonly data-comfy-copy value="([^"]+)">`)
	for name, body := range map[string]string{"invitation": invite.Body.String(), "reset": reset.Body.String()} {
		match := link.FindStringSubmatch(body)
		if match == nil {
			t.Fatalf("%s: no read-only link field marked for copying\n%s", name, body)
		}
		if n := strings.Count(body, match[2]); n != 1 {
			t.Errorf("%s: the link appears %d times, want once", name, n)
		}
		for _, gone := range []string{"data-invitation", "data-copy=", "/static/copy.js"} {
			if strings.Contains(body, gone) {
				t.Errorf("%s: %s is back", name, gone)
			}
		}
		if !strings.Contains(body, `<script src="/static/comfyware/suite.js"></script>`) {
			t.Errorf("%s: the shared script that adds Copy buttons is not loaded", name)
		}
	}
	if w := owner.request("GET", "/static/copy.js", nil, nil); w.Code != 404 {
		t.Fatalf("the app-local copy script is still served: %d", w.Code)
	}
}

// Witmoot's pages read the Comfyware tokens, which the shared palettes set,
// so an owner's palette reaches every page. The app sets only its fonts and
// corners, and draws no colors of its own.
func TestStylesFollowTheSharedPalette(t *testing.T) {
	css, err := assets.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	style := string(css)
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(style, -1); len(hex) > 0 {
		t.Errorf("style.css draws its own colors: %v", hex)
	}
	if strings.Contains(style, "light-dark(") {
		t.Error("style.css still carries the sage palette colors itself")
	}
	for _, old := range []string{"--ink:", "--paper:", "--green:", "--line:", "--background:", "var(--ink)", "var(--green)"} {
		if strings.Contains(style, old) {
			t.Errorf("style.css still uses the old variable %s", old)
		}
	}
	for _, want := range []string{"--comfy-font:", "--comfy-radius:", "var(--comfy-heading-font)", "var(--comfy-heading-weight)", "var(--comfy-accent)", "var(--comfy-surface)"} {
		if !strings.Contains(style, want) {
			t.Errorf("style.css lacks %s", want)
		}
	}
	layout, err := assets.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatal(err)
	}
	order := []string{`<script src="/static/comfyware/theme.js"></script>`, `href="/static/style.css"`, `href="/static/comfyware/components.css"`, `href="/static/comfyware/palettes.css"`}
	last := -1
	for _, part := range order {
		at := strings.Index(string(layout), part)
		if at <= last {
			t.Fatalf("%s is missing or out of order in the head", part)
		}
		last = at
	}
	if strings.Contains(string(layout), `theme.js" defer`) {
		t.Fatal("theme.js must run before the page is drawn")
	}
	app, visitor := newTestApp(t, false)
	body := visitor.request("GET", "/login", nil, nil).Body.String()
	if !strings.Contains(body, `<html lang="en" data-comfy-app="witmoot" data-theme-key="witmoot-theme" data-palettes="sage slate" data-palette="sage" data-palette-default="sage">`) {
		t.Fatalf("pages do not render the sage palette by default:\n%.400s", body)
	}
	_ = app
}

// Owners pick the site palette; anything outside the shared list is refused
// and changes nothing.
func TestOwnerSitePaletteSetting(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	form := func(palette string) url.Values {
		return url.Values{"mode": {"private"}, "site_name": {"Our board"}, "palette": {palette}}
	}
	requireStatus(t, owner.post("/settings", form("slate")), 303)
	body := owner.request("GET", "/", nil, nil).Body.String()
	if !strings.Contains(body, `data-palette="slate" data-palette-default="slate"`) {
		t.Fatal("site palette not applied")
	}
	settings := owner.request("GET", "/settings", nil, nil).Body.String()
	if !strings.Contains(settings, `<option value="slate" selected>Slate</option>`) {
		t.Fatal("settings do not show the saved palette")
	}
	for _, bad := range []string{"purple", "SAGE", " slate", "slate\n", `"><script>`, "../sage", strings.Repeat("s", 500)} {
		w := owner.post("/settings", form(bad))
		if w.Code != 422 || !strings.Contains(w.Body.String(), "Choose one of the listed color palettes.") {
			t.Errorf("palette %q: %d", bad, w.Code)
		}
		if strings.Contains(w.Body.String(), "<script>") {
			t.Fatal("palette draft not escaped")
		}
	}
	if !strings.Contains(owner.request("GET", "/", nil, nil).Body.String(), `data-palette="slate"`) {
		t.Fatal("a refused palette changed the site")
	}
	requireStatus(t, owner.post("/settings", form("")), 303)
	if !strings.Contains(owner.request("GET", "/", nil, nil).Body.String(), `data-palette="sage"`) {
		t.Fatal("blank palette should restore the sage default")
	}
	member := memberClient(t, app, "jules")
	requireStatus(t, member.post("/settings", form("slate")), 403)
}
