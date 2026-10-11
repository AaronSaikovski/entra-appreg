//go:build windows

package report

import (
	"path/filepath"
	"testing"
)

func TestReservedDeviceNamesArePrefixed(t *testing.T) {
	dir := t.TempDir()
	for in, want := range map[string]string{"NUL": "_NUL.txt", "com1.backup": "_com1.backup.txt", "CONSOLE": "CONSOLE.txt"} {
		path, err := Write(dir, in, "report")
		if err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(path); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
