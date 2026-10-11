// Command entra-appreg creates and manages Microsoft Entra app registrations
// through Microsoft Graph, authenticating with the Azure CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/AaronSaikovski/entra-appreg/internal/app"
	"github.com/AaronSaikovski/entra-appreg/internal/auth"
	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run executes one invocation and returns the exit code: 0 success, help or
// version; 1 operational failure; 2 invalid input.
func run(args []string, stdin *os.File, stdout io.Writer, stderr *os.File) int {
	inv, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	switch inv.Kind {
	case cli.KindHelp:
		fmt.Fprintln(stdout, cli.HelpText(inv.Topic))
		return 0
	case cli.KindVersion:
		fmt.Fprintln(stdout, "entra-appreg "+cli.Version)
		return 0
	case cli.KindNoArguments:
		fmt.Fprint(stderr, "No arguments given. Here is how to use this program:\n\n")
		fmt.Fprintln(stdout, cli.HelpText(cli.TopicGeneral))
		return 2
	}
	opts := inv.Options
	if opts.Command == cli.CommandDelete &&
		!(term.IsTerminal(int(stdin.Fd())) && term.IsTerminal(int(stderr.Fd()))) {
		fmt.Fprintln(stderr, "delete requires terminal input and terminal stderr for confirmation; nothing was deleted.")
		return 2
	}
	ctx := context.Background()
	session, err := auth.Authenticate(ctx, opts.Command == cli.CommandCreate)
	if err != nil {
		fmt.Fprintf(stderr, "Authentication failed: %v\n", err)
		return 1
	}
	client, err := graph.New(session.Credential)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	env := app.Env{Stdout: stdout, Stderr: stderr, Stdin: stdin, Now: time.Now, ReportDir: dir}
	if err := app.Run(ctx, opts, client, session.TenantID, env); err != nil {
		if usage, ok := errors.AsType[*cli.UsageError](err); ok {
			fmt.Fprintln(stderr, usage.Message)
			return 2
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}
