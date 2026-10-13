package app

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

// exposeAPI adds one scope to an existing registration. The PATCH body is
// rebuilt from fresh models (see copyAPI) and sets identifierUris only when
// the registration has none.
func exposeAPI(ctx context.Context, o *cli.Options, g *graph.Client, env Env) error {
	out := env.Stdout
	reference := *o.AppID // cli.Parse requires --appid for expose-api
	fmt.Fprintf(out, "Looking up application %s\n", reference)
	app, err := g.GetApplication(ctx, reference)
	if err != nil {
		return err
	}
	if app == nil {
		return fmt.Errorf("Application not found: %s", reference)
	}
	objectID, clientID := *app.GetId(), *app.GetAppId()
	name := deref(app.GetDisplayName(), reference)
	spec, err := resolveScope(o, name)
	if err != nil {
		return err
	}
	fetched := app.GetApi()
	if hasScope(fetched, spec.name) {
		return fmt.Errorf("'%s' already has a scope named '%s'. Nothing was changed.", name, spec.name)
	}
	existingURI := firstIdentifierURI(app.GetIdentifierUris())
	identifier := existingURI
	if identifier == "" {
		identifier = "api://" + clientID
	}
	fullName := strings.TrimRight(identifier, "/") + "/" + spec.name
	scopeID := uuid.New()
	fmt.Fprintf(out, "Adding scope '%s' to '%s' (%s, %s)\n", fullName, name, spec.consentLabel(), spec.stateLabel())

	api := copyAPI(fetched)
	api.SetOauth2PermissionScopes(append(api.GetOauth2PermissionScopes(), buildScope(scopeID, spec)))
	patch := models.NewApplication()
	patch.SetApi(api)
	api.SetRequestedAccessTokenVersion(new(int32(2)))
	patch.SetOptionalClaims(withAuthTime(app.GetOptionalClaims()))
	if existingURI == "" {
		patch.SetIdentifierUris([]string{identifier})
	}
	if err := g.PatchApplication(ctx, objectID, patch); err != nil {
		return err
	}
	fmt.Fprintf(out, "AUTH_APP_ID=%s\n", objectID)
	fmt.Fprintf(out, "AUTH_CLIENT_ID=%s\n", clientID)
	fmt.Fprintf(out, "Application ID URI: %s\n", identifier)
	fmt.Fprintf(out, "Scope (full name):  %s\n", fullName)
	fmt.Fprintf(out, "Scope ID:           %s\n", scopeID)
	fmt.Fprintf(out, "Scope consent:      %s\n", spec.consentLabel())
	fmt.Fprintf(out, "Scope state:        %s\n", spec.stateLabel())
	return nil
}

// hasScope reports whether api already defines a scope whose value equals
// name under ASCII case folding.
func hasScope(api models.ApiApplicationable, name string) bool {
	if api == nil {
		return false
	}
	for _, scope := range api.GetOauth2PermissionScopes() {
		if scope != nil && cli.EqualFoldASCII(deref(scope.GetValue(), ""), name) {
			return true
		}
	}
	return false
}

// firstIdentifierURI returns the first non-empty identifier URI, or "".
func firstIdentifierURI(uris []string) string {
	for _, uri := range uris {
		if uri != "" {
			return uri
		}
	}
	return ""
}

// authTimeClaim is the access-token optional claim every exposed API gets.
const authTimeClaim = "auth_time"

// withAuthTime returns fresh optionalClaims models copied from fetched (nil
// for a new registration) with auth_time added to the access-token claims
// when absent. A PATCH replaces the whole object, so the id and SAML claim
// lists are resent as fetched, and empty lists are sent rather than omitted.
func withAuthTime(fetched models.OptionalClaimsable) models.OptionalClaimsable {
	claims := models.NewOptionalClaims()
	var access, id, saml []models.OptionalClaimable
	if fetched != nil {
		access = copyClaims(fetched.GetAccessToken())
		id = copyClaims(fetched.GetIdToken())
		saml = copyClaims(fetched.GetSaml2Token())
		if v := fetched.GetOdataType(); v != nil {
			claims.SetOdataType(v)
		}
		if v := fetched.GetAdditionalData(); v != nil {
			claims.SetAdditionalData(maps.Clone(v))
		}
	}
	present := false
	for _, c := range access {
		if deref(c.GetName(), "") == authTimeClaim && c.GetSource() == nil {
			present = true
		}
	}
	if !present {
		c := models.NewOptionalClaim()
		c.SetName(new(authTimeClaim))
		c.SetEssential(new(false))
		c.SetAdditionalProperties([]string{})
		access = append(access, c)
	}
	claims.SetAccessToken(access)
	claims.SetIdToken(nonNilClaims(id))
	claims.SetSaml2Token(nonNilClaims(saml))
	return claims
}

