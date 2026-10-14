package auth

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/AaronSaikovski/entra-appreg/internal/graph"
	"github.com/AaronSaikovski/entra-appreg/internal/keyvault"
)

const (
	opaqueToken   = "opaque-access-token-not-a-jwt"
	privateOutput = "private-cli-output-must-not-leak"
	timeout       = 10 * time.Second
)

type fixture struct {
	mu        sync.Mutex
	calls     []string
	notFound  bool
	cancelled bool

	credOpts   []*azidentity.AzureCLICredentialOptions
	credErr    error
	tokenErr   error
	token      string
	blockToken bool
}

func (f *fixture) lookPath(file string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "lookpath "+file)
	if f.notFound {
		return "", fmt.Errorf("%s: %w", privateOutput, exec.ErrNotFound)
	}
	return "/fake/bin/" + file, nil
}

type fakeCred struct{ f *fixture }

func (c fakeCred) GetToken(ctx context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f := c.f
	f.mu.Lock()
	f.calls = append(f.calls, "get-token "+strings.Join(o.Scopes, " "))
	f.mu.Unlock()
	if f.blockToken {
		<-ctx.Done()
		f.mu.Lock()
		f.cancelled = true
		f.mu.Unlock()
		return azcore.AccessToken{}, errors.New(privateOutput)
	}
	if f.tokenErr != nil {
		return azcore.AccessToken{}, f.tokenErr
	}
	return azcore.AccessToken{Token: f.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func (f *fixture) newCred(o *azidentity.AzureCLICredentialOptions) (azcore.TokenCredential, error) {
	f.mu.Lock()
	f.credOpts = append(f.credOpts, o)
	f.mu.Unlock()
	if f.credErr != nil {
		return nil, f.credErr
	}
	return fakeCred{f}, nil
}

func authErr(t *testing.T, f *fixture, d time.Duration) string {
	t.Helper()
	_, err := authenticateWith(t.Context(), f.lookPath, f.newCred, d)
	if err == nil {
		t.Fatal("authentication unexpectedly succeeded")
	}
	return err.Error()
}

func TestAcquiresTokenForActiveContextAcceptingOpaqueToken(t *testing.T) {
	f := &fixture{token: opaqueToken}
	s, err := authenticateWith(t.Context(), f.lookPath, f.newCred, timeout)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := s.Credential.GetToken(t.Context(), policy.TokenRequestOptions{})
	if tok.Token != opaqueToken {
		t.Fatalf("got %q", tok.Token)
	}
	if strings.Join(f.calls, "|") != "lookpath az|get-token "+graph.Scope {
		t.Fatalf("calls %v", f.calls)
	}
	if len(f.credOpts) != 1 || f.credOpts[0] == nil || f.credOpts[0].TenantID != "" {
		t.Fatalf("credential options %+v", f.credOpts)
	}
}

func TestMissingAzStopsBeforeCredential(t *testing.T) {
	f := &fixture{notFound: true}
	if e := authErr(t, f, timeout); e != msgNotFound {
		t.Fatal(e)
	}
	if len(f.calls) != 1 || len(f.credOpts) != 0 {
		t.Fatalf("calls %v opts %v", f.calls, f.credOpts)
	}
}

func TestTokenErrorYieldsOnlyFixedMessage(t *testing.T) {
	f := &fixture{tokenErr: errors.New("AzureCLICredential: " + privateOutput + " " + opaqueToken)}
	if e := authErr(t, f, timeout); e != msgNoToken {
		t.Fatal(e)
	}
}

func TestEmptyTokensFail(t *testing.T) {
	for _, tok := range []string{"", " \t\n"} {
		f := &fixture{token: tok}
		if e := authErr(t, f, timeout); e != msgEmptyToken {
			t.Fatalf("%q: %s", tok, e)
		}
	}
}

func TestCredentialInitFailureDoesNotDisclose(t *testing.T) {
	f := &fixture{credErr: errors.New(privateOutput)}
	if e := authErr(t, f, timeout); e != msgInit {
		t.Fatal(e)
	}
}

func TestDeadlineCancelsTokenCollection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fixture{blockToken: true}
		start := time.Now()
		if e := authErr(t, f, time.Minute); e != msgTimeout {
			t.Fatal(e)
		}
		if got := time.Since(start); got != time.Minute {
			t.Fatalf("returned after %v, want %v", got, time.Minute)
		}
		if !f.cancelled {
			t.Fatal("token request was not cancelled")
		}
	})
}

func TestTokenArrivingAfterDeadlineIsDiscarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fixture{token: opaqueToken}
		slow := func(o *azidentity.AzureCLICredentialOptions) (azcore.TokenCredential, error) {
			time.Sleep(2 * time.Minute)
			return f.newCred(o)
		}
		_, err := authenticateWith(t.Context(), f.lookPath, slow, time.Minute)
		if err == nil || err.Error() != msgTimeout {
			t.Fatalf("err = %v, want %q", err, msgTimeout)
		}
	})
}

func TestStaticCredentialFormattingRedactsToken(t *testing.T) {
	c := staticCredential{token: azcore.AccessToken{Token: opaqueToken}}
	var tc azcore.TokenCredential = c
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%t", "%q", "%x"} {
		for _, v := range []any{c, tc, &c} {
			if s := fmt.Sprintf(verb, v); s != redactedCredential {
				t.Errorf("%s of %T = %q", verb, v, s)
			}
		}
	}
}

// The Key Vault token uses the same single-process path with its own scope
// and its own fixed failure message.
func TestKeyVaultTokenRequestsVaultScopeWithFixedMessage(t *testing.T) {
	f := &fixture{token: opaqueToken}
	cred, err := tokenWith(t.Context(), f.lookPath, f.newCred, timeout, keyvault.Scope, msgNoKeyVaultToken)
	if err != nil {
		t.Fatal(err)
	}
	if tok, _ := cred.GetToken(t.Context(), policy.TokenRequestOptions{}); tok.Token != opaqueToken {
		t.Fatalf("got %q", tok.Token)
	}
	if got := strings.Join(f.calls, "|"); got != "lookpath az|get-token https://vault.azure.net/.default" {
		t.Fatalf("calls %v", got)
	}
	f = &fixture{tokenErr: errors.New(privateOutput)}
	if _, err := tokenWith(t.Context(), f.lookPath, f.newCred, timeout, keyvault.Scope, msgNoKeyVaultToken); err == nil || err.Error() != msgNoKeyVaultToken {
		t.Fatalf("err = %v", err)
	}
}
