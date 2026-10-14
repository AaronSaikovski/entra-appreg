package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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

// upperASCII upper-cases only a-z, so non-ASCII letters such as ı or ſ keep
// their bytes and sort after every ASCII name instead of folding to I or S.
func upperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}

// collectApplications returns up to o.ListLimit applications sorted by name
// (case-insensitive for ASCII letters only, stable), and whether more exist
// beyond the limit.
// The limit applies in Graph's order, before sorting.
func collectApplications(ctx context.Context, o *cli.Options, g *graph.Client) ([]listedApp, bool, error) {
	limit := o.ListLimit
	apps := []listedApp{}
	more := false
	err := g.ApplicationPages(ctx, listQuery(o), func(page models.ApplicationCollectionResponseable, hasNext bool) (bool, error) {
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
			apps = append(apps, listedApp{Name: name, ClientID: *clientID, ID: *id, sortKey: upperASCII(name)})
		}
		if len(apps) < limit {
			return false, nil
		}
		more = more || hasNext
		return true, nil
	})
	if err != nil {
		return nil, false, err
	}
	slices.SortStableFunc(apps, func(a, b listedApp) int { return strings.Compare(a.sortKey, b.sortKey) })
	return apps, more, nil
}

// listQuery selects only the listed fields and pages at most 999 items, the
// Graph maximum. --name filters by display-name prefix.
func listQuery(o *cli.Options) *applications.ApplicationsRequestBuilderGetQueryParameters {
	q := &applications.ApplicationsRequestBuilderGetQueryParameters{
		Select: []string{"id", "appId", "displayName"},
		Top:    new(int32(min(o.ListLimit, 999))),
	}
	if o.Name != nil {
		q.Filter = new("startswith(displayName,'" + strings.ReplaceAll(strings.TrimSpace(*o.Name), "'", "''") + "')")
	}
	return q
}

// list prints the signed-in tenant and its applications. With --json, stdout
// carries only the JSON array; the tenant and notices go to stderr.
func list(ctx context.Context, o *cli.Options, g *graph.Client, env Env) error {
	tenantID, tenantName, err := currentTenant(ctx, g)
	if err != nil {
		return err
	}
	apps, more, err := collectApplications(ctx, o, g)
	if err != nil {
		return err
	}
	if o.JSON {
		fmt.Fprintf(env.Stderr, "Tenant: %s\nTenant ID: %s\n", strconv.Quote(tenantName), tenantID)
		if err := writeListJSON(env.Stdout, apps); err != nil {
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

// currentTenant returns the ID and display name of the only organization
// visible to the signed-in account.
func currentTenant(ctx context.Context, g *graph.Client) (id, name string, err error) {
	orgs, err := g.Organization(ctx)
	if err != nil {
		return "", "", err
	}
	if orgs == nil || len(orgs.GetValue()) != 1 {
		return "", "", errors.New("Graph must return exactly one tenant organization.")
	}
	org := orgs.GetValue()[0]
	if org == nil || org.GetId() == nil {
		return "", "", graph.MissingFieldError("id")
	}
	if org.GetDisplayName() == nil {
		return "", "", graph.MissingFieldError("displayName")
	}
	return *org.GetId(), *org.GetDisplayName(), nil
}

// writeListJSON writes apps as an indented JSON array without HTML escaping.
func writeListJSON(w io.Writer, apps []listedApp) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(apps)
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
