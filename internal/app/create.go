package app

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// createPlan is the validated input of a create run.
type createPlan struct {
	name      string
	audience  string
	redirects []string        // deduplicated in first-seen order; never nil, so POST sends []
	scope     *scopeSpec      // nil = no scope requested
	lifetime  *secretLifetime // nil = no secret requested
}

// needsV2 reports whether the audience includes personal Microsoft accounts,
// which Graph accepts only with v2 access tokens.
func (p createPlan) needsV2() bool {
	return p.audience == "AzureADandPersonalMicrosoftAccount" || p.audience == "PersonalMicrosoftAccount"
}

// exposedScope is the scope added after creation, as recorded in the report.
type exposedScope struct {
	identifier string
	fullName   string
	id         uuid.UUID
	spec       scopeSpec
}

// createdSecret is the addPassword result, as recorded in the report.
type createdSecret struct {
	description string
	keyID       string
	value       string
	starts      string
	expires     string
	keyMissing  bool // Graph omitted keyId; reported after the secret is saved
}

// create registers a new application. Steps run in a fixed order: optional
// --appid lookup, input validation, duplicate-name check, tenant lookup, POST,
// scope PATCH, addPassword, report. IDs are printed as soon as the POST
// succeeds so they survive any later failure.
func create(ctx context.Context, o *cli.Options, g *graph.Client, env Env) error {
	out := env.Stdout
	if o.AppID != nil && *o.AppID != "no-id" {
		exists, err := applicationExists(ctx, g, out, *o.AppID)
		if err != nil || exists {
			return err
		}
	}
	plan, err := planCreate(o, env.Now())
	if err != nil {
		return err
	}
	if err := checkNameAvailable(ctx, g, plan.name); err != nil {
		return err
	}
	// The token and /organization share one credential, so this is exactly
	// the token's tenant. Read before the POST so a failure mutates nothing.
	tenantID, _, err := currentTenant(ctx, g)
	if err != nil {
		return err
	}
	objectID, clientID, err := postApplication(ctx, g, out, plan)
	if err != nil {
		return err
	}

	var scope *exposedScope
	if plan.scope != nil {
		if scope, err = addScope(ctx, g, out, plan, objectID, clientID); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(out, "Skipping API scope (pass --scope-name to expose an API)")
	}
	var secret *createdSecret
	if plan.lifetime != nil {
		if secret, err = addSecret(ctx, g, out, plan, objectID); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(out, "Skipping client secret (pass --create-secret to create one)")
	}

	contents := reportContents(plan, tenantID, objectID, clientID, scope, secret)
	if err := writeReport(env, plan.name, contents, secret != nil); err != nil {
		return err
	}
	if secret != nil && secret.keyMissing {
		return errors.New("Graph's addPassword response had a missing or invalid keyId; the secret value was preserved in the report.")
	}
	return nil
}

// applicationExists looks up --appid. An existing application makes create a
// no-op; a missing one lets creation proceed.
func applicationExists(ctx context.Context, g *graph.Client, out io.Writer, reference string) (bool, error) {
	fmt.Fprintf(out, "Checking if application %s exists\n", reference)
	app, err := g.GetApplication(ctx, reference)
	if err != nil {
		return false, err
	}
	if app != nil {
		fmt.Fprintln(out, "Application already exists, not creating new one.")
		return true, nil
	}
	fmt.Fprintln(out, "Application not found")
	return false, nil
}

// planCreate validates the creation options in order: name, audience,
// redirects, scope, lifetime. Every returned error is a *cli.UsageError.
func planCreate(o *cli.Options, now time.Time) (createPlan, error) {
	if o.Name == nil || strings.TrimSpace(*o.Name) == "" {
		return createPlan{}, cli.Usagef("--name is required when creating an application.")
	}
	plan := createPlan{name: *o.Name}
	var err error
	if plan.audience, err = resolveAudience(o.Audience); err != nil {
		return createPlan{}, err
	}
	if plan.redirects, err = resolveRedirects(o.RedirectURIs); err != nil {
		return createPlan{}, err
	}
	if plan.scope, err = resolveOptionalScope(o, plan.name); err != nil {
		return createPlan{}, err
	}
	if plan.lifetime, err = resolveLifetime(o, now); err != nil {
		return createPlan{}, err
	}
	return plan, nil
}

