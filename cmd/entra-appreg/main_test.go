package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
)

// TestExitCodes runs the entry point offline: PATH is empty so az can never be
// reached, stdin is a pipe (not a terminal) and stderr is a regular file.
func TestExitCodes(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		args   []string
		code   int
		stdout string // substring
		stderr string // substring
	}{
		{args: []string{}, code: 2, stdout: "USAGE", stderr: "No arguments given."},
		{args: []string{"--version"}, code: 0, stdout: "entra-appreg " + cli.Version + "\n"},
		{args: []string{"list", "--top", "0"}, code: 2, stderr: "--top must be a positive whole number or 'all', got: 0"},
		{args: []string{"--appid", " "}, code: 2, stderr: "--appid cannot be blank"},
		{args: []string{"delete"}, code: 2},
		{args: []string{"delete", "--appid", "x"}, code: 2, stderr: "delete requires terminal input"},
		{args: []string{"delete", "--help"}, code: 0, stdout: "delete"},
		{args: []string{"list"}, code: 1, stderr: "Authentication failed: "},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdinRead, stdinWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdinRead.Close()
			_ = stdinWrite.Close()
			stderr, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			defer stderr.Close()
			var stdout bytes.Buffer

			code := run(tc.args, stdinRead, &stdout, stderr)

			errText, err := os.ReadFile(stderr.Name())
			if err != nil {
				t.Fatal(err)
			}
			if code != tc.code {
				t.Errorf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.code, stdout.String(), errText)
			}
			if !strings.Contains(stdout.String(), tc.stdout) {
				t.Errorf("stdout %q lacks %q", stdout.String(), tc.stdout)
			}
			if !strings.Contains(string(errText), tc.stderr) {
				t.Errorf("stderr %q lacks %q", errText, tc.stderr)
			}
		})
	}
}

func TestVersionOutputIsExact(t *testing.T) {
	var stdout bytes.Buffer
	if code := run([]string{"--version"}, os.Stdin, &stdout, os.Stderr); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, want := stdout.String(), "entra-appreg "+cli.Version+"\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
