package app

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRedirectsRequireExplicitHTTPAuthorityWithoutRepair(t *testing.T) {
	for _, uri := range []string{
		"http:example.com",
		"https:/example.com",
		`https:\example.com`,
		"https:///example.com",
		"https://",
		"/callback",
		"ftp://example.com",
		"https://example.com:0/",
		"https://example.com:65536/",
	} {
		if validRedirect(uri) {
			t.Errorf("%q accepted", uri)
		}
	}
	for _, uri := range []string{
		"https://example.com/Callback",
		"https://example.com/callback/",
		"HTTP://localhost:5173",
		"https://example.com/#fragment",
	} {
		if !validRedirect(uri) {
			t.Errorf("%q rejected", uri)
		}
	}
}

func TestScopeBoundariesAndConsentContradictions(t *testing.T) {
	for _, name := range []string{"", ".read", "a b", "é", strings.Repeat("a", 121)} {
		if _, err := resolveScope(options(t, "--scope-name", name), "App"); err == nil {
			t.Errorf("scope name %q accepted", name)
		}
	}
	spec, err := resolveScope(options(t, "--scope-name", strings.Repeat("a", 120)), "App")
	if err != nil || len(spec.name) != 120 {
		t.Fatalf("120-character name: %q, %v", spec.name, err)
	}
	if buildScope(uuid.New(), spec).GetUserConsentDescription() != nil {
		t.Error("admin scope carries a user consent description")
	}
	for _, args := range [][]string{
		{"--scope-name", "read", "--scope-user-display-name", "user"},
		{"--scope-name", "read", "--scope-display-name", " "},
		{"--scope-name", "read", "--scope-consent", ""},
		{"--scope-name", "read", "--scope-state", ""},
		{"--scope-name", "read", "--scope-consent", "users", "--scope-user-description", ""},
	} {
		if _, err := resolveScope(options(t, args...), "App"); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
	spec, err = resolveScope(options(t,
		"--scope-name", " read ", "--scope-consent", " USERS ", "--scope-state", "disabled"), "My App")
	if err != nil {
		t.Fatal(err)
	}
	scope := buildScope(uuid.New(), spec)
	if *scope.GetValue() != "read" || *scope.GetTypeEscaped() != "User" || *scope.GetIsEnabled() {
		t.Errorf("scope = %q %q %v", *scope.GetValue(), *scope.GetTypeEscaped(), *scope.GetIsEnabled())
	}
	if got := *scope.GetAdminConsentDescription(); got != "Allow the application to access My App on behalf of the signed-in user." {
		t.Errorf("admin description = %q", got)
	}
	if got := *scope.GetUserConsentDescription(); got != "Allow the application to access My App on your behalf." {
		t.Errorf("user description = %q", got)
	}
}