// resolveAudience returns the canonical spelling of --audience, matched
// case-insensitively. The default is AzureADMyOrg.
func resolveAudience(value *string) (string, error) {
	audience := trimmedOr(value, "AzureADMyOrg")
	for _, a := range audiences {
		if cli.EqualFoldASCII(a, audience) {
			return a, nil
		}
	}
	return "", cli.Usagef("--audience must be one of %s, got: %s", strings.Join(audiences, ", "), audience)
}

// resolveRedirects validates every redirect URI and removes exact duplicates,
// keeping first-seen order. The result is never nil.
func resolveRedirects(uris []string) ([]string, error) {
	redirects := []string{}
	seen := map[string]bool{}
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if !validRedirect(uri) {
			return nil, cli.Usagef("--redirect-urls must contain absolute http(s) URIs, got: %s", uri)
		}
		if !seen[uri] {
			seen[uri] = true
			redirects = append(redirects, uri)
		}
	}
	return redirects, nil
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

// resolveOptionalScope returns nil when --scope-name is absent, and rejects
// any other --scope-* option given without it.
func resolveOptionalScope(o *cli.Options, appName string) (*scopeSpec, error) {
	if o.ScopeName != nil {
		spec, err := resolveScope(o, appName)
		if err != nil {
			return nil, err
		}
		return &spec, nil
	}
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
		return nil, cli.Usagef("%s require --scope-name.", strings.Join(orphaned, ", "))
	}
	return nil, nil
}

// checkNameAvailable fails with a usage error when an application with
// exactly this display name already exists, following every result page.
func checkNameAvailable(ctx context.Context, g *graph.Client, name string) error {
	query := &applications.ApplicationsRequestBuilderGetQueryParameters{
		Filter: new("displayName eq '" + strings.ReplaceAll(name, "'", "''") + "'"),
		Select: []string{"id"},
		Top:    new(int32(1)),
	}
	return g.ApplicationPages(ctx, query, func(page models.ApplicationCollectionResponseable, _ bool) (bool, error) {
		matches := page.GetValue()
		if matches == nil {
			return false, errors.New("Graph returned an invalid name-check response.")
		}
		if len(matches) > 0 {
			return false, cli.Usagef("An app registration named %q already exists in this tenant. Choose a different --name, or use --appid to reference an existing registration.", name)
		}
		return false, nil
	})
}

// postApplication creates the registration and prints its IDs immediately.
func postApplication(ctx context.Context, g *graph.Client, out io.Writer, plan createPlan) (objectID, clientID string, err error) {
	fmt.Fprintf(out, "Creating application registration '%s'\n", plan.name)
	fmt.Fprintf(out, "  Supported account types: %s\n", plan.audience)
	for _, uri := range plan.redirects {
		fmt.Fprintf(out, "  Redirect URI (SPA): %s\n", uri)
	}
	body := models.NewApplication()
	body.SetDisplayName(new(plan.name))
	body.SetSignInAudience(new(plan.audience))
	spa := models.NewSpaApplication()
	spa.SetRedirectUris(plan.redirects)
	body.SetSpa(spa)
	if plan.needsV2() {
		api := models.NewApiApplication()
		api.SetRequestedAccessTokenVersion(new(int32(2)))
		body.SetApi(api)
	}
	app, err := g.CreateApplication(ctx, body)
	if err != nil {
		return "", "", err
	}
	objectID, clientID = *app.GetId(), *app.GetAppId()
	fmt.Fprintf(out, "AUTH_APP_ID=%s\n", objectID)
	fmt.Fprintf(out, "AUTH_CLIENT_ID=%s\n", clientID)
	return objectID, clientID, nil
}

// addScope sets the api://{clientID} identifier URI and the requested scope
// on the new registration.
func addScope(ctx context.Context, g *graph.Client, out io.Writer, plan createPlan, objectID, clientID string) (*exposedScope, error) {
	identifier := "api://" + clientID
	scope := &exposedScope{
		identifier: identifier,
		fullName:   identifier + "/" + plan.scope.name,
		id:         uuid.New(),
		spec:       *plan.scope,
	}
	fmt.Fprintf(out, "Exposing API: adding scope '%s' (%s, %s)\n", scope.fullName, scope.spec.consentLabel(), scope.spec.stateLabel())
	api := models.NewApiApplication()
	api.SetOauth2PermissionScopes([]models.PermissionScopeable{buildScope(scope.id, scope.spec)})
	if plan.needsV2() {
		api.SetRequestedAccessTokenVersion(new(int32(2)))
	}
	patch := models.NewApplication()
	patch.SetIdentifierUris([]string{identifier})
	patch.SetApi(api)
	if err := g.PatchApplication(ctx, objectID, patch); err != nil {
		return nil, err
	}
	return scope, nil
}

