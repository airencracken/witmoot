// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build proxyintegration

package proxy_test

import (
	"testing"

	"github.com/airencracken/comfylib/proxyconfig/proxytest"

	"witmoot/contrib"
)

// TestReverseProxies runs the generated nginx and Apache configurations in
// real servers: make test-proxies.
func TestReverseProxies(t *testing.T) {
	proxytest.ReverseProxies(t, contrib.ProxySpec())
}
