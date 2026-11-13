// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"strings"
	"testing"
)

// The home page explains access in one plain sidebar sentence, for every mode,
// without a separate site access block.
func TestHomeExplainsAccessOnceInTheSidebar(t *testing.T) {
	app, owner := newTestApp(t, false)
	signInTest(t, app, owner, true)
	member := memberClient(t, app, "jules")
	for mode, sentence := range map[Mode]string{
		ModePrivate:  "Members can read and join the shared conversations here. New people join by invitation.",
		ModeOpen:     "Public conversations are open to everyone. Create an account to take part.",
		ModePersonal: "Only owners can use this board.",
	} {
		selectMode(t, owner, mode)
		for name, client := range map[string]*testClient{"owner": owner, "member": member} {
			w := client.request("GET", "/", nil, nil)
			if mode == ModePersonal && name == "member" {
				continue
			}
			body := w.Body.String()
			if strings.Contains(body, "Site access") || strings.Contains(body, "site-access") {
				t.Errorf("%s in %s mode still sees the site access block", name, mode)
			}
			if strings.Count(body, sentence) != 1 {
				t.Errorf("%s in %s mode: want the sidebar sentence %q once", name, mode, sentence)
			}
		}
	}
}
