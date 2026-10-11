package cli

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeParams writes content to a fresh params file and returns its path.
func writeParams(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "params.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParamsFileFillsUnsetOptionsAndCommandLineWins(t *testing.T) {
	path := writeParams(t, `{
		"name": "From file", "audience": "AzureADMultipleOrgs",
		"redirect-urls": ["https://a.example/cb", "https://b.example/cb,https://c.example/cb"],
		"scope-name": "read", "scope-consent": null,
		"create-secret": true, "secret-expiry": 90
	}`)
	o := mustRun(t, "create", "--params-file", path)
	for name, got := range map[string]any{
		"name": deref(o.Name), "audience": deref(o.Audience), "scope-name": deref(o.ScopeName),
		"scope-consent": deref(o.ScopeConsent), "secret-expiry": deref(o.SecretExpiry),
	} {
		want := map[string]any{"name": "From file", "audience": "AzureADMultipleOrgs", "scope-name": "read", "scope-consent": nil, "secret-expiry": "90"}[name]
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if want := []string{"https://a.example/cb", "https://b.example/cb", "https://c.example/cb"}; !reflect.DeepEqual(o.RedirectURIs, want) || !o.RedirectsSupplied {
		t.Errorf("redirects = %q, supplied = %v", o.RedirectURIs, o.RedirectsSupplied)
	}
	if !o.CreateSecret {
		t.Error("create-secret from file not applied")
	}

	// The command line overrides scalars and replaces, not extends, the
	// file's redirect list; an empty command-line value still wins.
	o = mustRun(t, "--name", "", "--params-file", path, "--redirect-urls", "https://cli.example/cb")
	if got := deref(o.Name); got != "" {
		t.Errorf("name = %v, want the command-line empty value", got)
	}
	if want := []string{"https://cli.example/cb"}; !reflect.DeepEqual(o.RedirectURIs, want) {
		t.Errorf("redirects = %q", o.RedirectURIs)
	}

	// A false boolean in the file does not count as supplied.
	o = mustRun(t, "list", "--params-file", writeParams(t, `{"json": false, "top": "all", "name": "My"}`))
	if o.JSON || o.ListLimit != math.MaxInt32 || deref(o.Name) != "My" {
		t.Errorf("list options = json %v, limit %d, name %v", o.JSON, o.ListLimit, deref(o.Name))
	}
}

func TestParamsFileOptionsObeyCommandRules(t *testing.T) {
	expectErrMessage(t, "--scope-name can't be used with the list command.",
		"list", "--params-file", writeParams(t, `{"scope-name": "read"}`))
	expectErrMessage(t, "--top can only be used with the list command.",
		"--params-file", writeParams(t, `{"top": 5}`))
	expectErrMessage(t, "expose-api requires --scope-name.",
		"expose-api", "--params-file", writeParams(t, `{"appid": "id"}`))
	expectErrMessage(t, "--top must be a positive whole number or 'all', got: 0",
		"list", "--params-file", writeParams(t, `{"top": 0}`))
	if got := deref(mustRun(t, "delete", "--params-file", writeParams(t, `{"appid": "id"}`)).AppID); got != "id" {
		t.Errorf("delete appid = %v", got)
	}
	expectErrMessage(t, "--keyvault-uri, --keyvault-secret-name can't be used with the expose-api command.",
		"expose-api", "--appid", "id", "--scope-name", "s", "--params-file",
		writeParams(t, `{"keyvault-uri": "https://vault1.vault.azure.net", "keyvault-secret-name": "s"}`))
	expectErrMessage(t, "--keyvault-uri can't be used with the list command.", "list", "--keyvault-uri", "x")
	o := mustRun(t, "--params-file", writeParams(t, `{"keyvault-uri": "https://vault1.vault.azure.net", "keyvault-secret-name": "s"}`))
	if deref(o.KeyVaultURI) != "https://vault1.vault.azure.net" || deref(o.KeyVaultSecretName) != "s" {
		t.Errorf("keyvault options = %v, %v", deref(o.KeyVaultURI), deref(o.KeyVaultSecretName))
	}
}

func TestParamsFileRejectsBadFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	if err := expectErr(t, "--params-file", missing); !strings.HasPrefix(err.Error(), "Could not read --params-file: ") {
		t.Errorf("missing file: %q", err)
	}
	expectErrMessage(t, "--params-file cannot be blank.", "--params-file", " ")

	big := writeParams(t, `{"name": "`+strings.Repeat("x", maxParamsFile)+`"}`)
	expectErrMessage(t, "--params-file "+big+" is larger than 1048576 bytes.", "--params-file", big)

	for _, c := range []struct{ content, want string }{
		{`{"name": "a",}`, "is not valid JSON: "},
		{``, "is not valid JSON: "},
		{`["name"]`, "must contain a JSON object."},
		{`null`, "must contain a JSON object."},
		{`{"name": "a"} {}`, "must contain exactly one JSON object."},
		{`{"zeta": 1, "alpha": 1, "name": "a"}`, `unknown key "alpha". Keys are option names without the leading '--'.`},
		{`{"redirect-url": "https://a.example"}`, `unknown key "redirect-url".`},
		{`{"params-file": "other.json"}`, `unknown key "params-file".`},
		{`{"name": 5}`, `"name" must be a string.`},
		{`{"top": 1.5}`, `"top" must be a string or a whole number.`},
		{`{"secret-expiry": true}`, `"secret-expiry" must be a string or a whole number.`},
		{`{"create-secret": "yes"}`, `"create-secret" must be true or false.`},
		{`{"redirect-urls": [1]}`, `"redirect-urls" must be a string or an array of strings.`},
	} {
		path := writeParams(t, c.content)
		err := expectErr(t, "--params-file", path)
		if prefix := "--params-file " + path; !strings.HasPrefix(err.Error(), prefix) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %q, want %q", c.content, err, c.want)
		}
	}
}

func TestParamsFileIsNotReadForHelpOrVersion(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	expectHelp(t, TopicCreate, "create", "--params-file", missing, "--help")
	expectVersion(t, "--params-file", missing, "--version")
}

// The shipped sample must stay a valid create input.
func TestSampleParamsFileParses(t *testing.T) {
	o := mustRun(t, "create", "--params-file", filepath.Join("..", "..", "examples", "spa.params.json"))
	if deref(o.Name) != "My SPA" || deref(o.ScopeName) != "access_as_user" || len(o.RedirectURIs) != 2 {
		t.Errorf("sample options = name %v, scope %v, redirects %q", deref(o.Name), deref(o.ScopeName), o.RedirectURIs)
	}
}
