// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestMemberProfileRoutesOwnershipPrivacyAndExport(t *testing.T) {
	a, owner := newTestApp(t, false)
	ownerID := signInTest(t, a, owner, true)
	member, memberID := signedInCommunityMember(t, a)
	path := fmt.Sprintf("/members/%d", memberID)
	if w := owner.request("GET", path, nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "hasn’t added a bio") {
		t.Fatal("optional blank profile", w.Code, w.Body.String())
	}
	form := url.Values{"profile_name": {"<script>name</script>"}, "profile_bio": {"A short bio.\n<script>bio</script>"}, "profile_link_label": {"My site"}, "profile_link_url": {"https://example.org/?x=1&y=2"}, "user_id": {fmt.Sprint(ownerID)}}
	w := member.post("/account/profile", form)
	if w.Code != 303 || w.Header().Get("Location") != "/account/profile?saved=profile" {
		t.Fatal("save contract", w.Code, w.Body.String())
	}
	before, err := a.store.MemberProfile(t.Context(), memberID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.store.MemberProfile(t.Context(), ownerID)
	if err != nil || other.Biography.Name != "" {
		t.Fatal("edit targeted another member", other, err)
	}
	w = owner.request("GET", path, nil, nil)
	body := w.Body.String()
	for _, text := range []string{"&lt;script&gt;name&lt;/script&gt;", "&lt;script&gt;bio&lt;/script&gt;", `href="https://example.org/?x=1&amp;y=2"`, `target="_blank"`, `rel="noopener noreferrer ugc nofollow"`, `hx-boost="false"`, "signed-in members"} {
		if !strings.Contains(body, text) {
			t.Fatal("profile missing safe content", text, body)
		}
	}
	if strings.Contains(body, "<script>bio") || strings.Contains(body, "Edit your profile") || strings.Contains(body, "jules@example") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("profile privacy or escaping", body)
	}
	guest := &testClient{app: a, cookies: make(map[string]*http.Cookie)}
	for _, method := range []string{"GET", "HEAD"} {
		w := guest.request(method, path, nil, nil)
		if w.Code != 303 || strings.Contains(w.Body.String(), "short bio") {
			t.Fatal("guest profile disclosure", method, w.Code)
		}
	}
	for _, id := range []string{"0", "-1", "abc", "9223372036854775808", "99999"} {
		if w := owner.request("GET", "/members/"+id, nil, nil); w.Code != 404 {
			t.Fatal("unavailable profile", id, w.Code)
		}
	}
	if w := owner.request("HEAD", path, nil, nil); w.Code != 200 {
		t.Fatal("HEAD profile route", w.Code)
	}
	if w := owner.post(path, nil); w.Code != 405 {
		t.Fatal("profile method contract", w.Code)
	}
	for _, bad := range []url.Values{{"profile_name": {"one", "two"}}, {"profile_bio": {"one", "two"}}, {"profile_link_label": {"Site"}, "profile_link_url": {"javascript:bad"}}, {"profile_bio": {strings.Repeat("x", 1001)}}} {
		w = member.post("/account/profile", bad)
		if w.Code != 422 {
			t.Fatal("invalid profile accepted", bad, w.Code)
		}
		after, err := a.store.MemberProfile(t.Context(), memberID)
		if err != nil || !reflect.DeepEqual(after, before) {
			t.Fatal("invalid edit changed stored profile", after, err)
		}
	}
	if w := member.request("POST", "/account/profile", url.Values{"profile_name": {"Hacked"}}, nil); w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
	w = member.request("GET", "/account/profile?saved=profile", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Your profile has been saved.") || strings.Count(w.Body.String(), `name="profile_link_url"`) != 5 {
		t.Fatal("profile settings", w.Code, w.Body.String())
	}
	entries, _, _ := readExport(t, member)
	var archive exportManifest
	if err = json.Unmarshal(entries["manifest.json"], &archive); err != nil || archive.Account.Profile == nil || !reflect.DeepEqual(*archive.Account.Profile, before.Biography) {
		t.Fatal("own profile export", archive, err)
	}
	_, ownerExport, _ := readExport(t, owner)
	if strings.Contains(string(ownerExport), "short bio") {
		t.Fatal("another member's export contains profile")
	}
	if w := member.request("GET", "/members", nil, nil); w.Code != 403 {
		t.Fatal("profile route broadened owner directory access", w.Code)
	}
}
