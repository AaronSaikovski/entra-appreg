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
	spec, msg := resolveScope(o, name)
	if msg != "" {
		return usagef("%s", msg)
	}
	fetched := app.GetApi()
	if fetched != nil {
		for _, scope := range fetched.GetOauth2PermissionScopes() {
			if scope == nil {
				continue
			}
			if cli.EqualFoldASCII(deref(scope.GetValue(), ""), spec.name) {
				return fmt.Errorf("'%s' already has a scope named '%s'. Nothing was changed.", name, spec.name)
			}
		}
	}
	existingURI := ""
	for _, uri := range app.GetIdentifierUris() {
		if uri != "" {
			existingURI = uri
			break
		}
	}
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

// copyAPI copies a fetched api object into fresh models. The backing store
// serializes only fields changed after a fetch, so fetched models cannot be
// sent back. Each setter runs only when the fetched value is present, so absent
// fields are not sent as null.
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
			if p == nil {
				continue
			}
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
			copied = append(copied, c)
		}
		api.SetPreAuthorizedApplications(copied)
	}
	if scopes := fetched.GetOauth2PermissionScopes(); scopes != nil {
		copied := make([]models.PermissionScopeable, 0, len(scopes)+1)
		for _, s := range scopes {
			if s == nil {
				continue
			}
			copied = append(copied, copyScope(s))
		}
		api.SetOauth2PermissionScopes(copied)
	}
	return api
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
