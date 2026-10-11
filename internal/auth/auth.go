// Package auth acquires a Microsoft Graph token through the Azure CLI. Each
// command starts exactly one az process: the azidentity AzureCLICredential's
// token request. The tenant is the active az context's tenant; callers that
// need its ID read it from Graph with the same token.
package auth

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

// User-facing failure messages. They are fixed text by design: SDK and
// process errors can carry az stderr, account details or tokens, so they are
// replaced, never wrapped.
const (
	msgNotFound = "Azure CLI was not found. Install Azure CLI 2.54.0 or newer, then run 'az login'."
	msgInit     = "Could not initialize Azure CLI authentication."
	// azidentity reports signed-out and expired sessions with the same
	// AuthenticationFailedError; telling them apart would mean parsing az
	// stderr, which must never be surfaced.
	msgNoToken    = "No usable Azure CLI login could provide a Microsoft Graph token. Run 'az login' (or 'az login --tenant <tenant-id>') for the intended tenant, then retry. If already signed in, check 'az account show'; Azure CLI 2.54.0 or newer is required."
	msgEmptyToken = "Azure CLI did not return a nonempty access token."
	msgTimeout    = "Azure CLI authentication timed out."
)

// authDeadline bounds the whole authentication, including the az process.
const authDeadline = 10 * time.Second

// Session carries the acquired credential.
type Session struct {
	Credential azcore.TokenCredential
}

// lookPathFunc locates a program without running it.
type lookPathFunc func(file string) (string, error)

type credentialFunc func(*azidentity.AzureCLICredentialOptions) (azcore.TokenCredential, error)

// Authenticate checks that az is installed and acquires one Graph token for
// the active az context, all within authDeadline.
func Authenticate(ctx context.Context) (Session, error) {
	return authenticateWith(ctx, exec.LookPath, newCLICredential, authDeadline)
}

func newCLICredential(o *azidentity.AzureCLICredentialOptions) (azcore.TokenCredential, error) {
	return azidentity.NewAzureCLICredential(o)
}

// authenticateWith runs the lookup and token request under one deadline.
// Once the deadline has passed, the result is a timeout whatever the steps
// returned: a cancelled step fails with its own message, and a token that
// arrives late is discarded.
func authenticateWith(ctx context.Context, lookPath lookPathFunc, newCred credentialFunc, deadline time.Duration) (Session, error) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	session, err := acquire(ctx, lookPath, newCred)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Session{}, errors.New(msgTimeout)
	}
	return session, err
}

// acquire checks for az on PATH (LookPath honors PATHEXT on Windows, so
// az.cmd is found), then requests a Graph token from az.
func acquire(ctx context.Context, lookPath lookPathFunc, newCred credentialFunc) (Session, error) {
	if _, err := lookPath("az"); err != nil {
		return Session{}, errors.New(msgNotFound)
	}
	cred, err := newCred(&azidentity.AzureCLICredentialOptions{})
	if err != nil {
		return Session{}, errors.New(msgInit)
	}
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graph.Scope}})
	if err != nil {
		return Session{}, errors.New(msgNoToken)
	}
	if strings.TrimSpace(token.Token) == "" {
		return Session{}, errors.New(msgEmptyToken)
	}
	return Session{Credential: staticCredential{token: token}}, nil
}

// staticCredential returns the token acquired under the auth deadline.
// AzureCLICredential has no cache and would spawn az for every Graph request,
// outside that deadline.
type staticCredential struct {
	token azcore.AccessToken
}

// redactedCredential is what fmt prints for a staticCredential, so a stray
// %v or %#v can never log the token.
const redactedCredential = "auth.staticCredential{redacted}"

// GetToken returns the stored token regardless of the request options.
func (c staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return c.token, nil
}

func (staticCredential) String() string   { return redactedCredential }
func (staticCredential) GoString() string { return redactedCredential }
