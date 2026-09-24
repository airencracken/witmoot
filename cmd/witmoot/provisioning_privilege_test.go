package main

import (
	"reflect"
	"testing"
)

func TestWithDataDirEnvironmentReplacesOnlyTheConfiguredKey(t *testing.T) {
	got := withDataDirEnvironment([]string{"PATH=/bin", "WITMOOT_DATA_DIR=/wrong", "HOME=/root"}, "WITMOOT_DATA_DIR", "/var/lib/witmoot")
	want := []string{"PATH=/bin", "HOME=/root", "WITMOOT_DATA_DIR=/var/lib/witmoot"}
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
