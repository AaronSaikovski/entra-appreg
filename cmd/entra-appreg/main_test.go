package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
)

// runOffline calls run with a non-terminal stdin (a closed pipe) and a regular
// file as stderr, returning the exit code and both outputs.
func runOffline(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinRead.Close()
	_ = stdinWrite.Close()
	errFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()
	var out bytes.Buffer

	code = run(args, stdinRead, &out, errFile)

	errText, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), string(errText)
}

// TestExitCodes runs the entry point offline: PATH is empty so az can never be
// reached.
func TestExitCodes(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		args   []string
		code   int
		stdout string // substring
		stderr string // substring
	}{
		{args: []string{}, code: 2, stdout: "USAGE", stderr: "No arguments given."},
		{args: []string{"--version"}, code: 0, stdout: "entra-appreg " + fullVersion() + "\n"},
		{args: []string{"list", "--top", "0"}, code: 2, stderr: "--top must be a positive whole number or 'all', got: 0"},
		{args: []string{"--appid", " "}, code: 2, stderr: "--appid cannot be blank"},
		{args: []string{"delete"}, code: 2},
		{args: []string{"delete", "--appid", "x"}, code: 2, stderr: "delete requires terminal input"},
		{args: []string{"delete", "--help"}, code: 0, stdout: "delete"},
		{args: []string{"list"}, code: 1, stderr: "Authentication failed: "},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			code, stdout, stderr := runOffline(t, tc.args...)
			if code != tc.code {
				t.Errorf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.code, stdout, stderr)
			}
			if !strings.Contains(stdout, tc.stdout) {
				t.Errorf("stdout %q lacks %q", stdout, tc.stdout)
			}
			if !strings.Contains(stderr, tc.stderr) {
				t.Errorf("stderr %q lacks %q", stderr, tc.stderr)
			}
		})
	}
}

// TestVersionOutputIsExact pins the --version line and the general help
// heading for injected build metadata, as the release pipelines link it.
func TestVersionOutputIsExact(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() { version, commit, date = oldVersion, oldCommit, oldDate })
	version, commit, date = "1.2.3", "abc1234", "2026-10-12T01:02:03Z"

	code, stdout, stderr := runOffline(t, "--version")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if want := "entra-appreg 1.2.3 (commit abc1234, built 2026-10-12T01:02:03Z)\n"; stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
	// The help heading shows only the short version.
	if _, stdout, _ := runOffline(t, "--help"); !strings.HasPrefix(stdout, "entra-appreg 1.2.3 - manage ") {
		t.Errorf("help heading = %q", strings.SplitN(stdout, "\n", 2)[0])
	}
}

func TestFailMapsUsageErrorsToExit2AndOthersToExit1(t *testing.T) {
	usage := &cli.UsageError{Message: "bad input"}
	for _, tc := range []struct {
		name string
		err  error
		code int
		want string
	}{
		{"usage", usage, 2, "bad input\n"},
		{"wrapped usage", fmt.Errorf("context: %w", usage), 2, "bad input\n"},
		{"runtime", errors.New("boom"), 1, "Error: boom\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w bytes.Buffer
			if code := fail(&w, tc.err); code != tc.code || w.String() != tc.want {
				t.Errorf("fail = %d %q, want %d %q", code, w.String(), tc.code, tc.want)
			}
		})
	}
}
