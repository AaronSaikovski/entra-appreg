//go:build unix

package report

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveAndDanglingSymlinksAreCollisionsNotTargets(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	missing := filepath.Join(dir, "missing")
	if err := os.WriteFile(target, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "example.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(missing, filepath.Join(dir, "example-1.txt")); err != nil {
		t.Fatal(err)
	}
	path, err := Write(dir, "example", "fake secret")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "example-2.txt") {
		t.Fatalf("path = %q", path)
	}
	if b, _ := os.ReadFile(target); string(b) != "sentinel" {
		t.Fatalf("target changed: %q", b)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("dangling target created: %v", err)
	}
	if link, _ := os.Readlink(filepath.Join(dir, "example-1.txt")); link != missing {
		t.Fatalf("link = %q", link)
	}
}

func TestReportIsOwnerOnlyFromCreation(t *testing.T) {
	path, err := Write(t.TempDir(), "example", "fake secret")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o", mode)
	}
}
