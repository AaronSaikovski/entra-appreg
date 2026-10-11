package app

import (
	"strings"

	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
)

type scopeSpec struct {
	name            string
	displayName     string
	description     string
	userConsent     bool   // users may consent; otherwise admins only
	userDisplayName string // set only when userConsent
	userDescription string // set only when userConsent
	enabled         bool
}

// graphType is the Microsoft Graph permissionScope type value.
func (s scopeSpec) graphType() string {
	if s.userConsent {
		return "User"
	}
	return "Admin"
}

func (s scopeSpec) consentLabel() string {
	if s.userConsent {
		return "Admins and users"
	}
	return "Admins only"
}

func (s scopeSpec) stateLabel() string {
	if s.enabled {
		return "Enabled"
	}
	return "Disabled"
}

func validScopeName(name string) bool {
	if name == "" || len(name) > 120 || strings.HasPrefix(name, ".") {
		return false
	}
	for i := range len(name) {
		b := name[i]
		alnum := (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
		if !alnum && b != '.' && b != '_' && b != '-' {
			return false
		}
	}
	return true
}

// matchesAny reports whether value, trimmed, equals one of the words under
// ASCII case folding.
func matchesAny(value string, words ...string) bool {
	value = strings.TrimSpace(value)
	for _, word := range words {
		if cli.EqualFoldASCII(value, word) {
			return true
		}
	}
	return false
}

func trimmedOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return strings.TrimSpace(*value)
}

// resolveScope validates the --scope-* options. Every returned error is a
// *cli.UsageError.
func resolveScope(o *cli.Options, appName string) (scopeSpec, error) {
	rawName := deref(o.ScopeName, "")
	name := strings.TrimSpace(rawName)
	if !validScopeName(name) {
		return scopeSpec{}, cli.Usagef("--scope-name must be 1-120 characters using only letters, digits, '.', '_' and '-', and cannot start with '.', got: %s", rawName)
	}
	userConsent := false
	if o.ScopeConsent != nil {
		switch {
		case matchesAny(*o.ScopeConsent, "admin", "admins"):
		case matchesAny(*o.ScopeConsent, "user", "users"):
			userConsent = true
		default:
			return scopeSpec{}, cli.Usagef("--scope-consent must be 'admins' or 'users', got: %s", *o.ScopeConsent)
		}
	}
	enabled := true
	if o.ScopeState != nil {
		switch {
		case matchesAny(*o.ScopeState, "enabled"):
		case matchesAny(*o.ScopeState, "disabled"):
			enabled = false
		default:
			return scopeSpec{}, cli.Usagef("--scope-state must be 'enabled' or 'disabled', got: %s", *o.ScopeState)
		}
	}
	if !userConsent && (o.ScopeUserDisplayName != nil || o.ScopeUserDescription != nil) {
		return scopeSpec{}, cli.Usagef("--scope-user-display-name and --scope-user-description require --scope-consent users.")
	}
	for _, value := range []*string{o.ScopeDisplayName, o.ScopeDescription, o.ScopeUserDisplayName, o.ScopeUserDescription} {
		if value != nil && strings.TrimSpace(*value) == "" {
			return scopeSpec{}, cli.Usagef("The --scope-*display-name and --scope-*description options cannot be blank.")
		}
	}
	spec := scopeSpec{
		name:        name,
		displayName: trimmedOr(o.ScopeDisplayName, "Access "+appName),
		description: trimmedOr(o.ScopeDescription,
			"Allow the application to access "+appName+" on behalf of the signed-in user."),
		userConsent: userConsent,
		enabled:     enabled,
	}
	if userConsent {
		spec.userDisplayName = trimmedOr(o.ScopeUserDisplayName, "Access "+appName)
		spec.userDescription = trimmedOr(o.ScopeUserDescription,
			"Allow the application to access "+appName+" on your behalf.")
	}
	return spec, nil
}

func buildScope(id uuid.UUID, s scopeSpec) models.PermissionScopeable {
	scope := models.NewPermissionScope()
	scope.SetId(&id)
	scope.SetValue(new(s.name))
	scope.SetTypeEscaped(new(s.graphType()))
	scope.SetIsEnabled(new(s.enabled))
	scope.SetAdminConsentDisplayName(new(s.displayName))
	scope.SetAdminConsentDescription(new(s.description))
	if s.userConsent {
		scope.SetUserConsentDisplayName(new(s.userDisplayName))
		scope.SetUserConsentDescription(new(s.userDescription))
	}
	return scope
}
