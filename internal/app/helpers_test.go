package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
)

// frozenNow is the fixed clock used by every workflow test.
var frozenNow = time.Date(2028, 2, 28, 16, 30, 45, 0, time.UTC)

const keyID = "11111111-2222-3333-4444-555555555555"

func options(t *testing.T, args ...string) *cli.Options {
	t.Helper()
	inv, err := cli.Parse(args)
	if err != nil {
		t.Fatalf("parse %q: %v", args, err)
	}
	if inv.Kind != cli.KindRun {
		t.Fatalf("parse %q: expected a command, got kind %d", args, inv.Kind)
	}
	return inv.Options
}

type testIO struct {
	env            Env
	stdout, stderr *bytes.Buffer
}

func newTestIO(t *testing.T, stdin string) testIO {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	return testIO{
		env: Env{
			Stdout:    stdout,
			Stderr:    stderr,
			Stdin:     bytes.NewBufferString(stdin),
			Now:       func() time.Time { return frozenNow },
			ReportDir: t.TempDir(),
		},
		stdout: stdout,
		stderr: stderr,
	}
}

func ok(v any) graphtest.Response { return graphtest.JSON(200, v) }

func raw(status int, body string) graphtest.Response {
	return graphtest.Response{Status: status, Body: body}
}

func decode(t *testing.T, r graphtest.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatalf("%s %s body %q: %v", r.Method, r.Path, r.Body, err)
	}
	return body
}

// isUsage reports whether err is a usage error (exit code 2).
func isUsage(err error) bool {
	_, ok := errors.AsType[*cli.UsageError](err)
	return ok
}

// assertNoMethod fails the test if any recorded request used method.
func assertNoMethod(t *testing.T, requests []graphtest.Request, method string) {
	t.Helper()
	for _, r := range requests {
		if r.Method == method {
			t.Errorf("unexpected %s %s", r.Method, r.Path)
		}
	}
}

type obj = map[string]any
type arr = []any
