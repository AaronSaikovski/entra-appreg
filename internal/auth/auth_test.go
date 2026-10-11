package auth

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

const (
	tenant        = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	opaqueToken   = "opaque-access-token-not-a-jwt"
	privateOutput = "private-cli-output-must-not-leak"
	timeout       = 10 * time.Second
)

type runResult struct {
	out []byte
	err error
}

type fixture struct {
	mu        sync.Mutex
	results   []runResult
	calls     [][]string
	blockRun  bool
	cancelled bool

	credOpts   []*azidentity.AzureCLICredentialOptions
	credErr    error
	tokenErr   error
	token      string
	blockToken bool
}

func (f *fixture) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.blockRun {
		f.mu.Unlock()
		<-ctx.Done()
		f.mu.Lock()
		f.cancelled = true
		f.mu.Unlock()
		return nil, errors.New(privateOutput)
	}
	defer f.mu.Unlock()
	if len(f.results) == 0 {
		panic("unexpected Azure CLI invocation")
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r.out, r.err
}

type fakeCred struct{ f *fixture }

func (c fakeCred) GetToken(ctx context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f := c.f
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{"get-token"}, o.Scopes...))
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
	f.credOpts = append(f.credOpts, o)
	if f.credErr != nil {
		return nil, f.credErr
	}
	return fakeCred{f}, nil
}

func ok(out string) runResult { return runResult{out: []byte(out)} }

func exitFail() runResult { return runResult{out: []byte(privateOutput), err: &exec.ExitError{}} }

func authErr(t *testing.T, f *fixture, pin bool, d time.Duration) string {
	t.Helper()
	_, err := authenticateWith(context.Background(), pin, f.run, f.newCred, d)
	if err == nil {
		t.Fatal("authentication unexpectedly succeeded")
	}
	return err.Error()
}

func TestCreateDiscoversAndPinsNormalizedTenantAcceptingOpaqueToken(t *testing.T) {
	f := &fixture{results: []runResult{ok("  AAAAAAAABBBBCCCCDDDDEEEEEEEEEEEE\n")}, token: opaqueToken}
	c, err := authenticateWith(context.Background(), true, f.run, f.newCred, timeout)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := c.Credential.GetToken(context.Background(), policy.TokenRequestOptions{})
	if tok.Token != opaqueToken || c.TenantID != tenant {
		t.Fatalf("got %q %q", tok.Token, c.TenantID)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls %v", f.calls)
	}
	if !strings.HasSuffix(strings.Join(f.calls[0], " "), "az account show --query tenantId --output tsv --only-show-errors") {
		t.Fatalf("tenant command %v", f.calls[0])
	}
	if strings.Join(f.calls[1], " ") != "get-token "+graph.Scope {
		t.Fatalf("token call %v", f.calls[1])
	}
	if len(f.credOpts) != 1 || f.credOpts[0] == nil || f.credOpts[0].TenantID != tenant {
		t.Fatalf("credential options %+v", f.credOpts)
	}
}

func TestNonCreateCommandsCheckLoginWithoutPinningTokenTenant(t *testing.T) {
	f := &fixture{results: []runResult{ok(tenant)}, token: opaqueToken}
	c, err := authenticateWith(context.Background(), false, f.run, f.newCred, timeout)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := c.Credential.GetToken(context.Background(), policy.TokenRequestOptions{})
	if tok.Token != opaqueToken || c.TenantID != "" {
		t.Fatalf("got %q %q", tok.Token, c.TenantID)
	}
	if len(f.calls) != 2 || len(f.credOpts) != 1 || f.credOpts[0].TenantID != "" {
		t.Fatalf("calls %v opts %+v", f.calls, f.credOpts)
	}
}

func TestInvalidTenantStopsBeforeTokenRequestWithoutDisclosingOutput(t *testing.T) {
	for _, out := range []string{
		"",
		" \t\n",
		privateOutput,
		tenant + "; echo unsafe",
		"\xff",
		"\xff" + tenant + "\xff", // uuid.Parse alone accepts any outer bytes here
	} {
		t.Run(fmt.Sprintf("%q", out), func(t *testing.T) {
			f := &fixture{results: []runResult{ok(out)}, token: opaqueToken}
			e := authErr(t, f, true, timeout)
			if e != msgInvalidTenant {
				t.Fatal(e)
			}
			if len(f.calls) != 1 || len(f.credOpts) != 0 {
				t.Fatalf("calls %v", f.calls)
			}
		})
	}
}

