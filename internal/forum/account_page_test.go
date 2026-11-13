// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"testing"

	"witmoot/internal/imvault"
)

// The account page groups its forms into Profile, Sign-in, and Your data, in
// that order, and the avatar form carries the animation preference.
func TestAccountPageSectionsAndShortHelp(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	body := client.request("GET", "/account", nil, nil).Body.String()
	last := -1
	for _, part := range []string{"<h1>Your account</h1>", `<h2 id="account-profile">Profile</h2>`, `<h3 id="profile-avatar">Avatar</h3>`, `<h2>Times and dates</h2>`, `<h2 id="account-signin">Sign-in</h2>`, "<h3>Change your password</h3>", "<h3>Email address</h3>", `<h2 id="account-data">Your data</h2>`, `href="/account/export"`, `href="/account/delete"`} {
		at := strings.Index(body, part)
		if at <= last {
			t.Fatalf("%q is missing or out of order", part)
		}
		last = at
	}
	avatar := body[strings.Index(body, `action="/account/avatar" method="post" enctype`):]
	avatar = avatar[:strings.Index(avatar, "</form>")]
	for _, field := range []string{`name="avatar"`, `name="preference" value="1"`, `name="animate" value="1"`, "Show animated avatars"} {
		if !strings.Contains(avatar, field) {
			t.Errorf("avatar form lacks %s", field)
		}
	}
	if strings.Contains(body, "/account/avatar-preference") || strings.Contains(body, "Save animation preference") {
		t.Error("the separate animation form is still on the page")
	}
	for _, limit := range []string{"MiB", "megapixel", "512 by 512", "64 frames", "72 bytes"} {
		if strings.Contains(body, limit) {
			t.Errorf("help text still states the limit %q", limit)
		}
	}
}

// Saving the avatar form without a picture stores only the preference, and a
// refused picture changes neither the avatar nor the preference.
func TestAvatarFormSavesPreferenceAtomically(t *testing.T) {
	app, client := newTestApp(t, false)
	id := signInTest(t, app, client, false)
	ctx := t.Context()
	w := postAvatarMultipart(t, client, url.Values{"preference": {"1"}}, nil)
	requireStatus(t, w, 303)
	if w.Header().Get("Location") != "/account?saved=avatar-settings#profile-avatar" {
		t.Fatalf("redirect: %s", w.Header().Get("Location"))
	}
	if on, _ := app.store.AnimateAvatars(ctx, id); on {
		t.Fatal("unchecked box should turn animation off")
	}
	if !strings.Contains(client.request("GET", "/account?saved=avatar-settings", nil, nil).Body.String(), "Your avatar settings have been saved.") {
		t.Fatal("missing saved notice")
	}
	requireStatus(t, postAvatarMultipart(t, client, url.Values{"preference": {"1"}, "animate": {"1"}}, animatedAvatarFixture(t, 3, 32)), 303)
	if on, _ := app.store.AnimateAvatars(ctx, id); !on {
		t.Fatal("checked box should turn animation on")
	}
	before, _ := app.store.AvatarAnimation(ctx, id)
	for name, fields := range map[string]url.Values{
		"bad picture":         {"preference": {"1"}},
		"two animate values":  {"preference": {"1"}, "animate": {"1", "0"}},
		"strange animate":     {"preference": {"1"}, "animate": {"yes"}},
		"script in animate":   {"preference": {"1"}, "animate": {"<script>"}},
		"no preference field": {},
	} {
		file := []byte("not a picture")
		if name != "bad picture" {
			file = nil
		}
		requireStatus(t, postAvatarMultipart(t, client, fields, file), 422)
		if on, _ := app.store.AnimateAvatars(ctx, id); !on {
			t.Fatalf("%s changed the preference", name)
		}
		if after, _ := app.store.AvatarAnimation(ctx, id); !bytes.Equal(before, after) {
			t.Fatalf("%s changed the avatar", name)
		}
	}
}

// Limits appear in the error that a picture breaking them gets.
func TestAvatarErrorsStateTheirLimits(t *testing.T) {
	app, client := newTestApp(t, false)
	signInTest(t, app, client, false)
	for name, c := range map[string]struct {
		file []byte
		want string
	}{
		"too large":     {append(pngAvatarFixture(t, 8, 8), bytes.Repeat([]byte{0}, 2<<20)...), string(errBrandImageTooLarge)},
		"too many dots": {grayPNG(t, 4097, 16), "Choose a picture of at most 4 megapixels, such as 2048 by 2048 pixels."},
		"not a picture": {[]byte("plain text, not a picture"), "Choose a PNG, JPEG, or GIF picture."},
		"broken gif":    {[]byte("GIF89a but not really"), "Choose a GIF that opens properly"},
		"long gif":      {animatedAvatarFixture(t, 65, 8), "Choose a GIF that opens properly, up to 512 by 512 pixels and 64 frames."},
		"wide gif":      {animatedAvatarFixture(t, 2, 600), "Choose a GIF that opens properly, up to 512 by 512 pixels and 64 frames."},
	} {
		w := postAvatarMultipart(t, client, nil, c.file)
		if w.Code != 422 || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d, want error %q\n%s", name, w.Code, c.want, w.Body.String())
		}
	}
}

// Limits are binary values, and the copy that states them uses IEC units
// that match those values exactly.
func TestUploadLimitsAndTheirCopyAgree(t *testing.T) {
	for _, c := range []struct {
		limit   int64
		want    int64
		message string
		text    string
	}{
		{maxBrandImageBytes, 2 << 20, string(errBrandImageTooLarge), "2 MiB"},
		{maxAvatarInputBytes, 2 << 20, string(errAvatarTooLarge), "2 MiB"},
		{imvault.MaxImageBytes, 8 << 20, string(uploadProblem(errors.New("refused")).(userError)), "8 MiB"},
	} {
		if c.limit != c.want {
			t.Errorf("limit is %d bytes, want the binary value %d", c.limit, c.want)
		}
		if !strings.Contains(c.message, c.text) || strings.Contains(c.message, " MB") {
			t.Errorf("%q should state the limit as %s", c.message, c.text)
		}
	}
}
