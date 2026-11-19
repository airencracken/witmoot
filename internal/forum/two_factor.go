// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"context"
	"html/template"
	"net/http"
	"os"
	"time"

	"github.com/airencracken/comfylib/twofactor"
	"golang.org/x/crypto/bcrypt"
)

func (a *App) registerTwoFactor(mux *http.ServeMux) error {
	factor, err := twofactor.New(context.Background(), a.store.db, os.Getenv("WITMOOT_TOTP_KEY"),
		"Witmoot", "/", twofactor.Sessions{
			CredentialSQL: `SELECT password_hash FROM users WHERE id=? AND suspended=0 AND deleted=0 AND (role='owner' OR (SELECT mode FROM settings WHERE id=1)<>'personal')`,
			TokenColumn:   "token_hash", ExpiryColumn: "expires_at",
			Lifetime: 7 * 24 * time.Hour, Milliseconds: false,
		}, twofactor.Hooks{
			Request: func(r *http.Request) (int64, string, string) {
				u, csrf := state(r).User, state(r).CSRF
				if u == nil {
					return 0, "", csrf
				}
				session, err := r.Cookie(a.cookieName("session"))
				if err != nil {
					return 0, "", csrf
				}
				return u.ID, session.Value, csrf
			},
			CookieName: func(r *http.Request) string { return a.cookieName("twofactor") },
			Session: func(w http.ResponseWriter, r *http.Request, raw string) {
				a.cookie(w, "session", raw, 7*86400)
				a.cookie(w, "csrf", sessionCSRFToken(raw), 86400)
			},
			CheckPassword: func(hash, plain string) bool {
				return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
			},
			Render: func(w http.ResponseWriter, r *http.Request, status int, title string, content template.HTML) {
				a.render(w, r, status, Page{Title: title, View: "two-factor", FactorContent: content})
			},
			Fail: func(w http.ResponseWriter, r *http.Request, err error) { a.serverError(w, r, err) },
		})
	if err != nil {
		return err
	}
	a.factor = factor
	factor.Register(mux)
	return nil
}

func (a *App) finishFirstFactor(w http.ResponseWriter, r *http.Request, id int64, hash string) bool {
	target, handled, err := a.factor.Start(w, r, id, hash)
	if err != nil {
		a.serverError(w, r, err)
	} else if handled {
		if target == "/" {
			a.limiter.refund(a.rateKey(r))
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
	return handled
}