func nonNilClaims(c []models.OptionalClaimable) []models.OptionalClaimable {
	if c == nil {
		return []models.OptionalClaimable{}
	}
	return c
}

// copyClaims copies fetched optional claims into fresh models, dropping nil
// entries (backing-store rule, see copyAPI).
func copyClaims(fetched []models.OptionalClaimable) []models.OptionalClaimable {
	if fetched == nil {
		return nil
	}
	copied := make([]models.OptionalClaimable, 0, len(fetched))
	for _, f := range fetched {
		if f == nil {
			continue
		}
		c := models.NewOptionalClaim()
		if v := f.GetName(); v != nil {
			c.SetName(v)
		}
		if v := f.GetSource(); v != nil {
			c.SetSource(v)
		}
		if v := f.GetEssential(); v != nil {
			c.SetEssential(v)
		}
		if v := f.GetAdditionalProperties(); v != nil {
			c.SetAdditionalProperties(v)
		}
		if v := f.GetOdataType(); v != nil {
			c.SetOdataType(v)
		}
		if v := f.GetAdditionalData(); v != nil {
			c.SetAdditionalData(maps.Clone(v))
		}
		copied = append(copied, c)
	}
	return copied
}

// copyAPI copies a fetched api object into fresh models. The backing store
// serializes only fields changed after a fetch, so fetched models cannot be
// sent back. Each setter runs only when the fetched value is present, so absent
// fields are not sent as null. Nil list entries are dropped.
func copyAPI(fetched models.ApiApplicationable) models.ApiApplicationable {
	api := models.NewApiApplication()
	if fetched == nil {
		return api
	}
	if v := fetched.GetAcceptMappedClaims(); v != nil {
		api.SetAcceptMappedClaims(v)
	}
	if v := fetched.GetKnownClientApplications(); v != nil {
		api.SetKnownClientApplications(v)
	}
	if v := fetched.GetRequestedAccessTokenVersion(); v != nil {
		api.SetRequestedAccessTokenVersion(v)
	}
	if v := fetched.GetOdataType(); v != nil {
		api.SetOdataType(v)
	}
	if v := fetched.GetAdditionalData(); v != nil {
		api.SetAdditionalData(maps.Clone(v))
	}
	if pre := fetched.GetPreAuthorizedApplications(); pre != nil {
		copied := make([]models.PreAuthorizedApplicationable, 0, len(pre))
		for _, p := range pre {
			if p != nil {
				copied = append(copied, copyPreAuthorized(p))
			}
		}
		api.SetPreAuthorizedApplications(copied)
	}
	if scopes := fetched.GetOauth2PermissionScopes(); scopes != nil {
		// One spare slot for the scope exposeAPI appends.
		copied := make([]models.PermissionScopeable, 0, len(scopes)+1)
		for _, s := range scopes {
			if s != nil {
				copied = append(copied, copyScope(s))
			}
		}
		api.SetOauth2PermissionScopes(copied)
	}
	return api
}

func copyPreAuthorized(p models.PreAuthorizedApplicationable) models.PreAuthorizedApplicationable {
	c := models.NewPreAuthorizedApplication()
	if v := p.GetAppId(); v != nil {
		c.SetAppId(v)
	}
	if v := p.GetDelegatedPermissionIds(); v != nil {
		c.SetDelegatedPermissionIds(v)
	}
	if v := p.GetOdataType(); v != nil {
		c.SetOdataType(v)
	}
	if v := p.GetAdditionalData(); v != nil {
		c.SetAdditionalData(maps.Clone(v))
	}
	return c
}

func copyScope(s models.PermissionScopeable) models.PermissionScopeable {
	c := models.NewPermissionScope()
	if v := s.GetAdminConsentDescription(); v != nil {
		c.SetAdminConsentDescription(v)
	}
	if v := s.GetAdminConsentDisplayName(); v != nil {
		c.SetAdminConsentDisplayName(v)
	}
	if v := s.GetId(); v != nil {
		c.SetId(v)
	}
	if v := s.GetIsEnabled(); v != nil {
		c.SetIsEnabled(v)
	}
	if v := s.GetOrigin(); v != nil {
		c.SetOrigin(v)
	}
	if v := s.GetTypeEscaped(); v != nil {
		c.SetTypeEscaped(v)
	}
	if v := s.GetUserConsentDescription(); v != nil {
		c.SetUserConsentDescription(v)
	}
	if v := s.GetUserConsentDisplayName(); v != nil {
		c.SetUserConsentDisplayName(v)
	}
	if v := s.GetValue(); v != nil {
		c.SetValue(v)
	}
	if v := s.GetOdataType(); v != nil {
		c.SetOdataType(v)
	}
	if v := s.GetAdditionalData(); v != nil {
		c.SetAdditionalData(maps.Clone(v))
	}
	return c
}
