// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"fmt"
	"net/url"
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
