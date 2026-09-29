package forum

import (
	"net/http"
	"regexp"
	"testing"
)

func TestSignInControlsDescribeTheirAction(t *testing.T) {
	_, client := newTestApp(t, false)
	landing := client.request(http.MethodGet, "/", nil, nil)
	requireStatus(t, landing, http.StatusOK)
	link := regexp.MustCompile(`<a[^>]+href="/login"[^>]*>Sign in(?:\s|<)`)
	if !link.MatchString(landing.Body.String()) {
		t.Fatal("landing page has no clearly labelled sign-in link")
	}
	login := client.request(http.MethodGet, "/login", nil, nil)
	requireStatus(t, login, http.StatusOK)
	button := regexp.MustCompile(`<button[^>]+type="submit"[^>]*>Sign in</button>`)
	if !button.MatchString(login.Body.String()) {
		t.Fatal("login form has no clearly labelled sign-in submit control")
	}
}
