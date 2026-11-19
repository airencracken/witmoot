// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"strings"
	"testing"

	"github.com/airencracken/comfylib/twofactor/factortest"
)

func TestAuthenticatorSignIn(t *testing.T) {
	t.Setenv("WITMOOT_TOTP_KEY", strings.Repeat("ab", 32))
	app, browser := newTestApp(t, true)
	signInTest(t, app, browser, true)
	factortest.Run(t, factortest.Config{
		Handler: app, Origin: "https://example.test",
		Username: "alex", Password: "a long test password", Home: "/",
	})
}