func TestMalformedTokensFailWithoutDisclosure(t *testing.T) {
	for name, f := range map[string]*fixture{
		"token error": {tokenErr: errors.New("malformed " + privateOutput + " " + opaqueToken)},
		"empty":       {token: ""},
		"whitespace":  {token: " \t\n"},
	} {
		t.Run(name, func(t *testing.T) {
			f.results = []runResult{ok(tenant)}
			e := authErr(t, f, false, timeout)
			if strings.Contains(e, privateOutput) || strings.Contains(e, opaqueToken) {
				t.Fatalf("leaked: %s", e)
			}
			if len(f.calls) != 2 {
				t.Fatalf("calls %v", f.calls)
			}
		})
	}
}

func TestCredentialInitFailureDoesNotDisclose(t *testing.T) {
	f := &fixture{results: []runResult{ok(tenant)}, credErr: errors.New(privateOutput)}
	if e := authErr(t, f, true, timeout); e != msgInit {
		t.Fatal(e)
	}
}

func TestProcessFailureNeverDisclosesStderrStdoutOrIOErrorDetails(t *testing.T) {
	for _, pin := range []bool{true, false} {
		for name, r := range map[string]runResult{
			"exit":      {out: []byte(opaqueToken), err: &exec.ExitError{}},
			"not found": {err: fmt.Errorf("%s: %w", privateOutput, exec.ErrNotFound)},
			"start":     {err: errors.New(privateOutput)},
		} {
			t.Run(fmt.Sprintf("pin=%v/%s", pin, name), func(t *testing.T) {
				f := &fixture{results: []runResult{r}}
				e := authErr(t, f, pin, timeout)
				if !strings.Contains(e, "Azure CLI") || strings.Contains(e, privateOutput) || strings.Contains(e, opaqueToken) {
					t.Fatal(e)
				}
				if len(f.calls) != 1 {
					t.Fatalf("calls %v", f.calls)
				}
			})
		}
	}
	f := &fixture{results: []runResult{{err: exec.ErrNotFound}}}
	if e := authErr(t, f, false, timeout); e != msgNotFound {
		t.Fatal(e)
	}
}

func TestUnavailableLoginStopsBeforeTokenAcquisitionForEveryCommand(t *testing.T) {
	for _, pin := range []bool{true, false} {
		f := &fixture{results: []runResult{exitFail()}}
		e := authErr(t, f, pin, timeout)
		if e != msgNoLogin || len(f.calls) != 1 {
			t.Fatalf("pin=%v: %s calls %v", pin, e, f.calls)
		}
	}
}

func TestDeadlineCancelsTenantDiscoveryAndTokenCollection(t *testing.T) {
	for _, pin := range []bool{true, false} {
		for _, step := range []string{"tenant", "token"} {
			t.Run(fmt.Sprintf("pin=%v/%s", pin, step), func(t *testing.T) {
				f := &fixture{blockRun: step == "tenant", blockToken: step == "token"}
				wantCalls := 1
				if step == "token" {
					f.results = []runResult{ok(tenant)}
					wantCalls = 2
				}
				if e := authErr(t, f, pin, 10*time.Millisecond); e != msgTimeout {
					t.Fatal(e)
				}
				if len(f.calls) != wantCalls || !f.cancelled {
					t.Fatalf("calls %v cancelled %v", f.calls, f.cancelled)
				}
			})
		}
	}
}

func TestTokenArrivingAfterDeadlineIsDiscarded(t *testing.T) {
	f := &fixture{results: []runResult{ok(tenant)}, token: opaqueToken}
	slow := func(o *azidentity.AzureCLICredentialOptions) (azcore.TokenCredential, error) {
		time.Sleep(20 * time.Millisecond)
		return f.newCred(o)
	}
	_, err := authenticateWith(context.Background(), false, f.run, slow, 10*time.Millisecond)
	if err == nil || err.Error() != msgTimeout {
		t.Fatalf("err = %v, want %q", err, msgTimeout)
	}
}

func TestStaticCredentialFormattingRedactsToken(t *testing.T) {
	c := staticCredential{token: azcore.AccessToken{Token: opaqueToken}}
	s := fmt.Sprintf("%v %+v %#v %s", c, c, c, c)
	if strings.Contains(s, opaqueToken) {
		t.Fatal(s)
	}
	var tc azcore.TokenCredential = c
	if s := fmt.Sprintf("%v %+v %#v", tc, tc, tc); strings.Contains(s, opaqueToken) {
		t.Fatal(s)
	}
}
