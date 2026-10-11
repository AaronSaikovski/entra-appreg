// Package keyvault stores a secret in Azure Key Vault with the Azure SDK for
// Go (azsecrets). The SDK handles Key Vault's challenge authentication and
// verifies that the challenge resource matches the vault's domain. Its
// pipeline runs on the CLI's hardened transport: a 10-second timeout, no
// redirects and no retries. The secret value is sent only in the request
// body and never appears in errors.
package keyvault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// Scope is the OAuth scope requested for Azure Key Vault tokens.
const Scope = "https://vault.azure.net/.default"

const vaultSuffix = ".vault.azure.net"

// Secret is one secret version to store.
type Secret struct {
	Name      string
	Value     string
	Expires   time.Time         // the secret's exp attribute
	NotBefore *time.Time        // nbf attribute; nil = usable now
	Tags      map[string]string // nil = no tags
}

// Client writes secrets to one vault.
type Client struct {
	sdk *azsecrets.Client
}

// ParseVaultURI accepts https://<vault-name>.vault.azure.net with an
// optional trailing "/" and nothing else: no port, path, query, fragment or
// credentials. The vault name is 3-24 letters, digits or hyphens, starting
// with a letter. It returns the normalized "https://<host>" form.
func ParseVaultURI(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	name, ok := strings.CutSuffix(host, vaultSuffix)
	if !ok || !validVaultName(name) {
		return "", false
	}
	return "https://" + host, true
}

func validVaultName(name string) bool {
	if len(name) < 3 || len(name) > 24 || name[0] < 'a' || name[0] > 'z' ||
		strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// ValidSecretName reports whether name is a Key Vault secret name: 1-127
// ASCII letters, digits or hyphens.
func ValidSecretName(name string) bool {
	if len(name) < 1 || len(name) > 127 {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// New returns a client for vaultURI, which must pass ParseVaultURI.
func New(vaultURI string, cred azcore.TokenCredential) (*Client, error) {
	normalized, ok := ParseVaultURI(vaultURI)
	if !ok {
		return nil, fmt.Errorf("Invalid Key Vault URI: %s", vaultURI)
	}
	return newClient(normalized, cred, hardenedHTTPClient(), false)
}

// hardenedHTTPClient bounds each request and returns redirects as responses,
// so they surface as errors instead of being followed.
func hardenedHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// newClient builds the SDK client. Tests pass a loopback TLS transport and
// disable challenge resource verification, which would reject 127.0.0.1.
func newClient(vaultURL string, cred azcore.TokenCredential, transport policy.Transporter, disableVerify bool) (*Client, error) {
	sdk, err := azsecrets.NewClient(vaultURL, cred, &azsecrets.ClientOptions{
		Transport:                            transport,
		Retry:                                policy.RetryOptions{MaxRetries: -1},
		DisableChallengeResourceVerification: disableVerify,
	})
	if err != nil {
		return nil, err
	}
	return &Client{sdk: sdk}, nil
}

// SetSecret stores s as a new version of the named secret and returns the
// version ID Key Vault assigns. An existing secret keeps its older versions.
func (c *Client) SetSecret(ctx context.Context, s Secret) (string, error) {
	if !ValidSecretName(s.Name) {
		return "", fmt.Errorf("Invalid Key Vault secret name: %s", s.Name)
	}
	params := azsecrets.SetSecretParameters{
		Value: new(s.Value),
		SecretAttributes: &azsecrets.SecretAttributes{
			Enabled:   new(true),
			Expires:   new(s.Expires),
			NotBefore: s.NotBefore,
		},
	}
	if len(s.Tags) > 0 {
		params.Tags = make(map[string]*string, len(s.Tags))
		for k, v := range s.Tags {
			params.Tags[k] = new(v)
		}
	}
	resp, err := c.sdk.SetSecret(ctx, s.Name, params, nil)
	if err != nil {
		return "", describe(s.Name, err)
	}
	if resp.ID == nil || *resp.ID == "" {
		return "", errors.New("Key Vault response requires an object with string 'id'; the secret may have been stored.")
	}
	return string(*resp.ID), nil
}

// describe adds guidance for the usual Key Vault failures. A ResponseError
// carries the request URL, status and Key Vault's error body, none of which
// contain the secret value.
func describe(name string, err error) error {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return fmt.Errorf("Storing secret '%s' in Key Vault failed: %w", name, err)
	}
	guidance := ""
	switch respErr.StatusCode {
	case http.StatusUnauthorized:
		guidance = "Key Vault rejected the token (HTTP 401). The vault must be in the tenant of your active Azure CLI login; check 'az account show'.\n"
	case http.StatusForbidden:
		guidance = "Key Vault denied access (HTTP 403). Your account needs the 'Key Vault Secrets Officer' role on the vault (Azure RBAC), or a 'Set' secret permission in its access policy. Check the vault's network firewall too.\n"
	case http.StatusConflict:
		guidance = "Key Vault reported a conflict (HTTP 409). A soft-deleted secret with this name may exist; recover or purge it, or choose another --keyvault-secret-name.\n"
	}
	return fmt.Errorf("%sStoring secret '%s' in Key Vault failed: %w", guidance, name, err)
}
