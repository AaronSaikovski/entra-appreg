package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

type listedApp struct {
	Name     string `json:"displayName"`
	ClientID string `json:"appId"`
	ID       string `json:"id"`
	sortKey  string
}

func collectApplications(ctx context.Context, o *cli.Options, g *graph.Client) ([]listedApp, bool, error) {
	limit := o.ListLimit
	q := &applications.ApplicationsRequestBuilderGetQueryParameters{
		Select: []string{"id", "appId", "displayName"},
		Top:    new(int32(min(limit, 999))),
	}
	if o.Name != nil {
		q.Filter = new("startswith(displayName,'" + strings.ReplaceAll(strings.TrimSpace(*o.Name), "'", "''") + "')")
	}
	apps := []listedApp{}
	more := false
	err := g.ApplicationPages(ctx, q, func(page models.ApplicationCollectionResponseable, hasNext bool) (bool, error) {
		items := page.GetValue()
		if items == nil {
			return false, errors.New("Graph application list is missing a 'value' array.")
		}
		remaining := limit - len(apps)
		more = len(items) > remaining
		for _, item := range items[:min(len(items), remaining)] {
			if item == nil {
				return false, errors.New("Graph returned an invalid application list entry.")
			}
			clientID, id := item.GetAppId(), item.GetId()
			if clientID == nil {
				return false, graph.MissingFieldError("appId")
			}
			if id == nil {
				return false, graph.MissingFieldError("id")
			}
			name := deref(item.GetDisplayName(), "")
			apps = append(apps, listedApp{Name: name, ClientID: *clientID, ID: *id, sortKey: strings.ToUpper(name)})
		}
		if len(apps) == limit {
			more = more || hasNext
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(apps, func(i, j int) bool { return apps[i].sortKey < apps[j].sortKey })
	return apps, more, nil
}

func list(ctx context.Context, o *cli.Options, g *graph.Client, env Env) error {
	orgs, err := g.Organization(ctx)
	if err != nil {
		return err
	}
	if orgs == nil || len(orgs.GetValue()) != 1 {
		return errors.New("Graph must return exactly one tenant organization.")
	}
	tenants := orgs.GetValue()
	if tenants[0] == nil || tenants[0].GetId() == nil {
		return graph.MissingFieldError("id")
	}
	if tenants[0].GetDisplayName() == nil {
		return graph.MissingFieldError("displayName")
	}
	tenantID, tenantName := *tenants[0].GetId(), *tenants[0].GetDisplayName()
	apps, more, err := collectApplications(ctx, o, g)
	if err != nil {
		return err
	}
	if o.JSON {
		fmt.Fprintf(env.Stderr, "Tenant: %s\nTenant ID: %s\n", strconv.Quote(tenantName), tenantID)
		encoder := json.NewEncoder(env.Stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(apps); err != nil {
			return err
		}
		if more {
			fmt.Fprintf(env.Stderr, "Showing the first %d; more exist. Use --top or --name.\n", o.ListLimit)
		}
		return nil
	}
	if _, err := fmt.Fprintf(env.Stdout, "Tenant: %s\nTenant ID: %s\n\n", strconv.Quote(tenantName), tenantID); err != nil {
		return err
	}
	return writeListText(env.Stdout, apps, more, o.Name)
}

// writeListText prints the human-readable list. Remote display names are
// quoted so terminal control characters cannot disguise entries.
func writeListText(w io.Writer, apps []listedApp, more bool, name *string) error {
	var b strings.Builder
	switch {
	case len(apps) == 0 && name != nil:
		fmt.Fprintf(&b, "No app registrations found with a name starting with '%s'.\n", strings.TrimSpace(*name))
	case len(apps) == 0:
		b.WriteString("No app registrations found.\n")
	default:
		width := len(strconv.Itoa(len(apps)))
		indent := strings.Repeat(" ", width+2)
		for i, app := range apps {
			fmt.Fprintf(&b, "%*d. %s\n", width, i+1, strconv.Quote(app.Name))
			fmt.Fprintf(&b, "%sApplication (client) ID: %s\n", indent, app.ClientID)
			fmt.Fprintf(&b, "%sObject ID: %s\n", indent, app.ID)
		}
		b.WriteString("\n")
		if more {
			fmt.Fprintf(&b, "Showing the first %d app registrations; more exist. Use --top <n|all> or --name <text> to see others.\n", len(apps))
		} else {
			fmt.Fprintf(&b, "%d app registration(s).\n", len(apps))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
