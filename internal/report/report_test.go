package report

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func mustWrite(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path, err := Write(dir, name, contents)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func countEntries(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestSpacesBecomeHyphensWithoutOverwritingExistingReports(t *testing.T) {
	dir := t.TempDir()
	first := mustWrite(t, dir, "Test-App-Delete1", "original")
	second := mustWrite(t, dir, "Test App Delete1", "new")
	if filepath.Base(first) != "Test-App-Delete1.txt" || filepath.Base(second) != "Test-App-Delete1-1.txt" {
		t.Fatalf("names = %q, %q", first, second)
	}
	if readFile(t, first) != "original" || readFile(t, second) != "new" {
		t.Fatal("contents mismatch")
	}
}

func TestOccupiedFileAndDirectoryAreNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "example.txt"), []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "example-1.txt"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := mustWrite(t, dir, "example", "fake secret")
	if path != filepath.Join(dir, "example-2.txt") || !filepath.IsAbs(path) {
		t.Fatalf("path = %q", path)
	}
	if readFile(t, path) != "fake secret" || readFile(t, filepath.Join(dir, "example.txt")) != "sentinel" {
		t.Fatal("contents mismatch")
	}
	if info, err := os.Stat(filepath.Join(dir, "example-1.txt")); err != nil || !info.IsDir() {
		t.Fatal("directory was replaced")
	}
}

func TestConcurrentWritersEachClaimADistinctCompleteReport(t *testing.T) {
	dir := t.TempDir()
	const workers = 8
	var start, done sync.WaitGroup
	start.Add(1)
	paths := make([]string, workers)
	errs := make([]error, workers)
	for i := range workers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			paths[i], errs[i] = Write(dir, "example", "fake secret "+string(rune('0'+i)))
		}()
	}
	start.Done()
	done.Wait()
	seen := map[string]bool{}
	for i, path := range paths {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if got := readFile(t, path); got != "fake secret "+string(rune('0'+i)) {
			t.Fatalf("%s contains %q", path, got)
		}
		seen[path] = true
	}
	if len(seen) != workers || countEntries(t, dir) != workers {
		t.Fatalf("distinct=%d entries=%d", len(seen), countEntries(t, dir))
	}
}

func TestSanitizesPlatformCharactersAndTrimsOnlySpacesAndDots(t *testing.T) {
	dir := t.TempDir()
	if got := filepath.Base(mustWrite(t, dir, " .a/\x00b. ", "report")); got != "a__b.txt" {
		t.Fatalf("got %q", got)
	}
	if got := filepath.Base(mustWrite(t, dir, " . . ", "report")); got != "app-registration.txt" {
		t.Fatalf("got %q", got)
	}
	want := "a\\b:c*?\"<>|.txt"
	if runtime.GOOS == "windows" {
		want = "a_b_c______.txt"
	}
	if got := filepath.Base(mustWrite(t, dir, "a\\b:c*?\"<>|", "report")); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGenuineOpenFailuresPropagateWithoutCreatingAlternateReports(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, strings.Repeat("x", 1024), "secret"); err == nil {
		t.Fatal("expected error for overlong name")
	}
	if n := countEntries(t, dir); n != 0 {
		t.Fatalf("entries = %d", n)
	}
	missing := filepath.Join(dir, "missing")
	if _, err := Write(missing, "example", "secret"); err == nil {
		t.Fatal("expected error for missing directory")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing directory was created")
	}
}

func TestGenuineWriteFailuresPropagate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(path, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A read-only descriptor fails even when tests run as an elevated user.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeContents(f, "fake secret"); err == nil {
		t.Fatal("expected write error")
	}
	if readFile(t, path) != "unchanged" {
		t.Fatal("file changed")
	}
}
