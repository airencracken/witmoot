package openrc_test

import (
	"strings"
	"testing"
)

func TestSMTPAssignmentsReachDaemon(t *testing.T) {
	out, err := runService(`
WITMOOT_SMTP_HOST=smtp.example.org
WITMOOT_SMTP_PORT=465
WITMOOT_SMTP_USERNAME=witmoot
WITMOOT_SMTP_PASSWORD=audit-placeholder
WITMOOT_SMTP_FROM='Witmoot <no-reply@example.org>'
WITMOOT_SMTP_TLS=implicit
. ./witmoot || exit 1
checkpath() { return 0; }
start_pre || exit 1
env
`)
	if err != nil {
		t.Fatalf("service startup: %s (%v)", out, err)
	}
	for _, setting := range []string{"WITMOOT_SMTP_HOST=smtp.example.org", "WITMOOT_SMTP_PORT=465", "WITMOOT_SMTP_USERNAME=witmoot", "WITMOOT_SMTP_PASSWORD=audit-placeholder", "WITMOOT_SMTP_FROM=Witmoot <no-reply@example.org>", "WITMOOT_SMTP_TLS=implicit"} {
		if !strings.Contains("\n"+string(out), "\n"+setting+"\n") {
			t.Errorf("daemon did not receive %s", setting)
		}
	}
}
