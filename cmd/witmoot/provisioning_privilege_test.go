package main

import (
	"reflect"
	"testing"
)

func TestWithEnvironmentReplacesOnlyTheConfiguredKeys(t *testing.T) {
	got := withEnvironment([]string{"PATH=/bin", "WITMOOT_DATA_DIR=/wrong", "WITMOOT_DATA_DIR_EXTRA=kept", "HOME=/root", "WITMOOT_BASE_URL=https://old.example.org"},
		map[string]string{"WITMOOT_DATA_DIR": "/var/lib/witmoot", "WITMOOT_BASE_URL": "https://board.example.org", "WITMOOT_SMTP_HOST": ""})
	want := []string{"PATH=/bin", "WITMOOT_DATA_DIR_EXTRA=kept", "HOME=/root", "WITMOOT_BASE_URL=https://board.example.org", "WITMOOT_DATA_DIR=/var/lib/witmoot"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
}

func TestProvisioningHelpDoesNotReexecuteAsServiceUser(t *testing.T) {
	for _, args := range [][]string{{"create-owner", "--help"}, {"create-owner", "-h"}} {
		handled, status, err := reexecProvisioningAsService(args)
		if handled || status != 0 || err != nil {
			t.Fatalf("reexec for %v = handled %t status %d err %v", args, handled, status, err)
		}
	}
}
