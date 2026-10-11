//go:build windows

package report

import (
	"path/filepath"
	"testing"
)

func TestReservedDeviceNamesArePrefixed(t *testing.T) {
	for in, want := range map[string]string{
		"NUL":         "_NUL.txt",
		"com1.backup": "_com1.backup.txt",
		"lpt\u00b9":   "_lpt\u00b9.txt",
		"CONSOLE":     "CONSOLE.txt",
	} {
		t.Run(in, func(t *testing.T) {
			if got := filepath.Base(mustWrite(t, t.TempDir(), in, "report")); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}
