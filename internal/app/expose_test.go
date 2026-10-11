package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
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

func TestExposeDuplicateOrMalformedAPINeverPatches(t *testing.T) {
	for _, api := range []string{
		// An "api" that is a JSON string is not tested: Kiota parses it
		// silently as an absent api, indistinguishable from null.
		`{"oauth2PermissionScopes":[{"value":"READ"}]}`,
		`{"oauth2PermissionScopes":{}}`,
		`{"oauth2PermissionScopes":[{"value":123}]}`,
	} {
		f := graphtest.New(t, raw(200, `{"id":"object","appId":"client","api":`+api+`}`))
		err := exposeAPI(context.Background(), options(t, exposeArgs("object")...), f.Client(t), newTestIO(t, "").env)
		var usage *cli.UsageError
		if err == nil || errors.As(err, &usage) {
			t.Errorf("api %s: err = %v, want a runtime error", api, err)
		}
		if requests := f.Requests(); len(requests) != 1 || requests[0].Method != "GET" {
			t.Errorf("api %s: requests = %+v", api, requests)
		}
	}
}

func TestExposeRejectsMalformedIdentifierURICollectionsBeforePatch(t *testing.T) {
	for _, uris := range []string{`"api://existing"`, `{}`, `42`, `false`} {
		f := graphtest.New(t, raw(200, `{"id":"object","appId":"client","identifierUris":`+uris+`}`))
		err := exposeAPI(context.Background(), options(t, exposeArgs("object")...), f.Client(t), newTestIO(t, "").env)
		var usage *cli.UsageError
		if err == nil || errors.As(err, &usage) {
			t.Errorf("uris %s: err = %v, want a runtime error", uris, err)
		}
		if requests := f.Requests(); len(requests) != 1 || requests[0].Method != "GET" {
			t.Errorf("uris %s: requests = %+v", uris, requests)
		}
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
