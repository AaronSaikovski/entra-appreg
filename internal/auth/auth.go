// Package auth acquires a Microsoft Graph token through the Azure CLI.
package auth

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"
)

const graphScope = "https://graph.microsoft.com/.default"

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

// Context carries the acquired credential. TenantID is "" unless pinned.
type Context struct {
	Credential azcore.TokenCredential
	TenantID   string
}

type runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

type credentialFunc func(*azidentity.AzureCLICredentialOptions) (azcore.TokenCredential, error)

// Authenticate checks the Azure CLI login and acquires one Graph token.
func Authenticate(ctx context.Context, pinTenant bool) (Context, error) {
	return authenticateWith(ctx, pinTenant, runAz, newCLICredential, 10*time.Second)
}

// runAz returns stdout only; stderr is discarded so it can never leak.
func runAz(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Stderr = nil
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
	if !utf8.Valid(out) {
		return "", errors.New(msgInvalidTenant)
	}
	id, err := uuid.Parse(strings.TrimSpace(string(out)))
	if err != nil {
		return "", errors.New(msgInvalidTenant)
	}
	return id.String(), nil
}

func authenticateWith(ctx context.Context, pinTenant bool, run runFunc, newCred credentialFunc, deadline time.Duration) (Context, error) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	fail := func(msg string) (Context, error) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Context{}, errors.New(msgTimeout)
		}
		return Context{}, errors.New(msg)
	}

	tenant, err := currentTenant(ctx, run)
	if err != nil {
		return fail(err.Error())
	}
	opts := &azidentity.AzureCLICredentialOptions{}
	pinned := ""
	if pinTenant {
		pinned = tenant
		opts.TenantID = tenant
	}
	cred, err := newCred(opts)
	if err != nil {
		return fail(msgInit)
	}
	// SDK errors may contain subprocess output; replace rather than wrap them.
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graphScope}})
	if err != nil {
		return fail(msgToken)
	}
	if strings.TrimSpace(token.Token) == "" {
		return fail(msgEmptyToken)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Context{}, errors.New(msgTimeout)
	}
	return Context{Credential: staticCredential{token: token}, TenantID: pinned}, nil
}

// staticCredential returns the token acquired under the auth deadline.
// AzureCLICredential has no cache and would spawn az for every Graph request.
type staticCredential struct {
	token azcore.AccessToken
}

func (c staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return c.token, nil
}

func (staticCredential) String() string   { return "auth.staticCredential{redacted}" }
func (staticCredential) GoString() string { return "auth.staticCredential{redacted}" }
