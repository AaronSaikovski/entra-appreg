package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

// deleteApp resolves the target first, then deletes it only after the
// operator confirms on stdin. Every prompt and result line goes to stderr.
func deleteApp(ctx context.Context, o *cli.Options, g *graph.Client, env Env) error {
	reference := *o.AppID // cli.Parse requires --appid for delete
	app, err := g.GetApplication(ctx, reference)
	if err != nil {
		return err
	}
	if app == nil {
		return fmt.Errorf("Application not found: %s", reference)
	}
	objectID, clientID := *app.GetId(), *app.GetAppId()
	name := deref(app.GetDisplayName(), "(unnamed)")
	confirmed, err := confirmDeletion(env, name, objectID, clientID)
	if err != nil || !confirmed {
		return err
	}
	if err := g.DeleteApplication(ctx, objectID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(env.Stderr, "Deleted application %s (Application (client) ID: %s).\n",
		strconv.Quote(objectID), strconv.Quote(clientID)); err != nil {
		return fmt.Errorf("Application was deleted, but could not display completion: %w", err)
	}
	return nil
}

// confirmDeletion shows the target and reads one answer line. Only an exact
// "yes" line confirms; anything else, including EOF, cancels. A failed prompt
// write is an error so the operator never confirms an unseen target.
func confirmDeletion(env Env, name, objectID, clientID string) (bool, error) {
	prompt := env.Stderr
	// Quote remote fields so terminal control characters cannot disguise the target.
	if _, err := fmt.Fprintf(prompt, "Delete this app registration?\n  Name: %s\n  Object ID: %s\n  Application (client) ID: %s\nDeletion can interrupt applications using this registration.\n",
		strconv.Quote(name), strconv.Quote(objectID), strconv.Quote(clientID)); err != nil {
		return false, fmt.Errorf("Could not display deletion target: %w", err)
	}
	if _, err := io.WriteString(prompt, "Type 'yes' to delete; any other answer cancels: "); err != nil {
		return false, fmt.Errorf("Could not display deletion prompt: %w", err)
	}
	answer, err := bufio.NewReader(env.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("Could not read deletion confirmation; nothing was deleted: %w", err)
	}
	if answer == "yes\n" || answer == "yes\r\n" {
		return true, nil
	}
	if _, err := io.WriteString(prompt, "Deletion cancelled. Nothing was deleted.\n"); err != nil {
		return false, fmt.Errorf("Could not display deletion cancellation: %w", err)
	}
	return false, nil
}
