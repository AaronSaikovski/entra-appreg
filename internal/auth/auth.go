// Package auth acquires access tokens through the Azure CLI. Each command
// starts one az process for its Microsoft Graph token; create starts a second
// one, for an Azure Key Vault token, only when --keyvault-uri is given. The
// tenant is the active az context's tenant; callers that need its ID read it
// from Graph with the same token.
package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/AaronSaikovski/entra-appreg/internal/graph"
	"github.com/AaronSaikovski/entra-appreg/internal/keyvault"
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
	msgNoToken         = "No usable Azure CLI login could provide a Microsoft Graph token. Run 'az login' (or 'az login --tenant <tenant-id>') for the intended tenant, then retry. If already signed in, check 'az account show'; Azure CLI 2.54.0 or newer is required."
	msgNoKeyVaultToken = "No usable Azure CLI login could provide an Azure Key Vault token. Check 'az account show' and that the account can sign in to Key Vault, then retry."
	msgEmptyToken      = "Azure CLI did not return a nonempty access token."
	msgTimeout         = "Azure CLI authentication timed out."
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

// KeyVault checks that az is installed and acquires one Azure Key Vault token
// for the active az context, within authDeadline. It starts a second az
// process, so callers request it only when a vault is used.
func KeyVault(ctx context.Context) (azcore.TokenCredential, error) {
	return tokenWith(ctx, exec.LookPath, newCLICredential, authDeadline, keyvault.Scope, msgNoKeyVaultToken)
}

// authenticateWith acquires the Graph token under deadline.
func authenticateWith(ctx context.Context, lookPath lookPathFunc, newCred credentialFunc, deadline time.Duration) (Session, error) {
	cred, err := tokenWith(ctx, lookPath, newCred, deadline, graph.Scope, msgNoToken)
	if err != nil {
		return Session{}, err
	}
	return Session{Credential: cred}, nil
}

// tokenWith runs the lookup and token request under one deadline. Once the
// deadline has passed, the result is a timeout whatever the steps returned:
// a cancelled step fails with its own message, and a token that arrives late
// is discarded.
func tokenWith(ctx context.Context, lookPath lookPathFunc, newCred credentialFunc, deadline time.Duration, scope, noToken string) (azcore.TokenCredential, error) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	cred, err := acquire(ctx, lookPath, newCred, scope, noToken)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, errors.New(msgTimeout)
	}
	return cred, err
}

// acquire checks for az on PATH (LookPath honors PATHEXT on Windows, so
// az.cmd is found), then requests a token for scope from az.
func acquire(ctx context.Context, lookPath lookPathFunc, newCred credentialFunc, scope, noToken string) (azcore.TokenCredential, error) {
	if _, err := lookPath("az"); err != nil {
		return nil, errors.New(msgNotFound)
	}
	cred, err := newCred(&azidentity.AzureCLICredentialOptions{})
	if err != nil {
		return nil, errors.New(msgInit)
	}
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return nil, errors.New(noToken)
	}
	if strings.TrimSpace(token.Token) == "" {
		return nil, errors.New(msgEmptyToken)
	}
	return staticCredential{token: token}, nil
}

// staticCredential returns the token acquired under the auth deadline.
// AzureCLICredential has no cache and would spawn az for every Graph request,
// outside that deadline.
type staticCredential struct {
	token azcore.AccessToken
}

// redactedCredential is what fmt prints for a staticCredential. Format
// implements fmt.Formatter, so no fmt verb can print the token.
const redactedCredential = "auth.staticCredential{redacted}"

// GetToken returns the stored token regardless of the request options.
func (c staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return c.token, nil
}

func (staticCredential) String() string { return redactedCredential }

// Format writes redactedCredential for every verb and flag.
func (staticCredential) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, redactedCredential) }
