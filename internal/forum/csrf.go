package forum

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	"github.com/airencracken/comfylib/token"
)

// csrfPeekLimit bounds how much of a multipart body is read to find the token.
const csrfPeekLimit = 16 << 10

// expectedCSRF returns the token this request must carry, reissuing the cookie
// that holds it when the browser does not already have the right one.
//
// A signed-in request's token is derived from its session, so it cannot be
// planted: a cookie set by a neighbouring host, or left over from before sign
// in, is not the token this session expects. A signed-out request has no
// session to bind to and keeps a double-submit cookie, which still stops a
// stranger from submitting the sign-in or join form on somebody's behalf.
func (a *App) expectedCSRF(w http.ResponseWriter, r *http.Request, session string) string {
	current := ""
	if c, err := r.Cookie(a.cookieName("csrf")); err == nil && validToken(c.Value) {
		current = c.Value
	}
	expected := current
	if session != "" {
		expected = sessionCSRFToken(session)
	} else if expected == "" {
		expected, _ = token.New()
	}
	if expected != current {
		a.cookie(w, "csrf", expected, 86400)
	}
	return expected
}

// sessionCSRFToken derives a session's CSRF token from its secret. The session
// token never leaves its HttpOnly cookie, so nobody without it can compute
// this, and the derivation is one-way, so a page showing it reveals nothing.
// It is keyed differently from the stored session digest.
func sessionCSRFToken(session string) string {
	mac := hmac.New(sha256.New, []byte(session))
	mac.Write([]byte("witmoot-csrf-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

// isMutating reports whether a request method may change state. Every such
// method is checked, not only the ones the forms use today.
func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

// providedCSRF finds the token a state-changing request carries. A request
// with no form body, such as DELETE, may send it in the X-CSRF-Token header.
// Forms carry it in their csrf field, with or without JavaScript. An ordinary
// form is small and bounded, so it is parsed. A multipart body may be an
// upload of tens of megabytes, so only its first part is read, which is where
// the templates put the token, and the body is restored for the handler. A
// request without the token never has its upload read.
func providedCSRF(r *http.Request, upload bool) (string, error) {
	if provided := r.Header.Get("X-CSRF-Token"); provided != "" {
		return provided, nil
	}
	if upload {
		return firstPartToken(r)
	}
	// ParseForm reports body-limit errors that FormValue would hide.
	if err := r.ParseForm(); err != nil {
		return "", err
	}
	return r.PostForm.Get("csrf"), nil
}

// firstPartToken reads the csrf field from the first part of a multipart body
// and puts back everything it consumed.
func firstPartToken(r *http.Request) (string, error) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		return "", nil
	}
	original := r.Body
	var consumed bytes.Buffer
	reader := multipart.NewReader(io.TeeReader(io.LimitReader(original, csrfPeekLimit), &consumed), params["boundary"])
	defer func() {
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(consumed.Bytes()), original), original}
	}()
	part, err := reader.NextPart()
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return "", err
		}
		return "", nil
	}
	if part.FormName() != "csrf" {
		return "", nil
	}
	value, err := io.ReadAll(io.LimitReader(part, 128))
	if err != nil {
		return "", nil
	}
	return string(value), nil
}
