// Package app implements the entra-appreg workflows: create, expose-api, list
// and delete. Usage errors are *cli.UsageError (exit code 2); every other
// error is a runtime failure (exit code 1).
package app

import (
	"context"
	"io"
	"time"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

// Env is the process environment a workflow runs in.
type Env struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Now            func() time.Time
	ReportDir      string
}

// Run executes the command selected in opts. tenantID is "" unless the
// tenant was pinned during authentication.
func Run(ctx context.Context, opts *cli.Options, g *graph.Client, tenantID string, env Env) error {
	switch opts.Command {
	case cli.CommandExposeAPI:
		return exposeAPI(ctx, opts, g, env)
	case cli.CommandList:
		return list(ctx, opts, g, env)
	case cli.CommandDelete:
		return deleteApp(ctx, opts, g, env)
	default:
		return create(ctx, opts, g, tenantID, env)
	}
}

func deref(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}
