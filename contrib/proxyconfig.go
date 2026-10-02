// SPDX-License-Identifier: AGPL-3.0-or-later

// Package contrib embeds the proxy examples shipped with the packages, so
// that witmoot proxy-config prints exactly what the packages install.
package contrib

import (
	"embed"

	"github.com/airencracken/comfylib/proxyconfig"
)

//go:embed caddy/Caddyfile nginx/witmoot.conf apache/witmoot.conf
var examples embed.FS

// ProxySpec describes Witmoot's proxy examples for comfylib's proxyconfig.
// The examples serve board.example.org from 127.0.0.1:8082, the native
// services' default address.
func ProxySpec() proxyconfig.Spec {
	return proxyconfig.Spec{
		App:              "witmoot",
		ExampleDomain:    "board.example.org",
		DefaultUpstream:  "127.0.0.1:8082",
		ProxyEnvironment: "WITMOOT_TRUSTED_PROXIES=127.0.0.1/32,::1/128",
		Examples:         examples,
	}
}
