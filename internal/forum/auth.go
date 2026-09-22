package forum

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	if state(r).User != nil {
		a.redirect(w, r, "/")
		return
	}
	a.render(w, r, 200, Page{View: "login", Title: "Welcome back"})
}

func (a *App) joinForm(w http.ResponseWriter, r *http.Request) {
	if state(r).User != nil {
		a.redirect(w, r, "/")
		return
	}
	if state(r).Mode == ModePersonal {
		a.fail(w, r, 403, "This is a personal board. New accounts and invitations are disabled.")
		return
	}
	a.render(w, r, 200, Page{View: "join", Title: "Make yourself at home", Invitation: r.URL.Query().Get("invite")})
}

func (a *App) authAllowed(w http.ResponseWriter, r *http.Request) bool {
	if state(r).User != nil {
		a.redirect(w, r, "/")
		return false
	}
	if !a.limiter.allow(r) {
		w.Header().Set("Retry-After", "900")
		a.fail(w, r, 429, "Too many attempts. Take a little break and try again in 15 minutes.")
		return false
	}
	return true
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.authAllowed(w, r) {
		return
	}
	name, password := strings.TrimSpace(r.PostForm.Get("username")), r.PostForm.Get("password")
	user, hash, err := a.store.Credentials(r.Context(), name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.serverError(w, r, err)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		hash = a.dummyHash
	}
	check := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil || check != nil || len(password) > 72 {
		a.render(w, r, 422, Page{View: "login", Title: "Welcome back", Username: name, Error: "That username and password did not match. Give it another try."})
		return
	}
	if state(r).Mode == ModePersonal && user.Role != "owner" {
		a.render(w, r, 403, Page{View: "login", Title: "Welcome back", Username: name, Error: errPersonal.Error()})
		return
	}
	a.signIn(w, r, user.ID)
}

func (a *App) join(w http.ResponseWriter, r *http.Request) {
	if !a.authAllowed(w, r) {
		return
	}
	if state(r).Mode == ModePersonal {
		a.fail(w, r, 403, "This is a personal board. New accounts and invitations are disabled.")
		return
	}
	name, password, invite := strings.TrimSpace(r.PostForm.Get("username")), r.PostForm.Get("password"), strings.TrimSpace(r.PostForm.Get("invite"))
	p := Page{View: "join", Title: "Make yourself at home", Username: name, Invitation: invite}
	p.Error = ValidateCredentials(name, password)
	if (invite != "" || state(r).Mode != ModeOpen) && !validToken(invite) {
		p.Error = "You will need an invitation from the owner to join."
	}
	if p.Error != "" {
		a.render(w, r, 422, p)
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	inviteHash := ""
	if invite != "" {
		inviteHash = tokenHash(invite)
	}
	id, err := a.store.Register(r.Context(), name, hash, inviteHash)
	if errors.Is(err, errPersonal) {
		a.fail(w, r, 403, err.Error())
		return
	}
	if errors.Is(err, errUsernameTaken) || errors.Is(err, errInvitation) {
		p.Error = err.Error()
		a.render(w, r, 422, p)
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.signIn(w, r, id)
}

func (a *App) signIn(w http.ResponseWriter, r *http.Request, userID int64) {
	token := randomToken()
	const lifetime = 7 * 24 * time.Hour
	if err := a.store.NewSession(r.Context(), tokenHash(token), userID, time.Now().Add(lifetime)); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.cookie(w, "session", token, int(lifetime.Seconds()))
	a.cookie(w, "csrf", randomToken(), 86400)
	a.redirect(w, r, "/")
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(a.cookieName("session")); err == nil {
		if err := a.store.DeleteSession(r.Context(), tokenHash(c.Value)); err != nil {
			a.serverError(w, r, err)
			return
		}
	}
	a.cookie(w, "session", "", -1)
	a.cookie(w, "csrf", randomToken(), 86400)
	a.redirect(w, r, "/")
}
