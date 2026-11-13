// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"fmt"
	sharedoidc "github.com/airencracken/comfylib/oidc"
	"github.com/airencracken/comfylib/suite"
	"net/http"
)

func (a *App) registerSharedSignIn(mux *http.ServeMux) error {
	auth, err := sharedoidc.FromEnvironment("WITMOOT_", a.config.BaseURL, a.store.db, suite.AuthHooks{
		RateKey: a.rateKey,
		Current: func(r *http.Request) (int64, string) {
			u := state(r).User
			if u == nil {
				return 0, ""
			}
			binding := ""
			if c, err := r.Cookie(a.cookieName("session")); err == nil {
				binding = c.Value
			}
			return u.ID, binding
		},
		CSRF:   func(r *http.Request) (string, string) { return "csrf", state(r).CSRF },
		SignIn: a.sharedSignIn,
	})
	if err != nil {
		return err
	}
	if auth != nil {
		auth.Register(mux)
	}
	return nil
}

func (a *App) sharedSignIn(w http.ResponseWriter, r *http.Request, id int64) (string, error) {
	u, err := a.store.UserByID(r.Context(), id)
	if err != nil || u.Suspended || state(r).Mode == ModePersonal && u.Role != "owner" {
		return "", fmt.Errorf("account unavailable")
	}
	if err = a.startSession(w, r, id); err != nil {
		return "", err
	}
	return "/", nil
}
