package forum

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/airencracken/comfylib/smtp"
	"github.com/airencracken/comfylib/token"
	"golang.org/x/crypto/bcrypt"
)

// ResetLinkTTL is how long an owner-issued reset link stays valid, from the
// web or the admin view.
const ResetLinkTTL = 24 * time.Hour

// resetURL is the link an owner hands to a member, or mails when a relay is
// configured.
func (a *App) resetURL(r *http.Request, token string) string {
	return a.origin(r) + "/reset/" + token
}

// siteName is the configured board name, preferring the saved branding.
func (a *App) siteName(r *http.Request) string {
	brand, err := a.store.LoadBranding(r.Context(), SiteBranding{Name: a.config.Name})
	if err == nil && brand.Name != "" {
		return brand.Name
	}
	return a.config.Name
}

func (a *App) accountPage(r *http.Request, message, notice, email string) Page {
	page := Page{View: "account", Title: "Your account", Error: message, Notice: notice, Email: email, MailEnabled: a.mailer.Enabled()}
	if user := state(r).User; user != nil {
		if has, err := a.store.HasAvatar(r.Context(), user.ID); err == nil {
			page.HasAvatar = has
		}
	}
	return page
}

func (a *App) account(w http.ResponseWriter, r *http.Request) {
	notice := ""
	switch r.URL.Query().Get("saved") {
	case "password":
		notice = "Your password has been changed. Other devices have been signed out."
	case "email":
		notice = "Your email address has been saved."
	case "avatar":
		notice = "Your avatar has been saved."
	}
	a.render(w, r, 200, a.accountPage(r, "", notice, state(r).User.Email))
}

// changePassword is the signed-in member's own change form. It asks for the
// current password and signs every other device out afterwards.
func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	user := *state(r).User
	_, hash, err := a.store.Credentials(r.Context(), user.Username)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.PostForm.Get("current_password"))); err != nil {
		a.render(w, r, 403, a.accountPage(r, "That is not your current password.", "", user.Email))
		return
	}
	password, confirm := r.PostForm.Get("password"), r.PostForm.Get("confirm_password")
	if message := ValidatePassword(password); message != "" {
		a.render(w, r, 422, a.accountPage(r, message, "", user.Email))
		return
	}
	if password != confirm {
		a.render(w, r, 422, a.accountPage(r, "The two passwords do not match.", "", user.Email))
		return
	}
	newHash, err := HashPassword(password)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.store.SetPassword(r.Context(), user.ID, newHash); err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.startSession(w, r, user.ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/account?saved=password")
}

func (a *App) saveEmail(w http.ResponseWriter, r *http.Request) {
	user := *state(r).User
	email := strings.TrimSpace(r.PostForm.Get("email"))
	if !validEmail(email) {
		a.render(w, r, 422, a.accountPage(r, "That does not look like an email address.", "", email))
		return
	}
	if err := a.store.SetEmail(r.Context(), user.ID, email); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/account?saved=email")
}

// resetForm shows the new-password form for a valid link, without spending it.
func (a *App) resetForm(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("token")
	page := Page{View: "reset", Title: "Choose a new password", ResetToken: code}
	if !validToken(code) {
		page.Error = errAuthToken.Error()
		a.render(w, r, 400, page)
		return
	}
	user, err := a.store.AuthTokenValid(r.Context(), token.Hash(code), TokenPasswordReset, time.Now())
	if err != nil {
		if errors.Is(err, errAuthToken) {
			page.Error = errAuthToken.Error()
			a.render(w, r, 400, page)
			return
		}
		a.serverError(w, r, err)
		return
	}
	page.ResetUser = user.Username
	a.render(w, r, 200, page)
}

