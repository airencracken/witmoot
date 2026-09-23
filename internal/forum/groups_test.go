package forum

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestGroupRoutesAreOwnerOnlyAndCSRFProtected(t *testing.T) {
	f := privateBoardFixture(t)
	g := createTestGroup(t, f, "Private circle", f.writer.ID)
	base := fmt.Sprintf("/groups/%d", g.ID)
	form := url.Values{"name": {"Changed name"}, "revision": {"0"}, "confirmation": {g.Name}}
	for _, user := range []*User{f.writer, f.reader, f.outsider, nil} {
		c := sessionClient(t, f.app, user)
		want := 403
		if user == nil {
			want = 303
		}
		for _, path := range []string{"/groups", "/groups/new", base, base + "/delete"} {
			w := c.request("GET", path, nil, nil)
			requireStatus(t, w, want)
			if strings.Contains(w.Body.String(), g.Name) {
				t.Fatalf("group details leaked at %s", path)
			}
		}
		for _, path := range []string{"/groups/new", base, base + "/delete"} {
			requireStatus(t, c.post(path, maps.Clone(form)), want)
		}
	}
	owner := sessionClient(t, f.app, f.owner)
	for _, path := range []string{"/groups/new", base, base + "/delete"} {
		requireStatus(t, owner.request("POST", path, maps.Clone(form), nil), 403)
	}
	for _, path := range []string{"/groups", "/groups/new", base, base + "/delete"} {
		requireStatus(t, owner.request("GET", path, nil, nil), 200)
	}
	actual, err := f.app.store.Group(context.Background(), g.ID)
	if err != nil || actual != g {
		t.Fatalf("GET or rejected request changed group: %+v %v", actual, err)
	}
	for _, path := range []string{"/groups/0", "/groups/not-an-id", "/groups/99999", "/groups/99999/delete"} {
		requireStatus(t, owner.request("GET", path, nil, nil), 404)
		requireStatus(t, owner.post(path, maps.Clone(form)), 404)
	}
}

func TestGroupFormsCreateRenameValidateAndConfirmDeletion(t *testing.T) {
	f := privateBoardFixture(t)
	owner := sessionClient(t, f.app, f.owner)
	member := strconv.FormatInt(f.writer.ID, 10)
	form := url.Values{"name": {"Tea & biscuits"}, "description": {"Plain text <script>bad()</script>"}, "revision": {"0"}, "members": {member}}
	w := owner.post("/groups/new", maps.Clone(form))
	requireStatus(t, w, 303)
	path := strings.TrimSuffix(w.Header().Get("Location"), "?saved=1")
	w = owner.request("GET", path, nil, nil)
	requireStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Tea &amp; biscuits") || strings.Contains(w.Body.String(), "<script>bad()") {
		t.Fatal("group fields were not escaped")
	}
	for _, value := range []string{"", "-1", "too-large", "9223372036854775808"} {
		bad := maps.Clone(form)
		bad.Set("revision", value)
		requireStatus(t, owner.post(path, bad), 400)
	}
	for _, value := range []string{"-1", "0", "not-a-user"} {
		bad := maps.Clone(form)
		bad.Set("members", value)
		requireStatus(t, owner.post(path, bad), 400)
	}
	bad := maps.Clone(form)
	bad["members"] = []string{member, member}
	requireStatus(t, owner.post(path, bad), 422)
	bad.Set("members", "99999")
	requireStatus(t, owner.post(path, bad), 422)
	form.Set("name", "The soup committee")
	form["members"] = []string{member, strconv.FormatInt(f.reader.ID, 10)}
	requireStatus(t, owner.post(path, maps.Clone(form)), 303)
	requireStatus(t, owner.post(path, maps.Clone(form)), 409)
	requireStatus(t, owner.post(path+"/delete", url.Values{"revision": {"0"}, "confirmation": {form.Get("name")}}), 409)
	requireStatus(t, owner.post(path+"/delete", url.Values{"revision": {"1"}, "confirmation": {"the soup committee"}}), 422)
	requireStatus(t, owner.request("GET", path+"/delete", nil, nil), 200)
	w = owner.post(path+"/delete", url.Values{"revision": {"1"}, "confirmation": {form.Get("name")}})
	requireStatus(t, w, 303)
	if w.Header().Get("Location") != "/groups?deleted=1" {
		t.Fatal("missing group deletion redirect")
	}
	requireStatus(t, owner.request("GET", path, nil, nil), 404)
}

func TestBoardGroupFormRejectsInvalidPermissionsAtomically(t *testing.T) {
	f := privateBoardFixture(t)
	g := createTestGroup(t, f, "Friends", f.writer.ID)
	owner := sessionClient(t, f.app, f.owner)
	path := fmt.Sprintf("/boards/%d/settings", f.boardID)
	form := url.Values{"revision": {"0"}, "name": {"Hidden planning room"}, "category": {"Private corners"}, "visibility": {"selected"}, fmt.Sprintf("access_%d", f.writer.ID): {"inherit"}, fmt.Sprintf("group_%d", g.ID): {"write"}}
	for _, key := range []string{"group_bad", "group_0", "group_-1"} {
		bad := maps.Clone(form)
		bad.Set(key, "read")
		requireStatus(t, owner.post(path, bad), 422)
	}
	for _, level := range []string{"inherit", "owner", ""} {
		bad := maps.Clone(form)
		bad.Set(fmt.Sprintf("group_%d", g.ID), level)
		requireStatus(t, owner.post(path, bad), 422)
	}
	bad := maps.Clone(form)
	bad[fmt.Sprintf("group_%d", g.ID)] = []string{"read", "write"}
	requireStatus(t, owner.post(path, bad), 422)
	requireStatus(t, owner.post(path, maps.Clone(form)), 303)
	c := sessionClient(t, f.app, f.writer)
	requireStatus(t, c.post(fmt.Sprintf("/topics/%d/replies", f.topicID), url.Values{"body": {"Access through the group"}}), 303)
	form.Set("revision", "1")
	form.Set(fmt.Sprintf("access_%d", f.writer.ID), "none")
	requireStatus(t, owner.post(path, maps.Clone(form)), 303)
	requireStatus(t, c.request("GET", fmt.Sprintf("/topics/%d", f.topicID), nil, nil), 404)
}
