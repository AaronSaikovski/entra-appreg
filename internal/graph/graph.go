// Package graph is the Microsoft Graph v1.0 client used by entra-appreg. It wraps
// the typed msgraph-sdk-go client with a hardened HTTP pipeline: a 10-second
// timeout, no redirects, no retries, no request compression, and raw Graph error
// bodies preserved in StatusError.
package graph

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	abs "github.com/microsoft/kiota-abstractions-go"
	khttp "github.com/microsoft/kiota-http-go"
	msgraphsdkgo "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphauth "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/organization"
)

const graphBase = "https://graph.microsoft.com/v1.0"

// Scope is the OAuth scope requested for Microsoft Graph tokens.
const Scope = "https://graph.microsoft.com/.default"

// errRepeatedNextLink stops a page walk that would revisit a page.
var errRepeatedNextLink = errors.New("Graph returned a repeated next-page link; stopping.")

// Client is a Microsoft Graph client bound to one trusted base URL.
type Client struct {
	sdk  *msgraphsdkgo.GraphServiceClient
	base *url.URL
}

// New returns a client for the public Microsoft Graph v1.0 endpoint.
func New(cred azcore.TokenCredential) (*Client, error) {
	return NewForBase(cred, graphBase)
}

// NewForBase is the offline construction seam used by tests. It accepts only the
// public Graph origin or a plain-http loopback IP literal. There is deliberately
// no command-line or environment endpoint override.
func NewForBase(cred azcore.TokenCredential, base string) (*Client, error) {
	u, err := parseBase(base)
	if err != nil {
		return nil, err
	}
	auth, err := msgraphauth.NewAzureIdentityAuthenticationProviderWithScopesAndValidHosts(
		cred, []string{Scope}, []string{u.Hostname()})
	if err != nil {
		return nil, fmt.Errorf("Could not initialize Microsoft Graph client: %w", err)
	}
	adapter, err := msgraphsdkgo.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
		auth, nil, nil, newHTTPClient(u.Path+"/applications"))
	if err != nil {
		return nil, fmt.Errorf("Could not initialize Microsoft Graph client: %w", err)
	}
	// Set before NewGraphServiceClient, which copies the adapter's base URL.
	adapter.SetBaseUrl(u.String())
	return &Client{sdk: msgraphsdkgo.NewGraphServiceClient(adapter), base: u}, nil
}

// parseBase parses a trusted base URL and normalizes it: lowercase host, no
// trailing slash, so request and next-link paths compare by simple prefix.
func parseBase(base string) (*url.URL, error) {
	u, err := url.Parse(base)
	if err != nil || !trustedBase(u) {
		return nil, errors.New("Invalid Microsoft Graph base URL")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// newHTTPClient builds the hardened transport. Only these two middlewares are
// installed: Kiota's retry, redirect, and compression handlers are omitted, so
// a request is sent at most once, never followed, and never gzip-encoded.
func newHTTPClient(applicationsPath string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: khttp.NewCustomTransport(
			khttp.NewParametersNameDecodingHandler(),
			&statusHandler{applicationsPath: applicationsPath},
		),
	}
}

// trustedBase accepts only the public Graph origin over https, or plain http to
// a loopback IP literal (the offline test fixture), with no userinfo, query, or fragment.
func trustedBase(u *url.URL) bool {
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		port := u.Port()
		return strings.EqualFold(u.Hostname(), "graph.microsoft.com") && (port == "" || port == "443")
	case "http":
		ip := net.ParseIP(u.Hostname())
		return ip != nil && ip.IsLoopback()
	default:
		return false
	}
}

// isNotFound reports whether err carries a Graph HTTP 404 response.
func isNotFound(err error) bool {
	status, ok := errors.AsType[*StatusError](err)
	return ok && status.Status == http.StatusNotFound
}

// describe adds request context to a failed SDK call. Graph status errors are
// returned unchanged so their raw body and guidance stay intact; transport
// failures arrive from net/http as *url.Error, and anything else is a reply the
// SDK could not deserialize.
func describe(method, path string, err error) error {
	if status, ok := errors.AsType[*StatusError](err); ok {
		return status
	}
	if _, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("%s %s failed: %w", method, path, err)
	}
	return fmt.Errorf("%s %s returned an invalid response: %w", method, path, err)
}

