// Package auth acquires a Microsoft Graph token through the Azure CLI.
package auth

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"

	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

// User-facing failure messages. They are fixed text by design: SDK and
// process errors can carry az stderr, account details or tokens, so they are
// replaced, never wrapped.
const (
	msgNotFound      = "Azure CLI was not found. Install Azure CLI 2.54.0 or newer, then run 'az login'."
	msgCouldNotRun   = "Could not run Azure CLI to check login status. Check your Azure CLI installation and run 'az account show'."
	msgNoLogin       = "No usable Azure CLI login was found. Run 'az login' (or 'az login --tenant <tenant-id>'), then retry. If already signed in, check 'az account show'."
	msgInvalidTenant = "Azure CLI returned an invalid tenant ID; expected a UUID."
	msgInit          = "Could not initialize Azure CLI authentication."
	msgToken         = "Azure CLI has an account, but could not acquire a Microsoft Graph token. Your session may have expired or require reauthentication. Run 'az login' for the intended tenant, then retry; Azure CLI 2.54.0 or newer is required."
	msgEmptyToken    = "Azure CLI did not return a nonempty access token."
	msgTimeout       = "Azure CLI authentication timed out."
)

// authDeadline bounds tenant discovery and token acquisition together.
const authDeadline = 10 * time.Second

// Session carries the acquired credential. TenantID is the discovered tenant
// when the token was pinned to it, and "" otherwise.
type Session struct {
	Credential azcore.TokenCredential
	TenantID   string
}

// runFunc runs a program and returns its stdout.
type runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

type credentialFunc func(*azidentity.AzureCLICredentialOptions) (azcore.TokenCredential, error)

// Authenticate checks the Azure CLI login and acquires one Graph token, all
// within authDeadline. With pinTenant the token is requested for the tenant
// az reports, so the tenant ID that create records matches the token's tenant.
func Authenticate(ctx context.Context, pinTenant bool) (Session, error) {
	return authenticateWith(ctx, pinTenant, runAz, newCLICredential, authDeadline)
}

// runAz returns stdout only. Stderr is discarded rather than left nil,
// because Output would otherwise capture it into the returned *ExitError.
func runAz(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// Bound the wait for pipe copying if az leaves a child holding stdout.
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Stderr = io.Discard
	return cmd.Output()
}

func newCLICredential(o *azidentity.AzureCLICredentialOptions) (azcore.TokenCredential, error) {
	return azidentity.NewAzureCLICredential(o)
}

func tenantCommand() (string, []string) {
	// No user-controlled values enter this command, including on Windows where
	// Azure CLI is normally installed as az.cmd.
	args := []string{"account", "show", "--query", "tenantId", "--output", "tsv", "--only-show-errors"}
	if runtime.GOOS == "windows" {
		return "cmd.exe", append([]string{"/d", "/c", "az"}, args...)
	}
	return "az", args
}

// currentTenant returns the normalized tenant ID of the active az login.
func currentTenant(ctx context.Context, run runFunc) (string, error) {
	name, args := tenantCommand()
	out, err := run(ctx, name, args...)
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr):
			return "", errors.New(msgNoLogin)
		case errors.Is(err, exec.ErrNotFound):
			return "", errors.New(msgNotFound)
		default:
			return "", errors.New(msgCouldNotRun)
		}
	}
	// uuid.Parse ignores the outer bytes of the 38-byte "{...}" form, so it
	// alone would accept non-UTF-8 output.
	if !utf8.Valid(out) {
		return "", errors.New(msgInvalidTenant)
	}
	id, err := uuid.Parse(strings.TrimSpace(string(out)))
	if err != nil {
		return "", errors.New(msgInvalidTenant)
	}
	return id.String(), nil
}

// authenticateWith runs the login check and token request under one deadline.
// Once the deadline has passed, the result is a timeout whatever the steps
// returned: a cancelled step fails with its own message, and a token that
// arrives late is discarded.
func authenticateWith(ctx context.Context, pinTenant bool, run runFunc, newCred credentialFunc, deadline time.Duration) (Session, error) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	session, err := acquire(ctx, pinTenant, run, newCred)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Session{}, errors.New(msgTimeout)
	}
	return session, err
}

// acquire discovers the tenant, then requests a Graph token from az.
func acquire(ctx context.Context, pinTenant bool, run runFunc, newCred credentialFunc) (Session, error) {
	tenant, err := currentTenant(ctx, run)
	if err != nil {
		return Session{}, err
	}
	var opts azidentity.AzureCLICredentialOptions
	if pinTenant {
		opts.TenantID = tenant
	}
	cred, err := newCred(&opts)
	if err != nil {
		return Session{}, errors.New(msgInit)
	}
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graph.Scope}})
	if err != nil {
		return Session{}, errors.New(msgToken)
	}
	if strings.TrimSpace(token.Token) == "" {
		return Session{}, errors.New(msgEmptyToken)
	}
	return Session{Credential: staticCredential{token: token}, TenantID: opts.TenantID}, nil
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
