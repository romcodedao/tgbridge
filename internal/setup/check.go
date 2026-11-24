// Package setup holds the building blocks of tgbridge's init, doctor, service and uninstall commands.
package setup

import (
	"context"
	"fmt"
	"io"
)

// Runner runs a command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) (string, error)

// Status is the outcome of one check.
type Status int

const (
	OK Status = iota
	Info
	Warn
	Fail
)

var words = [...]string{"ok", "info", "warn", "FAIL"}

// Check is one row of init/doctor output.
type Check struct {
	Status            Status
	Name, Detail, Fix string
}

// Print writes the rows aligned: status, name, detail, then the fix on its own line.
func Print(w io.Writer, cs []Check) {
	n := 0
	for _, c := range cs {
		n = max(n, len(c.Name))
	}
	for _, c := range cs {
		fmt.Fprintf(w, "  %-6s%-*s  %s\n", words[c.Status], n, c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(w, "        fix: %s\n", c.Fix)
		}
	}
}

// Failed reports whether any check is a Fail.
func Failed(cs []Check) bool {
	for _, c := range cs {
		if c.Status == Fail {
			return true
		}
	}
	return false
}