// pathOf renders a request's path and query for describe. It takes the
// (info, err) pair returned by the SDK's To…RequestInformation builders so
// callers can pass that call directly.
func pathOf(info *abs.RequestInformation, err error) string {
	if err != nil {
		return "(invalid request)"
	}
	u, err := info.GetUri()
	if err != nil {
		return "(invalid request)"
	}
	return displayPath(u)
}

// displayPath renders u's path and query, with "$" query-name prefixes
// decoded as ParametersNameDecodingHandler sends them.
func displayPath(u *url.URL) string {
	decoded := *u
	decoded.RawQuery = strings.ReplaceAll(u.RawQuery, "%24", "$")
	return requestPath(&decoded)
}

// requestPath renders u's escaped path plus "?" and the raw query when present.
func requestPath(u *url.URL) string {
	if u.RawQuery != "" {
		return u.EscapedPath() + "?" + u.RawQuery
	}
	return u.EscapedPath()
}

// MissingFieldError reports a Graph reply that lacks a required string field.
func MissingFieldError(field string) error {
	return fmt.Errorf("Graph response requires an object with string '%s'", field)
}

// requireApplication returns app when Graph's reply carried both id and appId,
// which every caller needs to address the application afterwards.
func requireApplication(app models.Applicationable) (models.Applicationable, error) {
	if app == nil || app.GetId() == nil {
		return nil, MissingFieldError("id")
	}
	if app.GetAppId() == nil {
		return nil, MissingFieldError("appId")
	}
	return app, nil
}

// GetApplication looks up an application by object ID, falling back to the
// appId alternate key only when the first lookup is a 404. Two 404s return nil, nil.
func (c *Client) GetApplication(ctx context.Context, ref string) (models.Applicationable, error) {
	byID := c.sdk.Applications().ByApplicationId(ref)
	app, err := byID.Get(ctx, nil)
	if isNotFound(err) {
		return c.getApplicationByAppID(ctx, ref)
	}
	if err != nil {
		return nil, describe(http.MethodGet, pathOf(byID.ToGetRequestInformation(ctx, nil)), err)
	}
	return requireApplication(app)
}

// getApplicationByAppID looks up the appId alternate key; a 404 returns nil, nil.
func (c *Client) getApplicationByAppID(ctx context.Context, appID string) (models.Applicationable, error) {
	quoted := strings.ReplaceAll(appID, "'", "''") // OData string literal escaping
	builder := c.sdk.ApplicationsWithAppId(&quoted)
	app, err := builder.Get(ctx, nil)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, describe(http.MethodGet, pathOf(builder.ToGetRequestInformation(ctx, nil)), err)
	}
	return requireApplication(app)
}

// Organization returns the signed-in tenant's organization collection. The
// result may be nil when Graph sends an empty body; callers validate it.
func (c *Client) Organization(ctx context.Context) (models.OrganizationCollectionResponseable, error) {
	builder := c.sdk.Organization()
	config := &organization.OrganizationRequestBuilderGetRequestConfiguration{
		QueryParameters: &organization.OrganizationRequestBuilderGetQueryParameters{
			Select: []string{"id", "displayName"},
		},
	}
	orgs, err := builder.Get(ctx, config)
	if err != nil {
		return nil, describe(http.MethodGet, pathOf(builder.ToGetRequestInformation(ctx, config)), err)
	}
	return orgs, nil
}

// ApplicationPages lists applications, calling visit for each page until visit
// stops or there is no next page. Every next-page link is validated against the
// base URL before it is requested, and a link to an already-requested page
// stops the walk with an error.
func (c *Client) ApplicationPages(
	ctx context.Context,
	q *applications.ApplicationsRequestBuilderGetQueryParameters,
	visit func(page models.ApplicationCollectionResponseable, hasNext bool) (stop bool, err error),
) error {
	info, err := c.sdk.Applications().ToGetRequestInformation(ctx,
		&applications.ApplicationsRequestBuilderGetRequestConfiguration{QueryParameters: q})
	if err != nil {
		return fmt.Errorf("Invalid Microsoft Graph request URL: %w", err)
	}
	next, err := info.GetUri()
	if err != nil {
		return fmt.Errorf("Invalid Microsoft Graph request URL: %w", err)
	}
	// Each link is checked against seen before it becomes next, so the loop
	// itself never revisits a page.
	seen := map[string]bool{}
	for next != nil {
		seen[next.String()] = true
		page, err := c.applicationPage(ctx, next)
		if err != nil {
			return err
		}
		link, err := c.nextLink(page)
		if err != nil {
			return err
		}
		if link != nil && seen[link.String()] {
			return errRepeatedNextLink
		}
		stop, err := visit(page, link != nil)
		if err != nil || stop {
			return err
		}
		next = link
	}
	return nil
}

