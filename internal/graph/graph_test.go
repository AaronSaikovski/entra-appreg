package graph_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/AaronSaikovski/entra-appreg/internal/graph"
	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
)

type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func raw(status int, body string) graphtest.Response {
	return graphtest.Response{Status: status, Body: body}
}

func appJSON() graphtest.Response {
	return graphtest.JSON(200, map[string]any{"id": "object", "appId": "client"})
}

func TestLookupFallsBackOnlyOnNotFoundAndEncodesODataQuotes(t *testing.T) {
	f := graphtest.New(t, raw(404, "missing"), appJSON())
	app, err := f.Client(t).GetApplication(context.Background(), "a'b /?#")
	if err != nil {
		t.Fatal(err)
	}
	if app == nil || *app.GetId() != "object" {
		t.Fatalf("unexpected application %v", app)
	}
	requests := f.Requests()
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	if got := requests[0].Method + " " + requests[0].Path; got != "GET /v1.0/applications/a%27b%20%2F%3F%23" {
		t.Errorf("first request = %q", got)
	}
	if got := requests[1].Method + " " + requests[1].Path; got != "GET /v1.0/applications(appId='a%27%27b%20%2F%3F%23')" {
		t.Errorf("second request = %q", got)
	}
	for _, r := range requests {
		if r.RawQuery != "" {
			t.Errorf("unexpected query %q", r.RawQuery)
		}
	}
}

func TestTwoNotFoundResponsesAreAbsent(t *testing.T) {
	f := graphtest.New(t, raw(404, "first"), raw(404, "second"))
	app, err := f.Client(t).GetApplication(context.Background(), "missing")
	if err != nil || app != nil {
		t.Fatalf("got %v, %v; want nil, nil", app, err)
	}
	if n := len(f.Requests()); n != 2 {
		t.Fatalf("got %d requests, want 2", n)
	}
}

func TestLookupNeverFallsBackOnForbiddenOrMalformedSuccess(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{403, "denied-body"},
		{200, "[]"},
		{200, `{"id":"object"}`},
		{200, `{"id":null,"appId":"client"}`},
		{200, `{"id":"object","appId":5}`},
		{200, "not-json"},
		{200, ""},
	} {
		f := graphtest.New(t, raw(tc.status, tc.body))
		app, err := f.Client(t).GetApplication(context.Background(), "reference")
		if err == nil {
			t.Errorf("%d %q: got %v, want error", tc.status, tc.body, app)
			continue
		}
		if n := len(f.Requests()); n != 1 {
			t.Errorf("%d %q: got %d requests, want 1", tc.status, tc.body, n)
		}
		if tc.status == 403 {
			msg := err.Error()
			for _, want := range []string{"GET /v1.0/applications/reference", "403", "denied-body"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q lacks %q", msg, want)
				}
			}
		}
	}
}

const forbiddenBody = `{"error":{"code":"Authorization_RequestDenied","message":"Insufficient privileges to complete the operation.","innerError":{"request-id":"support-request-id"}}}`