// resetPassword redeems a link. The password is validated before the token is
// spent, so a rejected form can be corrected and resubmitted.
func (a *App) resetPassword(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("token")
	page := Page{View: "reset", Title: "Choose a new password", ResetToken: code}
	renderErr := func(status int, message string) {
		page.Error = message
		a.render(w, r, status, page)
	}
	if !validToken(code) {
		renderErr(400, errAuthToken.Error())
		return
	}
	// Check before the expensive password hash, then recheck while consuming
	// the token in the same transaction as the password and session changes.
	user, err := a.store.AuthTokenValid(r.Context(), token.Hash(code), TokenPasswordReset, time.Now())
	if err != nil {
		if errors.Is(err, errAuthToken) {
			renderErr(400, errAuthToken.Error())
			return
		}
		a.serverError(w, r, err)
		return
	}
	page.ResetUser = user.Username
	password, confirm := r.PostForm.Get("password"), r.PostForm.Get("confirm_password")
	if message := ValidatePassword(password); message != "" {
		renderErr(422, message)
		return
	}
	if password != confirm {
		renderErr(422, "The two passwords do not match.")
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	user, err = a.store.ResetPassword(r.Context(), token.Hash(code), hash, time.Now())
	if err != nil {
		if errors.Is(err, errAuthToken) {
			page.ResetUser = ""
			renderErr(400, errAuthToken.Error())
			return
		}
		a.serverError(w, r, err)
		return
	}
	if err := a.startSession(w, r, user.ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/")
}

func (a *App) renderMembers(w http.ResponseWriter, r *http.Request, status int, p Page) {
	members, err := a.store.Members(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p.View, p.Title, p.Members, p.MailEnabled = "members", "Members", members, a.mailer.Enabled()
	a.render(w, r, status, p)
}

func (a *App) members(w http.ResponseWriter, r *http.Request) {
	notice := ""
	switch r.URL.Query().Get("saved") {
	case "link-revoked":
		notice = "The reset link was cancelled. It can no longer be used."
	case "avatar-removed":
		notice = "The avatar was removed."
	case "suspend":
		notice = "The member was suspended and signed out."
	case "restore":
		notice = "The member can sign in again."
	}
	a.renderMembers(w, r, 200, Page{Notice: notice})
}

// createResetLink issues a single-use link for one member. The owner can copy
// it, or have Witmoot email it when a relay is configured.
func (a *App) createResetLink(w http.ResponseWriter, r *http.Request) {
	target, err := a.store.UserByID(r.Context(), pathID(r))
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	code, hash := token.New()
	if err := a.store.CreateAuthToken(r.Context(), target.ID, TokenPasswordReset, hash, time.Now().Add(ResetLinkTTL)); err != nil {
		a.storeError(w, r, err)
		return
	}
	p := Page{ResetLink: a.resetURL(r, code), ResetFor: target.Username}
	if r.PostForm.Get("email") == "1" {
		site := a.siteName(r)
		switch {
		case !a.mailer.Enabled():
			p.Error = "Mail is not configured on this server. Copy the link and share it yourself."
		case target.Email == "":
			p.Error = target.Username + " has no email address on file. Copy the link and share it yourself."
		default:
			if err := a.mailer.Send(r.Context(), ResetMessage(target, site, p.ResetLink, ResetLinkTTL)); err != nil {
				slog.Error("send reset link", "user", target.ID, "error", err)
				p.Error = "The link was created, but the email could not be sent. Copy the link and share it yourself."
			} else {
				p.ResetEmailed = true
			}
		}
	}
	a.renderMembers(w, r, 200, p)
}

func (a *App) revokeResetLink(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteAuthTokensForUser(r.Context(), pathID(r), TokenPasswordReset); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/members?saved=link-revoked")
}

// ResetMessage is the email a member receives with a reset link. The web and
// the admin view both use it, so the wording and stated expiry cannot drift
// from the link they send.
func ResetMessage(to User, site, link string, ttl time.Duration) smtp.Message {
	return smtp.Message{To: to.Email, Subject: "Choose a new password for " + site, Body: fmt.Sprintf(`Hello %s,

An owner of %s made a link so you can choose a new password. Open it here:

%s

The link works once and expires in %s.

If you did not ask for this, you can ignore this message: your password has not changed.
`, to.Username, site, link, HumanDuration(ttl))}
}

// HumanDuration renders a coarse duration for prose.
func HumanDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	case d >= time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	default:
		minutes := int(d.Minutes())
		if minutes <= 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
}