// addSecret creates the client secret. Graph returns the secret value only
// once, so a reply missing other fields is still recorded (keyMissing) rather
// than failing before the report is written.
func addSecret(ctx context.Context, g *graph.Client, out io.Writer, plan createPlan, objectID string) (*createdSecret, error) {
	description := secretDescription(plan.name)
	fmt.Fprintf(out, "Adding client secret '%s' to %s\n", description, objectID)
	credential := models.NewPasswordCredential()
	credential.SetDisplayName(new(description))
	credential.SetEndDateTime(new(plan.lifetime.end))
	if plan.lifetime.start != nil {
		credential.SetStartDateTime(plan.lifetime.start)
	}
	reply, err := g.AddPassword(ctx, objectID, credential)
	if err != nil {
		return nil, err
	}
	value := reply.GetSecretText()
	if value == nil {
		return nil, graph.MissingFieldError("secretText")
	}
	secret := &createdSecret{
		description: description,
		keyID:       "(not returned by Graph)",
		value:       *value,
		starts:      "now",
		expires:     formatTime(plan.lifetime.end),
	}
	if id := reply.GetKeyId(); id != nil {
		secret.keyID = id.String()
	} else {
		secret.keyMissing = true
	}
	if start := reply.GetStartDateTime(); start != nil {
		secret.starts = formatTime(*start)
	}
	if end := reply.GetEndDateTime(); end != nil {
		secret.expires = formatTime(*end)
	}
	return secret, nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// reportContents renders the report with LF line endings. scope and secret
// are nil when not requested.
func reportContents(plan createPlan, tenantID, objectID, clientID string, scope *exposedScope, secret *createdSecret) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Application name:        %s\nApplication (client) ID: %s\nObject ID:               %s\nDirectory (tenant) ID:   %s\nSupported account types: %s\n",
		plan.name, clientID, objectID, tenantID, plan.audience)
	if scope != nil {
		s := scope.spec
		fmt.Fprintf(&b, "Application ID URI:      %s\nScope (full name):       %s\nScope ID:                %s\nScope display name:      %s\nScope description:       %s\nScope consent:           %s\nScope state:             %s\n",
			scope.identifier, scope.fullName, scope.id, s.displayName, s.description, s.consentLabel(), s.stateLabel())
		if s.userConsent {
			fmt.Fprintf(&b, "Scope user display name: %s\nScope user description:  %s\n", s.userDisplayName, s.userDescription)
		}
	}
	if secret != nil {
		fmt.Fprintf(&b, "Secret description:      %s\nSecret ID:               %s\nSecret value:            %s\nSecret starts:           %s\nSecret expires:          %s\n",
			secret.description, secret.keyID, secret.value, secret.starts, secret.expires)
	}
	fmt.Fprintf(&b, "Redirect URIs (SPA):     %s\n", strings.Join(plan.redirects, "\n                         "))
	return b.String()
}

// writeReport saves contents (CRLF on Windows) to a new file in
// env.ReportDir. If that fails, the contents are printed to stdout so a
// one-time secret is not lost, and the write error is returned.
func writeReport(env Env, appName, contents string, hasSecret bool) error {
	if runtime.GOOS == "windows" {
		contents = strings.ReplaceAll(contents, "\n", "\r\n")
	}
	path, err := report.Write(env.ReportDir, appName, contents)
	if err != nil {
		fmt.Fprintf(env.Stderr, "Could not write the output file: %v\n", err)
		fmt.Fprintln(env.Stderr, "Printing the values here instead so they are not lost:")
		fmt.Fprintln(env.Stdout, contents)
		return err
	}
	fmt.Fprintf(env.Stdout, "Wrote details to %s\n", path)
	if hasSecret {
		fmt.Fprintln(env.Stdout, "This file contains the client secret in plain text. Keep it out of source control.")
	}
	return nil
}
