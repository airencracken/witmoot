package forum

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCommunityExportIncludesPrivateAndEmptyBoardsButNoCredentials(t *testing.T) {
	app, owner := newTestApp(t, false)
	ownerID := signInTest(t, app, owner, true)
	member := memberClient(t, app, "relative")
	user, _, _ := app.store.Credentials(t.Context(), "relative")
	selectMode(t, owner, ModePersonal)
	if _, err := app.store.CreateTopic(t.Context(), 1, ownerID, "Owner's notebook", "Owner only words", AudienceOwners); err != nil {
		t.Fatal(err)
	}
	selectMode(t, owner, ModePrivate)
	if _, err := app.store.CreateTopic(t.Context(), 1, user.ID, "Our conversation", "Member's words", AudienceMembers); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, member.request("GET", "/community/export", nil, nil), 403)
	guest := &testClient{app: app, cookies: make(map[string]*http.Cookie)}
	requireStatus(t, guest.request("GET", "/community/export", nil, nil), 303)
	settings := owner.request("GET", "/settings", nil, nil)
	if !strings.Contains(settings.Body.String(), `href="/community/export" hx-boost="false"`) {
		t.Fatal("community archive link is intercepted by HTMX")
	}
	account := member.request("GET", "/account", nil, nil)
	if !strings.Contains(account.Body.String(), `href="/account/export" hx-boost="false"`) {
		t.Fatal("member archive link is intercepted by HTMX")
	}
	w := owner.request("GET", "/community/export", nil, nil)
	requireStatus(t, w, 200)
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var manifest exportManifest
	var page string
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		closeTest(t, reader)
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "manifest.json" {
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
		}
		if file.Name == "archive.html" {
			page = string(data)
		}
	}
	if manifest.Format != "witmoot-community-export" || len(manifest.Posts) != 2 || len(manifest.Boards) != 5 || len(manifest.Members) != 2 {
		t.Fatalf("incomplete community archive: %+v", manifest)
	}
	for _, want := range []string{"Owner only words", "Member&#39;s words", "relative", "Community archive"} {
		if !strings.Contains(page, want) {
			t.Errorf("readable archive missing %q", want)
		}
	}
	for _, secret := range []string{"password_hash", "token_hash", "imvault.key", "a long test password"} {
		if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
			t.Fatalf("secret exported: %s", secret)
		}
	}
}

func TestMemberExportIncludesOfflinePreviewsAndLinksWithHonestFallback(t *testing.T) {
	app, client, userID, upstream := imageTestApp(t)
	_, err := app.store.CreateTopic(context.Background(), 1, userID, "Family pictures", "Our photographs", AudienceMembers, Attachment{Server: app.vault.Base, RemoteID: "own", CredentialUserID: sql.NullInt64{Int64: userID, Valid: true}, Name: "../<script>.png", Rendition: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _ := readExport(t, client)
	manifest := decodeManifest(t, entries)
	attachment := manifest.Posts[0].Attachments[0]
	if attachment.URL != "https://vault.example/f/own" || attachment.Path == "" || !bytes.Equal(entries[attachment.Path], testPNG) || attachment.Note != "" {
		t.Fatalf("preview not portable: %+v", attachment)
	}
	if !strings.Contains(string(entries["archive.html"]), `src="`+attachment.Path+`"`) || strings.Contains(string(entries["archive.html"]), "<script>.png") {
		t.Fatal("offline preview absent or unescaped")
	}
	upstream.unavailable = true
	entries, _, _ = readExport(t, client)
	attachment = decodeManifest(t, entries).Posts[0].Attachments[0]
	if attachment.Path != "" || attachment.Note == "" || attachment.URL == "" {
		t.Fatal("failed upstream preview silently disappeared")
	}
	// Permission changes still apply while exporting one's own words.
	requireStatus(t, client.post("/account/imvault/disconnect", url.Values{}), 303)
}

func TestCommunityExportPreservesPrivateBoardAndGroupBoundaries(t *testing.T) {
	app, owner := newTestApp(t, false)
	ownerID := signInTest(t, app, owner, true)
	memberClient(t, app, "relative")
	member, _, _ := app.store.Credentials(t.Context(), "relative")
	for _, query := range []string{
		"UPDATE boards SET restricted=1,archived=1 WHERE id=2",
		"INSERT INTO user_groups(id,name,description) VALUES(1,'Family','Our people')",
		"INSERT INTO group_members(group_id,user_id) VALUES(1,?)",
		"INSERT INTO board_groups(board_id,group_id,access) VALUES(2,1,'read')",
		"INSERT INTO board_members(board_id,user_id,access) VALUES(2,?,'none')",
	} {
		args := []any{}
		if strings.Contains(query, "?") {
			args = append(args, member.ID)
		}
		if _, err := app.store.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	contributions, err := app.store.CommunityContributions(t.Context(), &User{ID: ownerID, Role: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	policy := contributions.Policy
	if policy == nil || policy.Mode != "private" || len(policy.Boards) != 5 || !policy.Boards[1].Restricted || !policy.Boards[1].Archived || len(policy.Groups) != 1 || len(policy.GroupMembers) != 1 || len(policy.GroupGrants) != 1 || len(policy.MemberGrants) != 1 || policy.MemberGrants[0].Access != "none" {
		t.Fatalf("archive widened or lost audience boundaries: %+v", policy)
	}
	if _, err := app.store.CommunityContributions(t.Context(), &member); err == nil {
		t.Fatal("member obtained private community policy")
	}
}
