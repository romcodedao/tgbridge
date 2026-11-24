package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

func TestOrcaOnlyWhenEnabled(t *testing.T) {
	c := config.Defaults()
	if orcaFor(c) != nil {
		t.Fatal("orca_enabled=false must not construct the Orca CLI")
	}
	c.OrcaEnabled, c.OrcaCLI = true, "/o/orca"
	if o := orcaFor(c); o == nil || o.Bin != "/o/orca" {
		t.Fatalf("orca_enabled=true: got %+v", o)
	}
}

func TestWhoamiConflictHint(t *testing.T) {
	err := whoamiErr(&telegram.APIError{Code: 409, Description: "Conflict"})
	if !strings.Contains(err.Error(), "systemctl --user stop tgbridge") || !strings.Contains(err.Error(), "deleteWebhook") {
		t.Fatalf("409 needs a hint, got %v", err)
	}
	if e := errors.New("boom"); whoamiErr(e) != e {
		t.Fatal("other errors pass through")
	}
}
