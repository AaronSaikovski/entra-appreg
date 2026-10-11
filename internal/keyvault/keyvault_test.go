package keyvault

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const secretValue = "fake-one-time-secret-must-not-leak"

// fakeCred records each token request's scopes and tenant.
type fakeCred struct {
	mu       sync.Mutex
	requests []policy.TokenRequestOptions
}

func (c *fakeCred) GetToken(_ context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, o)
	return azcore.AccessToken{Token: "fake-vault-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type captured struct {
	method, path, query, auth string
	body                      map[string]any
	authenticated             int
}

// serve starts a loopback TLS vault. Unauthenticated requests get Key
// Vault's 401 challenge; each authenticated request is recorded and answered
// with status and body.
func serve(t *testing.T, status int, body string) (*Client, *captured, *fakeCred) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Bearer authorization="https://login.microsoftonline.com/vault-tenant", resource="https://vault.azure.net"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		got.authenticated++
		got.method, got.path, got.query, got.auth = r.Method, r.URL.EscapedPath(), r.URL.Query().Get("api-version"), r.Header.Get("Authorization")
		_ = json.Unmarshal(raw, &got.body)
		w.Header().Set("Content-Type", "application/json")
		if status == http.StatusFound {
			w.Header().Set("Location", "https://elsewhere.example/")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	transport := srv.Client()
	transport.CheckRedirect = hardenedHTTPClient().CheckRedirect
	cred := &fakeCred{}
	c, err := newClient(srv.URL, cred, transport, true)
	if err != nil {
		t.Fatal(err)
	}
	return c, got, cred
}

func TestSetSecretSendsValueAttributesAndTagsAfterChallenge(t *testing.T) {
	c, got, cred := serve(t, 200, `{"id":"https://v.vault.azure.net/secrets/app-secret/abc123","value":"`+secretValue+`"}`)
	exp := time.Date(2028, 5, 28, 16, 30, 45, 0, time.UTC)
	nbf := time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC)
	id, err := c.SetSecret(t.Context(), Secret{Name: "app-secret", Value: secretValue, Expires: exp, NotBefore: &nbf, Tags: map[string]string{"entra-client-id": "client"}})
	if err != nil || id != "https://v.vault.azure.net/secrets/app-secret/abc123" {
		t.Fatalf("got %q, %v", id, err)
	}
	if got.authenticated != 1 || got.method != "PUT" || got.path != "/secrets/app-secret" || got.query == "" || got.auth != "Bearer fake-vault-token" {
		t.Errorf("request = %+v", got)
	}
	// The SDK takes scope and tenant from the vault's challenge.
	if len(cred.requests) != 1 || strings.Join(cred.requests[0].Scopes, " ") != Scope || cred.requests[0].TenantID != "vault-tenant" {
		t.Errorf("token requests = %+v", cred.requests)
	}
	attrs := got.body["attributes"].(map[string]any)
	if got.body["value"] != secretValue || attrs["exp"] != float64(exp.Unix()) || attrs["nbf"] != float64(nbf.Unix()) || attrs["enabled"] != true {
		t.Errorf("body = %v", got.body)
	}
	if tags := got.body["tags"].(map[string]any); tags["entra-client-id"] != "client" {
		t.Errorf("tags = %v", tags)
	}
}

