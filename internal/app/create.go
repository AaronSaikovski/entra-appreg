package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graph"
	"github.com/AaronSaikovski/entra-appreg/internal/report"
)

var audiences = []string{
	"AzureADMyOrg",
	"AzureADMultipleOrgs",
	"AzureADandPersonalMicrosoftAccount",
	"PersonalMicrosoftAccount",
}

// validRedirect requires an explicit http(s) authority. Inputs that a lenient
// parser would repair ("https:host", extra leading slashes) are rejected.
func validRedirect(uri string) bool {
	scheme, authority, found := strings.Cut(uri, "://")
	if !found || !(cli.EqualFoldASCII(scheme, "http") || cli.EqualFoldASCII(scheme, "https")) ||
		strings.HasPrefix(authority, "/") || strings.HasPrefix(authority, `\`) {
		return false
	}
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return false
		}
	}
	return true
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func create(ctx context.Context, o *cli.Options, g *graph.Client, tenantID string, env Env) error {
	out := env.Stdout
	if o.AppID != nil && *o.AppID != "no-id" {
		reference := *o.AppID
		fmt.Fprintf(out, "Checking if application %s exists\n", reference)
		app, err := g.GetApplication(ctx, reference)
		if err != nil {
			return err
		}
		if app != nil {
			fmt.Fprintln(out, "Application already exists, not creating new one.")
			return nil
		}
		fmt.Fprintln(out, "Application not found")
	}
	if o.Name == nil || strings.TrimSpace(*o.Name) == "" {
		return cli.Usagef("--name is required when creating an application.")
	}
	name := *o.Name
	rawAudience := trimmedOr(o.Audience, "AzureADMyOrg")
	audience := ""
	for _, a := range audiences {
		if cli.EqualFoldASCII(a, rawAudience) {
			audience = a
			break
		}
	}
	if audience == "" {
		return cli.Usagef("--audience must be one of %s, got: %s", strings.Join(audiences, ", "), rawAudience)
	}
	redirects := []string{}
	seen := map[string]bool{}
	for _, uri := range o.RedirectURIs {
		uri = strings.TrimSpace(uri)
		if !validRedirect(uri) {
			return cli.Usagef("--redirect-urls must contain absolute http(s) URIs, got: %s", uri)
		}
		if !seen[uri] {
			seen[uri] = true
			redirects = append(redirects, uri)
		}
	}
	var spec *scopeSpec
	if o.ScopeName != nil {
		resolved, err := resolveScope(o, name)
		if err != nil {
			return err
		}
		spec = &resolved
	} else {
		var orphaned []string
		for _, option := range []struct {
			flag  string
			value *string
		}{
			{"--scope-display-name", o.ScopeDisplayName},
			{"--scope-description", o.ScopeDescription},
			{"--scope-consent", o.ScopeConsent},
			{"--scope-user-display-name", o.ScopeUserDisplayName},
			{"--scope-user-description", o.ScopeUserDescription},
			{"--scope-state", o.ScopeState},
		} {
			if option.value != nil {
				orphaned = append(orphaned, option.flag)
			}
		}
		if len(orphaned) > 0 {
			return cli.Usagef("%s require --scope-name.", strings.Join(orphaned, ", "))
		}
	}
	lifetime, err := resolveLifetime(o, env.Now())
	if err != nil {
		return err
	}

	err = g.ApplicationPages(ctx, &applications.ApplicationsRequestBuilderGetQueryParameters{
		Filter: new("displayName eq '" + strings.ReplaceAll(name, "'", "''") + "'"),
		Select: []string{"id"},
		Top:    new(int32(1)),
	}, func(page models.ApplicationCollectionResponseable, _ bool) (bool, error) {
		matches := page.GetValue()
		if matches == nil {
			return false, errors.New("Graph returned an invalid name-check response.")
		}
		if len(matches) > 0 {
			return false, cli.Usagef("An app registration named %q already exists in this tenant. Choose a different --name, or use --appid to reference an existing registration.", name)
		}
		return false, nil
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Creating application registration '%s'\n", name)
	fmt.Fprintf(out, "  Supported account types: %s\n", audience)
	for _, uri := range redirects {
		fmt.Fprintf(out, "  Redirect URI (SPA): %s\n", uri)
	}
	needsV2 := audience == "AzureADandPersonalMicrosoftAccount" || audience == "PersonalMicrosoftAccount"
	body := models.NewApplication()
	body.SetDisplayName(new(name))
	body.SetSignInAudience(new(audience))
	spa := models.NewSpaApplication()
	spa.SetRedirectUris(redirects) // non-nil, so "redirectUris":[] is sent
	body.SetSpa(spa)
	if needsV2 {
		api := models.NewApiApplication()
		api.SetRequestedAccessTokenVersion(new(int32(2)))
		body.SetApi(api)
	}
	app, err := g.CreateApplication(ctx, body)
	if err != nil {
		return err
	}
	objectID, clientID := *app.GetId(), *app.GetAppId()
	// Printed immediately so the IDs survive any later failure.
	fmt.Fprintf(out, "AUTH_APP_ID=%s\n", objectID)
	fmt.Fprintf(out, "AUTH_CLIENT_ID=%s\n", clientID)

	var contents strings.Builder
	fmt.Fprintf(&contents, "Application name:        %s\nApplication (client) ID: %s\nObject ID:               %s\nDirectory (tenant) ID:   %s\nSupported account types: %s\n",
		name, clientID, objectID, tenantID, audience)
	if spec != nil {
		identifier := "api://" + clientID
		fullName := identifier + "/" + spec.name
		scopeID := uuid.New()
		fmt.Fprintf(out, "Exposing API: adding scope '%s' (%s, %s)\n", fullName, spec.consentLabel(), spec.stateLabel())
		api := models.NewApiApplication()
		api.SetOauth2PermissionScopes([]models.PermissionScopeable{buildScope(scopeID, *spec)})
		if needsV2 {
			api.SetRequestedAccessTokenVersion(new(int32(2)))
		}
		patch := models.NewApplication()
		patch.SetIdentifierUris([]string{identifier})
		patch.SetApi(api)
		if err := g.PatchApplication(ctx, objectID, patch); err != nil {
			return err
		}
		fmt.Fprintf(&contents, "Application ID URI:      %s\nScope (full name):       %s\nScope ID:                %s\nScope display name:      %s\nScope description:       %s\nScope consent:           %s\nScope state:             %s\n",
			identifier, fullName, scopeID, spec.displayName, spec.description, spec.consentLabel(), spec.stateLabel())
		if spec.userConsent {
			fmt.Fprintf(&contents, "Scope user display name: %s\nScope user description:  %s\n", spec.userDisplayName, spec.userDescription)
		}
	} else {
		fmt.Fprintln(out, "Skipping API scope (pass --scope-name to expose an API)")
	}
	var invalidSecret error
	if lifetime != nil {
		description := secretDescription(name)
		fmt.Fprintf(out, "Adding client secret '%s' to %s\n", description, objectID)
		credential := models.NewPasswordCredential()
		credential.SetDisplayName(new(description))
		credential.SetEndDateTime(new(lifetime.end))
		if lifetime.start != nil {
			credential.SetStartDateTime(lifetime.start)
		}
		secret, err := g.AddPassword(ctx, objectID, credential)
		if err != nil {
			return err
		}
		text := secret.GetSecretText()
		if text == nil {
			return graph.MissingFieldError("secretText")
		}
		// The secret is shown only once: record it even if other fields are missing.
		key := "(not returned by Graph)"
		if id := secret.GetKeyId(); id != nil {
			key = id.String()
		} else {
			invalidSecret = errors.New("Graph's addPassword response had a missing or invalid keyId; the secret value was preserved in the report.")
		}
		startText := "now"
		if start := secret.GetStartDateTime(); start != nil {
			startText = formatTime(*start)
		}
		endText := formatTime(lifetime.end)
		if end := secret.GetEndDateTime(); end != nil {
			endText = formatTime(*end)
		}
		fmt.Fprintf(&contents, "Secret description:      %s\nSecret ID:               %s\nSecret value:            %s\nSecret starts:           %s\nSecret expires:          %s\n",
			description, key, *text, startText, endText)
	} else {
		fmt.Fprintln(out, "Skipping client secret (pass --create-secret to create one)")
	}
	fmt.Fprintf(&contents, "Redirect URIs (SPA):     %s\n", strings.Join(redirects, "\n                         "))
	text := contents.String()
	if runtime.GOOS == "windows" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	path, err := report.Write(env.ReportDir, name, text)
	if err != nil {
		fmt.Fprintf(env.Stderr, "Could not write the output file: %v\n", err)
		fmt.Fprintln(env.Stderr, "Printing the values here instead so they are not lost:")
		fmt.Fprintln(out, text)
		return err
	}
	fmt.Fprintf(out, "Wrote details to %s\n", path)
	if o.CreateSecret {
		fmt.Fprintln(out, "This file contains the client secret in plain text. Keep it out of source control.")
	}
	return invalidSecret
}
