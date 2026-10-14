// Package app implements the entra-appreg workflows: create, expose-api, list
// and delete. Usage errors are *cli.UsageError (exit code 2); every other
// error is a runtime failure (exit code 1).
package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graph"
	"github.com/AaronSaikovski/entra-appreg/internal/keyvault"
)

// SecretStore saves one secret version and returns the ID the store assigns.
type SecretStore interface {
	SetSecret(ctx context.Context, s keyvault.Secret) (string, error)
}

// Env is the process environment a workflow runs in.
type Env struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Now            func() time.Time
	ReportDir      string

	// KeyVault opens the secret store for a validated vault URI. create
	// calls it once, before anything is created, so a missing Key Vault
	// token fails before the tenant changes. nil when unavailable.
	KeyVault func(ctx context.Context, vaultURI string) (SecretStore, error)
}

// Run executes the command selected in opts.
func Run(ctx context.Context, opts *cli.Options, g *graph.Client, env Env) error {
	switch opts.Command {
	case cli.CommandExposeAPI:
		return exposeAPI(ctx, opts, g, env)
	case cli.CommandList:
		return list(ctx, opts, g, env)
	case cli.CommandDelete:
		return deleteApp(ctx, opts, g, env)
	case cli.CommandCreate:
		return create(ctx, opts, g, env)
	default:
		return fmt.Errorf("unsupported command %d", opts.Command)
	}
}

func deref(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}