func TestSetSecretOmitsUnsetNotBeforeAndTags(t *testing.T) {
	c, got, _ := serve(t, 200, `{"id":"x"}`)
	if _, err := c.SetSecret(t.Context(), Secret{Name: "n", Value: "v", Expires: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.body["attributes"].(map[string]any)["nbf"]; ok {
		t.Error("nbf sent without NotBefore")
	}
	if _, ok := got.body["tags"]; ok {
		t.Error("tags sent without Tags")
	}
}

func TestSetSecretErrorsCarryGuidanceButNeverTheValue(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   []string
	}{
		{403, `{"error":{"code":"Forbidden","message":"denied"}}`, []string{"Key Vault Secrets Officer", "Storing secret 'n' in Key Vault failed", "403"}},
		{409, `{"error":{"code":"Conflict","message":"deleted"}}`, []string{"soft-deleted secret", "409"}},
		{500, `{"error":{"code":"Boom","message":"boom"}}`, []string{"Storing secret 'n' in Key Vault failed", "500", "Boom"}},
		{302, ``, []string{"Storing secret 'n' in Key Vault failed", "302"}}, // redirects are not followed
		{200, `{}`, []string{"requires an object with string 'id'"}},
		{200, `not json`, []string{"Storing secret 'n' in Key Vault failed"}},
	} {
		c, got, _ := serve(t, tc.status, tc.body)
		_, err := c.SetSecret(t.Context(), Secret{Name: "n", Value: secretValue, Expires: time.Unix(1, 0)})
		if err == nil {
			t.Errorf("HTTP %d: succeeded", tc.status)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("HTTP %d: %q lacks %q", tc.status, err, want)
			}
		}
		if strings.Contains(err.Error(), secretValue) {
			t.Errorf("HTTP %d: error leaks the secret: %v", tc.status, err)
		}
		// Retries are disabled: one authenticated attempt only.
		if got.authenticated != 1 {
			t.Errorf("HTTP %d: %d authenticated attempts", tc.status, got.authenticated)
		}
	}
}

func TestSetSecretRejectsInvalidNameBeforeSending(t *testing.T) {
	c, got, cred := serve(t, 200, `{"id":"x"}`)
	if _, err := c.SetSecret(t.Context(), Secret{Name: "bad_name", Value: "v"}); err == nil {
		t.Fatal("invalid name accepted")
	}
	if got.authenticated != 0 || len(cred.requests) != 0 {
		t.Errorf("request sent: %+v, token requests %d", got, len(cred.requests))
	}
}

func TestParseVaultURI(t *testing.T) {
	for raw, want := range map[string]string{
		"https://my-vault.vault.azure.net":                        "https://my-vault.vault.azure.net",
		"https://My-Vault.VAULT.azure.net/":                       "https://my-vault.vault.azure.net",
		" https://abc.vault.azure.net ":                           "https://abc.vault.azure.net",
		"http://abc.vault.azure.net":                              "",
		"https://abc.vault.azure.net:443":                         "",
		"https://abc.vault.azure.net/secrets":                     "",
		"https://abc.vault.azure.net/?x=1":                        "",
		"https://abc.vault.azure.net/#f":                          "",
		"https://u:p@abc.vault.azure.net":                         "",
		"https://ab.vault.azure.net":                              "",
		"https://1abc.vault.azure.net":                            "",
		"https://abc-.vault.azure.net":                            "",
		"https://a--bc.vault.azure.net":                           "",
		"https://abc.vault.azure.cn":                              "",
		"https://abc.vault.azure.net.evil.io":                     "",
		"https://evil.io/abc.vault.azure.net":                     "",
		"abc.vault.azure.net":                                     "",
		"https://" + strings.Repeat("a", 25) + ".vault.azure.net": "",
	} {
		got, ok := ParseVaultURI(raw)
		if got != want || ok != (want != "") {
			t.Errorf("ParseVaultURI(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
}

func TestValidSecretName(t *testing.T) {
	for name, want := range map[string]bool{
		"a": true, "App-Secret-1": true, strings.Repeat("a", 127): true,
		"": false, strings.Repeat("a", 128): false, "app_secret": false, "app.secret": false, "app secret": false, "ä": false,
	} {
		if got := ValidSecretName(name); got != want {
			t.Errorf("ValidSecretName(%q) = %v", name, got)
		}
	}
}

func TestNewRejectsUntrustedVaultURI(t *testing.T) {
	if _, err := New("https://abc.vault.azure.net.evil.io", &fakeCred{}); err == nil {
		t.Fatal("untrusted vault accepted")
	}
	if _, err := New("https://abc.vault.azure.net/", &fakeCred{}); err != nil {
		t.Fatal(err)
	}
}
