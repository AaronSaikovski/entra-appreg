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

// Exit codes.
const (
	exitOK      = 0 // success, help, version, cancelled delete
	exitFailure = 1 // operational failure: auth, Graph, I/O
	exitUsage   = 2 // invalid input, including no arguments and a non-TTY delete
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run executes one invocation and returns its exit code. Everything up to the
// delete terminal check is local; only execute talks to Azure.
func run(args []string, stdin *os.File, stdout io.Writer, stderr *os.File) int {
	inv, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	switch inv.Kind {
	case cli.KindHelp:
		fmt.Fprintln(stdout, cli.HelpText(inv.Topic))
		return exitOK
	case cli.KindVersion:
		fmt.Fprintln(stdout, "entra-appreg "+cli.Version())
		return exitOK
	case cli.KindNoArguments:
		fmt.Fprint(stderr, "No arguments given. Here is how to use this program:\n\n")
		fmt.Fprintln(stdout, cli.HelpText(cli.TopicGeneral))
		return exitUsage
	}
	opts := inv.Options
	if opts.Command == cli.CommandDelete && !canConfirm(stdin, stderr) {
		fmt.Fprintln(stderr, "delete requires terminal input and terminal stderr for confirmation; nothing was deleted.")
		return exitUsage
	}
	return execute(opts, stdin, stdout, stderr)
}

// canConfirm reports whether delete can prompt: the prompt goes to stderr and
// the answer is read from stdin, so both must be terminals.
func canConfirm(stdin, stderr *os.File) bool {
	return term.IsTerminal(int(stdin.Fd())) && term.IsTerminal(int(stderr.Fd()))
}

// execute authenticates, then runs the parsed command against Graph.
func execute(opts *cli.Options, stdin *os.File, stdout io.Writer, stderr *os.File) int {
	ctx := context.Background()
	session, err := auth.Authenticate(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Authentication failed: %v\n", err)
		return exitFailure
	}
	client, err := graph.New(session.Credential)
	if err != nil {
		return fail(stderr, err)
	}
	dir, err := os.Getwd()
	if err != nil {
		return fail(stderr, err)
	}
	env := app.Env{Stdout: stdout, Stderr: stderr, Stdin: stdin, Now: time.Now, ReportDir: dir}
	if err := app.Run(ctx, opts, client, env); err != nil {
		return fail(stderr, err)
	}
	return exitOK
}

// fail prints err to w and returns its exit code: a usage error prints its
// bare message and exits 2; anything else gets an "Error: " prefix and exits 1.
func fail(w io.Writer, err error) int {
	if usage, ok := errors.AsType[*cli.UsageError](err); ok {
		fmt.Fprintln(w, usage.Message)
		return exitUsage
	}
	fmt.Fprintf(w, "Error: %v\n", err)
	return exitFailure
}
