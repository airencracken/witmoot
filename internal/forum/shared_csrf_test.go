// SPDX-License-Identifier: AGPL-3.0-or-later

package forum

import (
	"strings"
	"testing"
)

func TestSharedCSRFCompatibility(t *testing.T) {
	if got := sessionCSRFToken(strings.Repeat("a", 64)); got != "d50afcd33e36c9ca4d2005e0be2c1e5209f24fe3de4cdb403754600ea42439ea" {
		t.Fatal("existing session CSRF tokens changed", got)
	}
}
