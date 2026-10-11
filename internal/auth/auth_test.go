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
	if strings.Join(f.calls[1], " ") != "get-token "+graphScope {
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
	for _, out := range []string{"", " \t\n", privateOutput, tenant + "; echo unsafe", "\xff"} {
		f := &fixture{results: []runResult{ok(out)}, token: opaqueToken}
		e := authErr(t, f, true, timeout)
		if !strings.Contains(e, "UUID") || strings.Contains(e, privateOutput) || strings.Contains(e, "unsafe") {
			t.Fatalf("%q: %s", out, e)
		}
		if len(f.calls) != 1 || len(f.credOpts) != 0 {
			t.Fatalf("%q: calls %v", out, f.calls)
		}
	}
}

func TestMalformedTokensFailWithoutDisclosure(t *testing.T) {
	cases := []*fixture{
		{tokenErr: errors.New("malformed " + privateOutput + " " + opaqueToken)},
		{token: ""},
		{token: " \t\n"},
	}
	for i, f := range cases {
		f.results = []runResult{ok(tenant)}
		e := authErr(t, f, false, timeout)
		if strings.Contains(e, privateOutput) || strings.Contains(e, opaqueToken) {
			t.Fatalf("case %d leaked: %s", i, e)
		}
		if len(f.calls) != 2 {
			t.Fatalf("case %d calls %v", i, f.calls)
		}
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
		for _, r := range []runResult{
			{out: []byte(opaqueToken), err: &exec.ExitError{}},
			{err: fmt.Errorf("%s: %w", privateOutput, exec.ErrNotFound)},
			{err: errors.New(privateOutput)},
		} {
			f := &fixture{results: []runResult{r}}
			e := authErr(t, f, pin, timeout)
			if !strings.Contains(e, "Azure CLI") || strings.Contains(e, privateOutput) || strings.Contains(e, opaqueToken) {
				t.Fatal(e)
			}
			if len(f.calls) != 1 {
				t.Fatalf("calls %v", f.calls)
			}
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
		if e != msgNoLogin || strings.Contains(e, privateOutput) || len(f.calls) != 1 {
			t.Fatalf("%s calls %v", e, f.calls)
		}
	}
}

func TestDeadlineCancelsTenantDiscoveryAndTokenCollection(t *testing.T) {
	for _, pin := range []bool{true, false} {
		for pending := 1; pending <= 2; pending++ {
			f := &fixture{blockRun: pending == 1, blockToken: pending == 2}
			if pending == 2 {
				f.results = []runResult{ok(tenant)}
			}
			e := authErr(t, f, pin, 10*time.Millisecond)
			if e != msgTimeout {
				t.Fatalf("pin=%v pending=%d: %s", pin, pending, e)
			}
			if len(f.calls) != pending || !f.cancelled {
				t.Fatalf("pin=%v pending=%d calls %v cancelled %v", pin, pending, f.calls, f.cancelled)
			}
		}
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