func TestForbiddenListPreservesGraphDetailsWithoutRetrying(t *testing.T) {
	f := graphtest.New(t, raw(403, forbiddenBody))
	q := &applications.ApplicationsRequestBuilderGetQueryParameters{
		Select: []string{"id", "appId", "displayName"},
		Top:    new(int32(50)),
	}
	err := f.Client(t).ApplicationPages(context.Background(), q,
		func(models.ApplicationCollectionResponseable, bool) (bool, error) {
			t.Error("visit called on a failed request")
			return true, nil
		})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{forbiddenBody, "GET /v1.0/applications?$select=id,appId,displayName&$top=50", "Application.Read.All"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	if n := len(f.Requests()); n != 1 {
		t.Fatalf("got %d requests, want 1", n)
	}
}

func TestRedirectsAreErrorsWithoutFollowingLocation(t *testing.T) {
	destination := graphtest.New(t)
	source := graphtest.New(t, graphtest.Response{
		Status: 302,
		Body:   "redirect-body",
		Header: map[string]string{"Location": destination.Base + "/applications/reference"},
	})
	_, err := source.Client(t).GetApplication(context.Background(), "reference")
	var status *graph.StatusError
	if !errors.As(err, &status) || status.Status != 302 {
		t.Fatalf("got %v, want StatusError 302", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "302") || !strings.Contains(msg, "redirect-body") {
		t.Errorf("error %q lacks status or body", msg)
	}
	if n := len(source.Requests()); n != 1 {
		t.Errorf("source got %d requests, want 1", n)
	}
	if n := len(destination.Requests()); n != 0 {
		t.Errorf("destination got %d requests, want 0", n)
	}
}

func TestUnsafePageURLIsRejectedBeforeSending(t *testing.T) {
	destination := graphtest.New(t)
	source := graphtest.New(t, graphtest.JSON(200, map[string]any{
		"value":           []any{},
		"@odata.nextLink": destination.Base + "/applications?page=2",
	}))
	client := source.Client(t)
	visited := 0
	err := client.ApplicationPages(context.Background(), nil,
		func(models.ApplicationCollectionResponseable, bool) (bool, error) {
			visited++
			return false, nil
		})
	if err == nil || !strings.Contains(err.Error(), "untrusted next-page link") {
		t.Fatalf("got %v, want untrusted next-page link", err)
	}
	if visited != 0 {
		t.Errorf("visited %d pages before rejecting the link", visited)
	}
	credentials := strings.Replace(source.Base, "http://", "http://user@", 1) + "/applications"
	if _, err := client.ValidateNextLink(credentials); err == nil {
		t.Errorf("accepted %s", credentials)
	}
	if n := len(source.Requests()); n != 1 {
		t.Errorf("source got %d requests, want 1", n)
	}
	if n := len(destination.Requests()); n != 0 {
		t.Errorf("destination got %d requests, want 0", n)
	}
}

func TestPaginationRestrictsOriginCredentialsAndScheme(t *testing.T) {
	client, err := graph.New(fakeCredential{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ValidateNextLink("https://GRAPH.MICROSOFT.COM:443/v1.0/applications?$skiptoken=x"); err != nil {
		t.Errorf("rejected default-port Graph link: %v", err)
	}
	for _, link := range []string{
		"/v1.0/applications",
		"http://graph.microsoft.com/v1.0/applications",
		"https://graph.microsoft.com:444/v1.0/applications",
		"https://graph.microsoft.com.evil.test/v1.0/applications",
		"https://user@graph.microsoft.com/v1.0/applications",
		"https://user:pass@graph.microsoft.com/v1.0/applications",
		"https://graph.microsoft.com/v1.0/applications#fragment",
		"https://graph.microsoft.com/beta/applications",
		"https://graph.microsoft.com/v1.0/../beta/applications",
		"https://graph.microsoft.com/v1.0",
	} {
		if _, err := client.ValidateNextLink(link); err == nil {
			t.Errorf("accepted %s", link)
		}
	}
	f := graphtest.New(t)
	fixtureClient := f.Client(t)
	if _, err := fixtureClient.ValidateNextLink(f.Base + "/applications"); err != nil {
		t.Errorf("rejected fixture link: %v", err)
	}
	if _, err := fixtureClient.ValidateNextLink("http://127.0.0.1:1/v1.0/applications"); err == nil {
		t.Error("accepted a link on another port")
	}
}

func TestPatchAcceptsEmptySuccessButPostRequiresJSON(t *testing.T) {
	f := graphtest.New(t, raw(204, ""), raw(201, ""))
	client := f.Client(t)
	patch := models.NewApplication()
	patch.SetDisplayName(new("new"))
	if err := client.PatchApplication(context.Background(), "object", patch); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateApplication(context.Background(), models.NewApplication()); err == nil {
		t.Error("POST with an empty reply succeeded")
	}
	requests := f.Requests()
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	if got := requests[0].Method + " " + requests[0].Path; got != "PATCH /v1.0/applications/object" {
		t.Errorf("first request = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(requests[0].Body, &body); err != nil || body["displayName"] != "new" {
		t.Errorf("PATCH body %s (%v) lacks displayName new", requests[0].Body, err)
	}
	if got := requests[1].Method + " " + requests[1].Path; got != "POST /v1.0/applications" {
		t.Errorf("second request = %q", got)
	}
}

func TestRequestsCarryBearerToken(t *testing.T) {
	f := graphtest.New(t, appJSON())
	if _, err := f.Client(t).GetApplication(context.Background(), "object"); err != nil {
		t.Fatal(err)
	}
	if got := f.Requests()[0].Authorization; got != "Bearer fake-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestStatusErrorKeepsRawBody(t *testing.T) {
	f := graphtest.New(t, raw(403, forbiddenBody), raw(403, forbiddenBody))
	client := f.Client(t)
	_, err := client.GetApplication(context.Background(), "reference")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"Microsoft Graph denied access (HTTP 403). Your signed-in Azure CLI account does not have permission to perform this operation in the selected tenant.\nReading app registrations requires the delegated Microsoft Graph permission `Application.Read.All`",
		"Owner/Contributor roles do not grant this access.\nCheck the account and tenant with `az account show`.",
		"Signing in again alone does not grant permissions.\nTechnical details: GET /v1.0/applications/reference failed with HTTP 403: " + forbiddenBody,
		"support-request-id",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	err = client.DeleteApplication(context.Background(), "object")
	if err == nil {
		t.Fatal("expected error")
	}
	msg = err.Error()
	if strings.Contains(msg, "Application.Read.All") {
		t.Errorf("DELETE 403 includes the read-permission paragraph: %q", msg)
	}
	if !strings.Contains(msg, "tenant.\nCheck the account") || !strings.Contains(msg, "DELETE /v1.0/applications/object failed with HTTP 403: "+forbiddenBody) {
		t.Errorf("unexpected DELETE 403 text %q", msg)
	}
}

func TestGenericStatusErrorText(t *testing.T) {
	f := graphtest.New(t, raw(500, "boom"))
	err := f.Client(t).DeleteApplication(context.Background(), "object")
	if err == nil || err.Error() != "DELETE /v1.0/applications/object failed with HTTP 500: boom" {
		t.Fatalf("got %v", err)
	}
	if graph.IsNotFound(err) {
		t.Error("500 reported as not found")
	}
}

func TestFailuresAreNotRetried(t *testing.T) {
	for _, reply := range []graphtest.Response{
		{Status: 429, Body: "throttled", Header: map[string]string{"Retry-After": "0"}},
		{Status: 503, Body: "unavailable", Header: map[string]string{"Retry-After": "0"}},
		{Status: 504, Body: "timeout"},
		{Status: 401, Body: "claims", Header: map[string]string{
			"WWW-Authenticate": `Bearer realm="", error="insufficient_claims", claims="eyJhY2Nlc3NfdG9rZW4iOnt9fQ=="`,
		}},
	} {
		f := graphtest.New(t, reply)
		err := f.Client(t).DeleteApplication(context.Background(), "object")
		var status *graph.StatusError
		if !errors.As(err, &status) || status.Status != reply.Status || status.Body != reply.Body {
			t.Errorf("%d: got %v", reply.Status, err)
		}
		if n := len(f.Requests()); n != 1 {
			t.Errorf("%d: got %d requests, want 1", reply.Status, n)
		}
	}
}

func TestRequestBodiesAreNotCompressed(t *testing.T) {
	f := graphtest.New(t, graphtest.JSON(201, map[string]any{"id": "object", "appId": "client"}))
	app := models.NewApplication()
	app.SetDisplayName(new("App"))
	app.SetSignInAudience(new("AzureADMyOrg"))
	spa := models.NewSpaApplication()
	spa.SetRedirectUris([]string{})
	app.SetSpa(spa)
	created, err := f.Client(t).CreateApplication(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	if *created.GetAppId() != "client" {
		t.Errorf("appId = %q", *created.GetAppId())
	}
	body := f.Requests()[0].Body
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("POST body is not plain JSON: %v (%q)", err, body)
	}
	if decoded["displayName"] != "App" {
		t.Errorf("POST body %s lacks displayName", body)
	}
	spaBody, _ := decoded["spa"].(map[string]any)
	if uris, ok := spaBody["redirectUris"].([]any); !ok || len(uris) != 0 {
		t.Errorf("POST body %s lacks empty spa.redirectUris", body)
	}
}

func TestApplicationPagesFollowValidatedLinks(t *testing.T) {
	f := graphtest.New(t,
		graphtest.JSON(200, map[string]any{"value": []any{}, "@odata.nextLink": "{base}/applications?page=2"}),
		graphtest.JSON(200, map[string]any{"value": []any{map[string]any{"id": "a"}}}),
	)
	var hasNext []bool
	ids := 0
	err := f.Client(t).ApplicationPages(context.Background(),
		&applications.ApplicationsRequestBuilderGetQueryParameters{
			Filter: new("displayName eq 'App'"),
			Select: []string{"id"},
			Top:    new(int32(1)),
		},
		func(page models.ApplicationCollectionResponseable, next bool) (bool, error) {
			hasNext = append(hasNext, next)
			ids += len(page.GetValue())
			return false, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(hasNext) != 2 || !hasNext[0] || hasNext[1] || ids != 1 {
		t.Fatalf("hasNext = %v, ids = %d", hasNext, ids)
	}
	requests := f.Requests()
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	if got := requests[0].RawQuery; got != "$filter=displayName%20eq%20%27App%27&$select=id&$top=1" {
		t.Errorf("first query = %q", got)
	}
	if got := requests[1].Path + "?" + requests[1].RawQuery; got != "/v1.0/applications?page=2" {
		t.Errorf("second request = %q", got)
	}
}

func TestApplicationPagesRejectRepeatedLinks(t *testing.T) {
	// Page 1 links to itself: rejected before visiting.
	self := graphtest.New(t,
		graphtest.JSON(200, map[string]any{"value": []any{}, "@odata.nextLink": "{base}/applications"}))
	visited := 0
	visit := func(models.ApplicationCollectionResponseable, bool) (bool, error) {
		visited++
		return false, nil
	}
	err := self.Client(t).ApplicationPages(context.Background(), nil, visit)
	if err == nil || !strings.Contains(err.Error(), "repeated next-page link") {
		t.Fatalf("got %v, want repeated next-page link", err)
	}
	if visited != 0 || len(self.Requests()) != 1 {
		t.Errorf("visited %d, requests %d; want 0, 1", visited, len(self.Requests()))
	}

	// Page 2 links back to page 1.
	visited = 0
	cycle := graphtest.New(t,
		graphtest.JSON(200, map[string]any{"value": []any{}, "@odata.nextLink": "{base}/applications?page=2"}),
		graphtest.JSON(200, map[string]any{"value": []any{}, "@odata.nextLink": "{base}/applications"}))
	err = cycle.Client(t).ApplicationPages(context.Background(), nil, visit)
	if err == nil || !strings.Contains(err.Error(), "repeated next-page link") {
		t.Fatalf("got %v, want repeated next-page link", err)
	}
	if visited != 1 || len(cycle.Requests()) != 2 {
		t.Errorf("visited %d, requests %d; want 1, 2", visited, len(cycle.Requests()))
	}
}

func TestApplicationPagesStopWhenVisitorStops(t *testing.T) {
	f := graphtest.New(t,
		graphtest.JSON(200, map[string]any{"value": []any{}, "@odata.nextLink": "{base}/applications?page=2"}))
	err := f.Client(t).ApplicationPages(context.Background(), nil,
		func(models.ApplicationCollectionResponseable, bool) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.Requests()); n != 1 {
		t.Fatalf("got %d requests, want 1", n)
	}
	sentinel := errors.New("visitor failed")
	g := graphtest.New(t, graphtest.JSON(200, map[string]any{"value": []any{}}))
	err = g.Client(t).ApplicationPages(context.Background(), nil,
		func(models.ApplicationCollectionResponseable, bool) (bool, error) { return false, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want visitor error", err)
	}
}

func TestOrganizationSelectsIDAndDisplayName(t *testing.T) {
	f := graphtest.New(t, graphtest.JSON(200, map[string]any{
		"value": []any{map[string]any{"id": "tenant", "displayName": "Contoso"}},
	}))
	orgs, err := f.Client(t).Organization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v := orgs.GetValue(); len(v) != 1 || *v[0].GetId() != "tenant" || *v[0].GetDisplayName() != "Contoso" {
		t.Fatalf("unexpected organizations %v", v)
	}
	r := f.Requests()[0]
	if got := r.Method + " " + r.Path + "?" + r.RawQuery; got != "GET /v1.0/organization?$select=id,displayName" {
		t.Errorf("request = %q", got)
	}
}

func TestAddPasswordPostsCredentialAndRequiresReply(t *testing.T) {
	f := graphtest.New(t,
		graphtest.JSON(200, map[string]any{"secretText": "fake-one-time-secret", "keyId": "00000000-0000-0000-0000-000000000001"}),
		raw(200, ""))
	client := f.Client(t)
	cred := models.NewPasswordCredential()
	cred.SetDisplayName(new("secret"))
	secret, err := client.AddPassword(context.Background(), "object", cred)
	if err != nil {
		t.Fatal(err)
	}
	if *secret.GetSecretText() != "fake-one-time-secret" || secret.GetKeyId() == nil {
		t.Fatalf("unexpected secret reply")
	}
	_, err = client.AddPassword(context.Background(), "object", models.NewPasswordCredential())
	if err == nil || err.Error() != "Graph response requires an object with string 'secretText'" {
		t.Fatalf("got %v", err)
	}
	r := f.Requests()[0]
	if got := r.Method + " " + r.Path; got != "POST /v1.0/applications/object/addPassword" {
		t.Errorf("request = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatal(err)
	}
	if pc, _ := body["passwordCredential"].(map[string]any); pc["displayName"] != "secret" {
		t.Errorf("body %s lacks passwordCredential.displayName", r.Body)
	}
}

func TestNewForBaseRejectsUntrustedBases(t *testing.T) {
	for _, base := range []string{
		"http://graph.microsoft.com/v1.0",
		"https://graph.microsoft.com:444/v1.0",
		"https://graph.microsoft.com.evil.test/v1.0",
		"https://user@graph.microsoft.com/v1.0",
		"https://graph.microsoft.com/v1.0?x=1",
		"https://graph.microsoft.com/v1.0#fragment",
		"http://localhost:8080/v1.0",
		"http://10.0.0.1/v1.0",
		"https://127.0.0.1/v1.0",
		"ftp://127.0.0.1/v1.0",
		"graph.microsoft.com/v1.0",
	} {
		if _, err := graph.NewForBase(fakeCredential{}, base); err == nil || err.Error() != "Invalid Microsoft Graph base URL" {
			t.Errorf("%s: got %v", base, err)
		}
	}
	for _, base := range []string{
		"https://GRAPH.microsoft.com:443/v1.0/",
		"http://127.0.0.1:8080/v1.0",
		"http://[::1]:8080/v1.0",
	} {
		if _, err := graph.NewForBase(fakeCredential{}, base); err != nil {
			t.Errorf("%s: %v", base, err)
		}
	}
}

func TestTransportFailureNamesRequest(t *testing.T) {
	client, err := graph.NewForBase(fakeCredential{}, "http://127.0.0.1:1/v1.0")
	if err != nil {
		t.Fatal(err)
	}
	err = client.DeleteApplication(context.Background(), "object")
	if err == nil || !strings.HasPrefix(err.Error(), "DELETE /v1.0/applications/object failed: ") {
		t.Fatalf("got %v", err)
	}
}
