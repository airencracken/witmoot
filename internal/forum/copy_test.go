// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

// The offline archive uses the same palette and headings as the site, so an
// export still looks like Witmoot without a server.
func TestExportUsesTheFamilyPalette(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	entries, _, _ := readExport(t, client)
	page := string(entries["archive.html"])
	css, err := assets.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	token := regexp.MustCompile(`--(ink|muted|green|line|paper|background|soft): ([^;]+);`)
	site := map[string]string{}
	for _, match := range token.FindAllStringSubmatch(string(css), -1) {
		if _, seen := site[match[1]]; !seen {
			site[match[1]] = match[2]
		}
	}
	found := token.FindAllStringSubmatch(page, -1)
	if len(found) != 7 {
		t.Fatalf("archive defines %d palette tokens, want 7", len(found))
	}
	for _, match := range found {
		if site[match[1]] != match[2] {
			t.Errorf("--%s is %q in the archive but %q on the site", match[1], match[2], site[match[1]])
		}
	}
	for _, want := range []string{`font-family: Georgia, "Times New Roman", serif`, "color: var(--ink)", "background: var(--background)"} {
		if !strings.Contains(page, want) {
			t.Errorf("archive lacks %q", want)
		}
	}
	for _, gone := range []string{"system-ui", "#14161a", "#b9c", "#dde"} {
		if strings.Contains(page, gone) {
			t.Errorf("archive still uses %q", gone)
		}
	}
}

// A one-time link is shown once, in a read-only field, with a Copy button
// that the script reveals only where the clipboard works.
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
	link := regexp.MustCompile(`<input id="(invite-link|reset-link)" type="text" readonly value="([^"]+)">`)
	for name, c := range map[string]struct {
		body string
		ids  []string
	}{
		"invitation": {invite.Body.String(), []string{"invite-link", "invite-code"}},
		"reset":      {reset.Body.String(), []string{"reset-link"}},
	} {
		match := link.FindStringSubmatch(c.body)
		if match == nil {
			t.Fatalf("%s: no read-only link field\n%s", name, c.body)
		}
		if n := strings.Count(c.body, match[2]); n != 1 {
			t.Errorf("%s: the link appears %d times, want once", name, n)
		}
		for _, id := range c.ids {
			if !strings.Contains(c.body, fmt.Sprintf(`data-copy="%s" hidden>`, id)) || !strings.Contains(c.body, fmt.Sprintf(`id="%s" type="text" readonly`, id)) {
				t.Errorf("%s: %s lacks a hidden copy button for its read-only field", name, id)
			}
		}
		if strings.Contains(c.body, "data-invitation") {
			t.Errorf("%s: unused data-invitation attribute is back", name)
		}
		if !strings.Contains(c.body, `<script src="/static/copy.js" defer></script>`) {
			t.Errorf("%s: copy script is not loaded", name)
		}
	}
	w := owner.request("GET", "/static/copy.js", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "navigator.clipboard") {
		t.Fatalf("copy script not served: %d", w.Code)
	}
}
