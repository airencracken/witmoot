// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"net/http"

	"github.com/airencracken/comfylib/clientip"
)

// clients believes forwarding headers only from WITMOOT_TRUSTED_PROXIES. An
// empty list trusts none, including requests from loopback.
func (a *App) clients() clientip.Resolver {
	return clientip.Resolver{Trusted: a.config.TrustedProxies}
}

// rateKey names the network a request's sign-in attempts are counted
// against: the client behind any trusted proxies, with IPv6 grouped by /64 so
// one host cannot claim a fresh budget for every address it owns.
func (a *App) rateKey(r *http.Request) string {
	return clientip.NetworkKey(a.clients().Client(r))
}