// applicationPage fetches one application collection page from u.
func (c *Client) applicationPage(ctx context.Context, u *url.URL) (models.ApplicationCollectionResponseable, error) {
	page, err := c.sdk.Applications().WithUrl(u.String()).Get(ctx, nil)
	if err != nil {
		return nil, describe(http.MethodGet, displayPath(u), err)
	}
	if page == nil {
		return nil, errors.New("Graph returned an invalid application list.")
	}
	return page, nil
}

// nextLink returns page's validated next-page link, or nil on the last page.
func (c *Client) nextLink(page models.ApplicationCollectionResponseable) (*url.URL, error) {
	raw := page.GetOdataNextLink()
	if raw == nil || *raw == "" {
		return nil, nil
	}
	return c.validateNextLink(*raw)
}

// validateNextLink accepts only links on the client's base origin (same scheme,
// host, and effective port) beneath the base path, with no credentials or
// fragment. It returns the dot-segment-normalized link to request.
func (c *Client) validateNextLink(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return nil, errors.New("Graph returned an invalid next-page link")
	}
	// Remove dot segments so "/v1.0/../beta" cannot pass the prefix check.
	u = c.base.ResolveReference(u)
	if u.Scheme != c.base.Scheme ||
		!strings.EqualFold(u.Hostname(), c.base.Hostname()) ||
		effectivePort(u) != effectivePort(c.base) ||
		u.User != nil ||
		strings.ContainsRune(raw, '#') || // also rejects an empty fragment
		!strings.HasPrefix(u.EscapedPath(), c.base.EscapedPath()+"/") {
		return nil, errors.New("Graph returned an untrusted next-page link")
	}
	return u, nil
}

// effectivePort returns u's explicit port or its scheme's default, so
// "https://host" and "https://host:443" compare equal.
func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch u.Scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// CreateApplication POSTs a new application and requires id and appId in the reply.
func (c *Client) CreateApplication(ctx context.Context, app models.Applicationable) (models.Applicationable, error) {
	builder := c.sdk.Applications()
	created, err := builder.Post(ctx, app, nil)
	if err != nil {
		return nil, describe(http.MethodPost, pathOf(builder.ToPostRequestInformation(ctx, app, nil)), err)
	}
	return requireApplication(created)
}

// PatchApplication PATCHes an application; an empty success reply is fine.
//
// Backing-store rule: msgraph-sdk-go enables a backing store, so a model fetched
// from Graph serializes only the fields changed after the fetch. PATCH bodies
// must be built from fresh models.New…() objects, never from mutated fetched models.
func (c *Client) PatchApplication(ctx context.Context, objectID string, app models.Applicationable) error {
	builder := c.sdk.Applications().ByApplicationId(objectID)
	if _, err := builder.Patch(ctx, app, nil); err != nil {
		return describe(http.MethodPatch, pathOf(builder.ToPatchRequestInformation(ctx, app, nil)), err)
	}
	return nil
}

// AddPassword adds a client secret and requires a reply body, since the reply
// is the only place Graph ever returns the secret text.
func (c *Client) AddPassword(ctx context.Context, objectID string, cred models.PasswordCredentialable) (models.PasswordCredentialable, error) {
	body := applications.NewItemAddPasswordPostRequestBody()
	body.SetPasswordCredential(cred)
	builder := c.sdk.Applications().ByApplicationId(objectID).AddPassword()
	secret, err := builder.Post(ctx, body, nil)
	if err != nil {
		return nil, describe(http.MethodPost, pathOf(builder.ToPostRequestInformation(ctx, body, nil)), err)
	}
	if secret == nil {
		return nil, MissingFieldError("secretText")
	}
	return secret, nil
}

// DeleteApplication deletes an application by object ID.
func (c *Client) DeleteApplication(ctx context.Context, objectID string) error {
	builder := c.sdk.Applications().ByApplicationId(objectID)
	if err := builder.Delete(ctx, nil); err != nil {
		return describe(http.MethodDelete, pathOf(builder.ToDeleteRequestInformation(ctx, nil)), err)
	}
	return nil
}
