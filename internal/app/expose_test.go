package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
)

const oldScopeID = "00000000-0000-0000-0000-0000000000a1"

func exposeArgs(ref string) []string {
	return []string{"expose-api", "--appid", ref, "--scope-name", "read"}
}

func TestExposePreservesExistingAPIAndIdentifierURIs(t *testing.T) {
	apiJSON := `{"oauth2PermissionScopes":[{"id":"` + oldScopeID + `","value":"existing","unknown":"retained"}],` +
		`"requestedAccessTokenVersion":2,` +
		`"preAuthorizedApplications":[{"appId":"pre","delegatedPermissionIds":["` + oldScopeID + `"]}],` +
		`"unknown":{"retain":true}}`
	f := graphtest.New(t,
		raw(200, `{"id":"object","appId":"client","displayName":"Original","identifierUris":["api://first/","api://second"],"api":`+apiJSON+`}`),
		raw(204, ""))
	tio := newTestIO(t, "")
	if err := exposeAPI(context.Background(), options(t, exposeArgs("object")...), f.Client(t), tio.env); err != nil {
		t.Fatal(err)
	}
	requests := f.Requests()
	if len(requests) != 2 || requests[1].Method != "PATCH" || requests[1].Path != "/v1.0/applications/object" {
		t.Fatalf("requests = %+v", requests)
	}
	patch := decode(t, requests[1])
	if _, present := patch["identifierUris"]; present {
		t.Error("existing identifierUris were replaced")
	}
	api := patch["api"].(obj)
	scopes := api["oauth2PermissionScopes"].(arr)
	newScope := scopes[len(scopes)-1].(obj)
	if newScope["value"] != "read" || newScope["adminConsentDisplayName"] != "Access Original" {
		t.Errorf("new scope = %v", newScope)
	}
	api["oauth2PermissionScopes"] = scopes[:len(scopes)-1]
	var want obj
	if err := json.Unmarshal([]byte(apiJSON), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(api, want) {
		got, _ := json.Marshal(api)
		t.Errorf("api not preserved:\n got %s\nwant %s", got, apiJSON)
	}
	if out := tio.stdout.String(); !strings.Contains(out, "Application ID URI: api://first/\nScope (full name):  api://first/read\n") {
		t.Errorf("stdout = %q", out)
	}
}

// expose-api makes the registration an API: its access tokens must be v2 and
// carry auth_time. optionalClaims is replaced whole, so existing claims stay.
func TestExposeSetsV2AndAuthTimeKeepingClaims(t *testing.T) {
	cases := map[string]struct{ claims, want string }{
		"none": {``, `{"accessToken":[{"name":"auth_time","essential":false,"additionalProperties":[]}],"idToken":[],"saml2Token":[]}`},
		"existing": {`,"optionalClaims":{"accessToken":[{"name":"email","essential":true,"additionalProperties":[]}],"idToken":[{"name":"upn","additionalProperties":[]}],"saml2Token":[]}`,
			`{"accessToken":[{"name":"email","essential":true,"additionalProperties":[]},{"name":"auth_time","essential":false,"additionalProperties":[]}],"idToken":[{"name":"upn","additionalProperties":[]}],"saml2Token":[]}`},
		"already": {`,"optionalClaims":{"accessToken":[{"name":"auth_time","essential":true,"additionalProperties":[]}],"idToken":[],"saml2Token":[]}`,
			`{"accessToken":[{"name":"auth_time","essential":true,"additionalProperties":[]}],"idToken":[],"saml2Token":[]}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := graphtest.New(t,
				raw(200, `{"id":"object","appId":"client","displayName":"Original","api":{"requestedAccessTokenVersion":1}`+c.claims+`}`),
				raw(204, ""))
			if err := exposeAPI(context.Background(), options(t, exposeArgs("object")...), f.Client(t), newTestIO(t, "").env); err != nil {
				t.Fatal(err)
			}
			patch := decode(t, f.Requests()[1])
			if v := patch["api"].(obj)["requestedAccessTokenVersion"]; v != float64(2) {
				t.Errorf("requestedAccessTokenVersion = %v", v)
			}
			var want obj
			if err := json.Unmarshal([]byte(c.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(patch["optionalClaims"], any(want)) {
				got, _ := json.Marshal(patch["optionalClaims"])
				t.Errorf("optionalClaims:\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

// assertExposeFailsBeforePatch runs expose-api against a single GET reply and
// requires a runtime error with no PATCH sent.
func assertExposeFailsBeforePatch(t *testing.T, appJSON string) {
	t.Helper()
	f := graphtest.New(t, raw(200, appJSON))
	err := exposeAPI(context.Background(), options(t, exposeArgs("object")...), f.Client(t), newTestIO(t, "").env)
	if err == nil || isUsage(err) {
		t.Errorf("err = %v, want a runtime error", err)
	}
	if requests := f.Requests(); len(requests) != 1 || requests[0].Method != "GET" {
		t.Errorf("requests = %+v", requests)
	}
}

func TestExposeDuplicateOrMalformedAPINeverPatches(t *testing.T) {
	for _, api := range []string{
		// An "api" that is a JSON string is not tested: Kiota parses it
		// silently as an absent api, indistinguishable from null.
		`{"oauth2PermissionScopes":[{"value":"READ"}]}`,
		`{"oauth2PermissionScopes":{}}`,
		`{"oauth2PermissionScopes":[{"value":123}]}`,
	} {
		t.Run(api, func(t *testing.T) {
			assertExposeFailsBeforePatch(t, `{"id":"object","appId":"client","api":`+api+`}`)
		})
	}
}

func TestExposeRejectsMalformedIdentifierURICollectionsBeforePatch(t *testing.T) {
	for _, uris := range []string{`"api://existing"`, `{}`, `42`, `false`} {
		t.Run(uris, func(t *testing.T) {
			assertExposeFailsBeforePatch(t, `{"id":"object","appId":"client","identifierUris":`+uris+`}`)
		})
	}
}

func TestExposeMissingURIUsesClientIDAndNullNameUsesReference(t *testing.T) {
	f := graphtest.New(t,
		raw(200, `{"id":"object","appId":"client","displayName":null,"api":null,"identifierUris":[]}`),
		raw(204, ""))
	if err := exposeAPI(context.Background(), options(t, exposeArgs("reference")...), f.Client(t), newTestIO(t, "").env); err != nil {
		t.Fatal(err)
	}
	patch := decode(t, f.Requests()[1])
	if got := patch["identifierUris"]; !reflect.DeepEqual(got, arr{"api://client"}) {
		t.Errorf("identifierUris = %v", got)
	}
	scope := patch["api"].(obj)["oauth2PermissionScopes"].(arr)[0].(obj)
	if scope["adminConsentDisplayName"] != "Access reference" {
		t.Errorf("adminConsentDisplayName = %v", scope["adminConsentDisplayName"])
	}
}

func TestExposeOutputIsExact(t *testing.T) {
	f := graphtest.New(t, ok(obj{"id": "object", "appId": "client", "displayName": "App"}), raw(204, ""))
	tio := newTestIO(t, "")
	if err := exposeAPI(context.Background(), options(t, "expose-api", "--appid", "object", "--scope-name", "read", "--scope-consent", "users", "--scope-state", "disabled"), f.Client(t), tio.env); err != nil {
		t.Fatal(err)
	}
	scopeID, _ := decode(t, f.Requests()[1])["api"].(obj)["oauth2PermissionScopes"].(arr)[0].(obj)["id"].(string)
	want := "Looking up application object\n" +
		"Adding scope 'api://client/read' to 'App' (Admins and users, Disabled)\n" +
		"AUTH_APP_ID=object\n" +
		"AUTH_CLIENT_ID=client\n" +
		"Application ID URI: api://client\n" +
		"Scope (full name):  api://client/read\n" +
		"Scope ID:           " + scopeID + "\n" +
		"Scope consent:      Admins and users\n" +
		"Scope state:        Disabled\n"
	if got := tio.stdout.String(); got != want {
		t.Errorf("stdout:\n got %q\nwant %q", got, want)
	}
}
