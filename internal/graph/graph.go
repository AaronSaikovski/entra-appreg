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

const (
	graphBase  = "https://graph.microsoft.com/v1.0"
	graphScope = "https://graph.microsoft.com/.default"
)

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
	u, err := url.Parse(base)
	if err != nil || !trustedBase(u) {
		return nil, errors.New("Invalid Microsoft Graph base URL")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""

	auth, err := msgraphauth.NewAzureIdentityAuthenticationProviderWithScopesAndValidHosts(
		cred, []string{graphScope}, []string{u.Hostname()})
	if err != nil {
		return nil, fmt.Errorf("Could not initialize Microsoft Graph client: %w", err)
	}
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		// Only these two middlewares: Kiota's retry, redirect, and compression
		// handlers are intentionally omitted.
		Transport: khttp.NewCustomTransport(
			khttp.NewParametersNameDecodingHandler(),
			&statusHandler{applicationsPath: u.Path + "/applications"},
		),
	}
	adapter, err := msgraphsdkgo.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
		auth, nil, nil, httpClient)
	if err != nil {
		return nil, fmt.Errorf("Could not initialize Microsoft Graph client: %w", err)
	}
	// The service client copies the adapter's base URL at construction time.
	adapter.SetBaseUrl(u.String())
	return &Client{sdk: msgraphsdkgo.NewGraphServiceClient(adapter), base: u}, nil
}

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

// IsNotFound reports whether err carries a Graph HTTP 404 response.
func IsNotFound(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && status.Status == http.StatusNotFound
}

// describe adds request context to a failed SDK call. Graph status errors are
// returned unchanged so their raw body and guidance stay intact.
func describe(method, path string, err error) error {
	var status *StatusError
	if errors.As(err, &status) {
		return status
	}
	var transport *url.Error
	if errors.As(err, &transport) {
		return fmt.Errorf("%s %s failed: %w", method, path, err)
	}
	return fmt.Errorf("%s %s returned an invalid response: %w", method, path, err)
}

// pathOf renders the request path (and query) for error messages. Query
// parameter names are shown decoded, as ParametersNameDecodingHandler sends them.
func pathOf(info *abs.RequestInformation, err error) string {
	if err != nil {
		return "(invalid request)"
	}
	u, err := info.GetUri()
	if err != nil {
		return "(invalid request)"
	}
	u.RawQuery = strings.ReplaceAll(u.RawQuery, "%24", "$")
	return requestPath(u)
}

func requestPath(u *url.URL) string {
	if u.RawQuery != "" {
		return u.EscapedPath() + "?" + u.RawQuery
	}
	return u.EscapedPath()
}

func requireError(field string) error {
	return fmt.Errorf("Graph response requires an object with string '%s'", field)
}

func requireApplication(app models.Applicationable) error {
	if app == nil || app.GetId() == nil {
		return requireError("id")
	}
	if app.GetAppId() == nil {
		return requireError("appId")
	}
	return nil
}

// GetApplication looks up an application by object ID, falling back to the
// appId alternate key only when the first lookup is a 404. Two 404s return nil, nil.
func (c *Client) GetApplication(ctx context.Context, ref string) (models.Applicationable, error) {
	byID := c.sdk.Applications().ByApplicationId(ref)
	app, err := byID.Get(ctx, nil)
	if err != nil {
		if !IsNotFound(err) {
			return nil, describe(http.MethodGet, pathOf(byID.ToGetRequestInformation(ctx, nil)), err)
		}
		quoted := strings.ReplaceAll(ref, "'", "''")
		byAppID := c.sdk.ApplicationsWithAppId(&quoted)
		app, err = byAppID.Get(ctx, nil)
		if err != nil {
			if IsNotFound(err) {
				return nil, nil
			}
			return nil, describe(http.MethodGet, pathOf(byAppID.ToGetRequestInformation(ctx, nil)), err)
		}
	}
	if err := requireApplication(app); err != nil {
		return nil, err
	}
	return app, nil
}

// Organization returns the signed-in tenant's organization collection.
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
	if orgs == nil {
		return nil, errors.New("Graph must return exactly one tenant organization.")
	}
	return orgs, nil
}

// ApplicationPages lists applications, calling visit for each page. Every
// next-page link is validated against the base URL and a repeated link stops
// the walk with an error.
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
	u, err := info.GetUri()
	if err != nil {
		return fmt.Errorf("Invalid Microsoft Graph request URL: %w", err)
	}
	next := u.String()
	seen := map[string]bool{}
	for {
		if seen[next] {
			return errors.New("Graph returned a repeated next-page link; stopping.")
		}
		seen[next] = true
		page, err := c.sdk.Applications().WithUrl(next).Get(ctx, nil)
		if err != nil {
			path := next
			if parsed, perr := url.Parse(next); perr == nil {
				parsed.RawQuery = strings.ReplaceAll(parsed.RawQuery, "%24", "$")
				path = requestPath(parsed)
			}
			return describe(http.MethodGet, path, err)
		}
		if page == nil {
			return errors.New("Graph returned an invalid application list.")
		}
		link := ""
		if raw := page.GetOdataNextLink(); raw != nil && *raw != "" {
			link, err = c.ValidateNextLink(*raw)
			if err != nil {
				return err
			}
			if seen[link] {
				return errors.New("Graph returned a repeated next-page link; stopping.")
			}
		}
		stop, err := visit(page, link != "")
		if err != nil {
			return err
		}
		if stop || link == "" {
			return nil
		}
		next = link
	}
}

// ValidateNextLink accepts only links on the client's base origin (same scheme,
// host, and effective port) beneath the base path, with no credentials or
// fragment. It returns the normalized link to request.
func (c *Client) ValidateNextLink(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return "", errors.New("Graph returned an invalid next-page link")
	}
	// Remove dot segments so "/v1.0/../beta" cannot pass the prefix check.
	u = c.base.ResolveReference(u)
	if u.Scheme != c.base.Scheme ||
		!strings.EqualFold(u.Hostname(), c.base.Hostname()) ||
		effectivePort(u) != effectivePort(c.base) ||
		u.User != nil ||
		strings.ContainsRune(raw, '#') ||
		!strings.HasPrefix(u.EscapedPath(), c.base.EscapedPath()+"/") {
		return "", errors.New("Graph returned an untrusted next-page link")
	}
	return u.String(), nil
}

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
	if err := requireApplication(created); err != nil {
		return nil, err
	}
	return created, nil
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

// AddPassword adds a client secret and requires a reply body.
func (c *Client) AddPassword(ctx context.Context, objectID string, cred models.PasswordCredentialable) (models.PasswordCredentialable, error) {
	body := applications.NewItemAddPasswordPostRequestBody()
	body.SetPasswordCredential(cred)
	builder := c.sdk.Applications().ByApplicationId(objectID).AddPassword()
	secret, err := builder.Post(ctx, body, nil)
	if err != nil {
		return nil, describe(http.MethodPost, pathOf(builder.ToPostRequestInformation(ctx, body, nil)), err)
	}
	if secret == nil {
		return nil, requireError("secretText")
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
