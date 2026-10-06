// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionDisplayDefaultsPermissionsPersistenceAndEscaping(t *testing.T) {
	const stamp = "0.13.0<script>alert(1)</script>"
	app, owner := newTestApp(t, false)
	app.config.Version = stamp
	guest := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	if strings.Contains(guest.request("GET", "/login", nil, nil).Body.String(), "0.13.0") {
		t.Fatal("version disclosed by default")
	}
	signInTest(t, app, owner, true)
	page := owner.request("GET", "/settings", nil, nil).Body.String()
	if !strings.Contains(page, "Running Witmoot 0.13.0&lt;script&gt;") || !strings.Contains(page, `name="show_version"`) {
		t.Fatal("running version or toggle missing/unsafe")
	}
	form := url.Values{"mode": {"private"}, "site_name": {"Family"}, "source_url": {""}, "show_version": {"1"}}
	requireStatus(t, guest.post("/settings", form), 303)
	member, _ := signedInCommunityMember(t, app)
	requireStatus(t, member.post("/settings", form), 403)
	requireStatus(t, owner.request("POST", "/settings", form, nil), 403)
	for _, enabled := range []bool{true, false, true} {
		if enabled {
			form.Set("show_version", "1")
		} else {
			form.Del("show_version")
		}
		requireStatus(t, owner.post("/settings", form), 303)
		brand, err := app.store.LoadBranding(context.Background(), SiteBranding{})
		if err != nil || brand.ShowVersion != enabled {
			t.Fatal("preference not persisted", brand, err)
		}
		restarted, err := New(app.store, Config{Version: stamp})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/login", "/about"} {
			client := &testClient{app: restarted, cookies: make(map[string]*http.Cookie)}
			page = client.request("GET", path, nil, nil).Body.String()
			if strings.Contains(page, "Witmoot 0.13.0&lt;script&gt;alert(1)&lt;/script&gt;") != enabled || strings.Contains(page, stamp) {
				t.Fatal("footer preference/escaping", path, enabled)
			}
		}
	}
	// Changing access mode through the older form must preserve the preference.
	requireStatus(t, owner.post("/settings", url.Values{"mode": {"open"}}), 303)
	if !strings.Contains(guest.request("GET", "/login", nil, nil).Body.String(), "Witmoot 0.13.0&lt;script&gt;") {
		t.Fatal("mode-only save reset preference")
	}
	// Failed validation keeps the submitted checkbox but the footer uses saved settings.
	form.Del("show_version")
	form.Set("source_url", "javascript:alert(1)")
	response := owner.post("/settings", form)
	requireStatus(t, response, 422)
	page = response.Body.String()
	if strings.Contains(page, `name="show_version" value="1" checked`) || !strings.Contains(page, "Witmoot 0.13.0&lt;script&gt;") {
		t.Fatal("draft changed public preference or lost unchecked checkbox")
	}
}
func TestVersionDisplayMigrationAndAtomicity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "witmoot.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range migrationFiles[:16] {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.Exec(string(body) + fmt.Sprintf("; PRAGMA user_version=%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Exec(`INSERT INTO instance_branding(id,name,source_url,house_rules) VALUES(1,'Family','https://example.org/source','Be kind'); INSERT INTO users(username,password_hash,role,created_at) VALUES('alex','hash','owner',123); UPDATE boards SET name='Kept board' WHERE id=1;`); err != nil {
		t.Fatal(err)
	}
	closeTest(t, old)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, s)
	brand, err := s.LoadBranding(t.Context(), SiteBranding{})
	if err != nil || brand.ShowVersion || brand.Name != "Family" || brand.HouseRules != "Be kind" {
		t.Fatal("migration changed branding", brand, err)
	}
	user, _, err := s.Credentials(t.Context(), "alex")
	if err != nil || user.CreatedAt != 123 || user.Timezone != "UTC" {
		t.Fatal("migration changed account", user, err)
	}
	var boards int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM boards WHERE id=1 AND name='Kept board'`).Scan(&boards); err != nil || boards != 1 {
		t.Fatal("migration lost content", err)
	}
	for _, show := range []bool{true, false, true} {
		brand.ShowVersion = show
		if err := s.SaveInstanceSettings(t.Context(), ModePrivate, brand); err != nil {
			t.Fatal(err)
		}
		if err := s.migrate(); err != nil {
			t.Fatal(err)
		}
		loaded, err := s.LoadBranding(t.Context(), SiteBranding{})
		if err != nil || loaded.ShowVersion != show {
			t.Fatal("re-migration lost preference", loaded, err)
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_version BEFORE UPDATE ON instance_branding BEGIN SELECT RAISE(ABORT,'write failed'); END;`); err != nil {
		t.Fatal(err)
	}
	brand.ShowVersion = false
	if err := s.SaveInstanceSettings(t.Context(), ModeOpen, brand); err == nil {
		t.Fatal("write failure ignored")
	}
	mode, err := s.Mode(t.Context())
	loaded, loadErr := s.LoadBranding(t.Context(), SiteBranding{})
	if err != nil || loadErr != nil || mode != ModePrivate || !loaded.ShowVersion {
		t.Fatal("partial settings write", mode, loaded, err, loadErr)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_version`); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, -1, 2, "<script>"} {
		if _, err := s.db.Exec(`UPDATE instance_branding SET show_version=? WHERE id=1`, value); err == nil {
			t.Fatal("invalid schema boolean", value)
		}
	}
}
