package app

import (
	"strings"
	"testing"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
)

func TestRunRejectsUnsupportedCommandWithoutGraphRequests(t *testing.T) {
	f := graphtest.New(t)
	o := options(t, "list")
	o.Command = cli.CommandDelete + 1
	err := Run(t.Context(), o, f.Client(), newTestIO(t, "").env)
	if err == nil || !strings.Contains(err.Error(), "unsupported command") {
		t.Fatalf("err = %v, want unsupported command", err)
	}
	if n := len(f.Requests()); n != 0 {
		t.Errorf("%d Graph requests, want 0", n)
	}
}
