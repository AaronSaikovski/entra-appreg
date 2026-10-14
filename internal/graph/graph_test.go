package graph_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// page is an application collection reply with an optional next-page link.
func page(next string) graphtest.Response {
	body := map[string]any{"value": []any{}}
	if next != "" {
		body["@odata.nextLink"] = next
	}
	return graphtest.JSON(200, body)
}

// requestLine renders a recorded request as "METHOD /escaped/path".
func requestLine(r graphtest.Request) string {
	return r.Method + " " + r.Path
}

// assertContains fails the test for each want missing from msg.
func assertContains(t *testing.T, msg string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
}

func TestLookupFallsBackOnlyOnNotFoundAndEncodesODataQuotes(t *testing.T) {
	f := graphtest.New(t, raw(404, "missing"), appJSON())
	app, err := f.Client().GetApplication(t.Context(), "a'b /?#")
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
	if got := requestLine(requests[0]); got != "GET /v1.0/applications/a%27b%20%2F%3F%23" {
		t.Errorf("first request = %q", got)
	}
	if got := requestLine(requests[1]); got != "GET /v1.0/applications(appId='a%27%27b%20%2F%3F%23')" {
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
	app, err := f.Client().GetApplication(t.Context(), "missing")
	if err != nil || app != nil {
		t.Fatalf("got %v, %v; want nil, nil", app, err)
	}
	if n := len(f.Requests()); n != 2 {
		t.Fatalf("got %d requests, want 2", n)
	}
}

func TestLookupNeverFallsBackOnForbiddenOrMalformedSuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   []string
	}{
		{"forbidden", 403, "denied-body", []string{"GET /v1.0/applications/reference", "403", "denied-body"}},
		{"array", 200, "[]", nil},
		{"missing appId", 200, `{"id":"object"}`, []string{"Graph response requires an object with string 'appId'"}},
		{"null id", 200, `{"id":null,"appId":"client"}`, []string{"Graph response requires an object with string 'id'"}},
		{"numeric appId", 200, `{"id":"object","appId":5}`, nil},
		{"not json", 200, "not-json", []string{"GET /v1.0/applications/reference returned an invalid response: "}},
		{"empty", 200, "", []string{"Graph response requires an object with string 'id'"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := graphtest.New(t, raw(tc.status, tc.body))
			app, err := f.Client().GetApplication(t.Context(), "reference")
			if err == nil {
				t.Fatalf("got %v, want error", app)
			}
			if n := len(f.Requests()); n != 1 {
				t.Errorf("got %d requests, want 1", n)
			}
			assertContains(t, err.Error(), tc.want...)
		})
	}
}

const forbiddenBody = `{"error":{"code":"Authorization_RequestDenied","message":"Insufficient privileges to complete the operation.","innerError":{"request-id":"support-request-id"}}}`

func TestForbiddenListPreservesGraphDetailsWithoutRetrying(t *testing.T) {
	f := graphtest.New(t, raw(403, forbiddenBody))
	q := &applications.ApplicationsRequestBuilderGetQueryParameters{
		Select: []string{"id", "appId", "displayName"},
		Top:    new(int32(50)),
	}
	err := f.Client().ApplicationPages(t.Context(), q,
		func(models.ApplicationCollectionResponseable, bool) (bool, error) {
			t.Error("visit called on a failed request")
			return true, nil
		})
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), forbiddenBody, "GET /v1.0/applications?$select=id,appId,displayName&$top=50", "Application.Read.All")
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
	_, err := source.Client().GetApplication(t.Context(), "reference")
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
	source := graphtest.New(t, page(destination.Base+"/applications?page=2"))
	client := source.Client()
	visited := 0
	err := client.ApplicationPages(t.Context(), nil,
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

const untrusted = "Graph returned an untrusted next-page link"

func TestPaginationRestrictsOriginCredentialsAndScheme(t *testing.T) {
	client, err := graph.New(fakeCredential{})
	if err != nil {
		t.Fatal(err)
	}
	const defaultPort = "https://GRAPH.MICROSOFT.COM:443/v1.0/applications?$skiptoken=x"
	if got, err := client.ValidateNextLink(defaultPort); err != nil || got != defaultPort {
		t.Errorf("default-port Graph link: got %q, %v", got, err)
	}
	if got, err := client.ValidateNextLink("https://graph.microsoft.com/v1.0/x/../applications"); err != nil || got != "https://graph.microsoft.com/v1.0/applications" {
		t.Errorf("dot segments not removed: got %q, %v", got, err)
	}
	for _, tc := range []struct{ link, want string }{
		{"/v1.0/applications", "Graph returned an invalid next-page link"},
		{"%zz", "Graph returned an invalid next-page link"},
		{"http://graph.microsoft.com/v1.0/applications", untrusted},
		{"https://graph.microsoft.com:444/v1.0/applications", untrusted},
		{"https://graph.microsoft.com.evil.test/v1.0/applications", untrusted},
		{"https://user@graph.microsoft.com/v1.0/applications", untrusted},
		{"https://user:pass@graph.microsoft.com/v1.0/applications", untrusted},
		{"https://graph.microsoft.com/v1.0/applications#fragment", untrusted},
		{"https://graph.microsoft.com/v1.0/applications#", untrusted},
		{"https://graph.microsoft.com/beta/applications", untrusted},
		{"https://graph.microsoft.com/v1.0/../beta/applications", untrusted},
		{"https://graph.microsoft.com/v1.0", untrusted},
		{"https://graph.micro\u017Foft.com/v1.0/applications", untrusted},
	} {
		if _, err := client.ValidateNextLink(tc.link); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.link, err, tc.want)
		}
	}
	f := graphtest.New(t)
	fixtureClient := f.Client()
	if _, err := fixtureClient.ValidateNextLink(f.Base + "/applications"); err != nil {
		t.Errorf("rejected fixture link: %v", err)
	}
	if _, err := fixtureClient.ValidateNextLink("http://127.0.0.1:1/v1.0/applications"); err == nil {
		t.Error("accepted a link on another port")
	}
}

func TestPatchAcceptsEmptySuccessButPostRequiresJSON(t *testing.T) {
	f := graphtest.New(t, raw(204, ""), raw(201, ""))
	client := f.Client()
	patch := models.NewApplication()
	patch.SetDisplayName(new("new"))
	if err := client.PatchApplication(t.Context(), "object", patch); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateApplication(t.Context(), models.NewApplication()); err == nil || err.Error() != "Graph response requires an object with string 'id'" {
		t.Errorf("POST with an empty reply: got %v", err)
	}
	requests := f.Requests()
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	if got := requestLine(requests[0]); got != "PATCH /v1.0/applications/object" {
		t.Errorf("first request = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(requests[0].Body, &body); err != nil || body["displayName"] != "new" {
		t.Errorf("PATCH body %s (%v) lacks displayName new", requests[0].Body, err)
	}
	if got := requestLine(requests[1]); got != "POST /v1.0/applications" {
		t.Errorf("second request = %q", got)
	}
}

func TestRequestsCarryBearerToken(t *testing.T) {
	f := graphtest.New(t, appJSON())
	if _, err := f.Client().GetApplication(t.Context(), "object"); err != nil {
		t.Fatal(err)
	}
	if got := f.Requests()[0].Authorization; got != "Bearer fake-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestStatusErrorKeepsRawBody(t *testing.T) {
	f := graphtest.New(t, raw(403, forbiddenBody), raw(403, forbiddenBody))
	client := f.Client()
	_, err := client.GetApplication(t.Context(), "reference")
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(),
		"Microsoft Graph denied access (HTTP 403). Your signed-in Azure CLI account does not have permission to perform this operation in the selected tenant.\nReading app registrations requires the delegated Microsoft Graph permission `Application.Read.All`",
		"Owner/Contributor roles do not grant this access.\nCheck the account and tenant with `az account show`.",
		"Signing in again alone does not grant permissions.\nTechnical details: GET /v1.0/applications/reference failed with HTTP 403: "+forbiddenBody,
		"support-request-id",
	)
	err = client.DeleteApplication(t.Context(), "object")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "Application.Read.All") {
		t.Errorf("DELETE 403 includes the read-permission paragraph: %q", msg)
	}
	if !strings.Contains(msg, "tenant.\nCheck the account") || !strings.Contains(msg, "DELETE /v1.0/applications/object failed with HTTP 403: "+forbiddenBody) {
		t.Errorf("unexpected DELETE 403 text %q", msg)
	}
}

func TestStatusErrorBodyIsCappedAtOneMiB(t *testing.T) {
	const limit = 1 << 20
	for _, tc := range []struct {
		name      string
		size      int
		truncated bool
	}{
		{"at cap is kept whole", limit, false},
		{"over cap is truncated and marked", limit + 4096, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Repeat("x", tc.size)
			f := graphtest.New(t, raw(500, body))
			err := f.Client().DeleteApplication(t.Context(), "object")
			var status *graph.StatusError
			if !errors.As(err, &status) {
				t.Fatalf("got %v, want *graph.StatusError", err)
			}
			want := body
			if tc.truncated {
				want = body[:limit] + fmt.Sprintf("\n[response body truncated after %d bytes]", limit)
			}
			if status.Body != want {
				t.Fatalf("body length %d, want %d (truncated=%v)", len(status.Body), len(want), tc.truncated)
			}
		})
	}
}

func TestGenericStatusErrorText(t *testing.T) {
	f := graphtest.New(t, raw(500, "boom"))
	err := f.Client().DeleteApplication(t.Context(), "object")
	if err == nil || err.Error() != "DELETE /v1.0/applications/object failed with HTTP 500: boom" {
		t.Fatalf("got %v", err)
	}
	if graph.IsNotFound(err) {
		t.Error("500 reported as not found")
	}
	if !graph.IsNotFound(fmt.Errorf("wrapped: %w", &graph.StatusError{Status: 404})) {
		t.Error("wrapped 404 not reported as not found")
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
		t.Run(strconv.Itoa(reply.Status), func(t *testing.T) {
			f := graphtest.New(t, reply)
			err := f.Client().DeleteApplication(t.Context(), "object")
			var status *graph.StatusError
			if !errors.As(err, &status) || status.Status != reply.Status || status.Body != reply.Body {
				t.Errorf("got %v", err)
			}
			if n := len(f.Requests()); n != 1 {
				t.Errorf("got %d requests, want 1", n)
			}
		})
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
	created, err := f.Client().CreateApplication(t.Context(), app)
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
		page("{base}/applications?page=2"),
		graphtest.JSON(200, map[string]any{"value": []any{map[string]any{"id": "a"}}}),
	)
	var hasNext []bool
	ids := 0
	err := f.Client().ApplicationPages(t.Context(),
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
	self := graphtest.New(t, page("{base}/applications"))
	visited := 0
	visit := func(models.ApplicationCollectionResponseable, bool) (bool, error) {
		visited++
		return false, nil
	}
	err := self.Client().ApplicationPages(t.Context(), nil, visit)
	if err == nil || !strings.Contains(err.Error(), "repeated next-page link") {
		t.Fatalf("got %v, want repeated next-page link", err)
	}
	if visited != 0 || len(self.Requests()) != 1 {
		t.Errorf("visited %d, requests %d; want 0, 1", visited, len(self.Requests()))
	}

	// Page 2 links back to page 1.
	visited = 0
	cycle := graphtest.New(t, page("{base}/applications?page=2"), page("{base}/applications"))
	err = cycle.Client().ApplicationPages(t.Context(), nil, visit)
	if err == nil || !strings.Contains(err.Error(), "repeated next-page link") {
		t.Fatalf("got %v, want repeated next-page link", err)
	}
	if visited != 1 || len(cycle.Requests()) != 2 {
		t.Errorf("visited %d, requests %d; want 1, 2", visited, len(cycle.Requests()))
	}
}

func TestApplicationPagesStopWhenVisitorStops(t *testing.T) {
	f := graphtest.New(t, page("{base}/applications?page=2"))
	err := f.Client().ApplicationPages(t.Context(), nil,
		func(models.ApplicationCollectionResponseable, bool) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.Requests()); n != 1 {
		t.Fatalf("got %d requests, want 1", n)
	}
	sentinel := errors.New("visitor failed")
	g := graphtest.New(t, page(""))
	err = g.Client().ApplicationPages(t.Context(), nil,
		func(models.ApplicationCollectionResponseable, bool) (bool, error) { return false, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want visitor error", err)
	}
}

func TestApplicationPagesDescribeInvalidReplies(t *testing.T) {
	q := &applications.ApplicationsRequestBuilderGetQueryParameters{Select: []string{"id"}}
	visit := func(models.ApplicationCollectionResponseable, bool) (bool, error) { return false, nil }
	for _, tc := range []struct {
		name      string
		responses []graphtest.Response
		want      string
	}{
		{"first page not json", []graphtest.Response{raw(200, "not-json")},
			"GET /v1.0/applications?$select=id returned an invalid response: "},
		{"next page not json", []graphtest.Response{page("{base}/applications?page=2&%24skiptoken=x"), raw(200, "not-json")},
			"GET /v1.0/applications?page=2&$skiptoken=x returned an invalid response: "},
		{"empty page", []graphtest.Response{raw(200, "")}, "Graph returned an invalid application list."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := graphtest.New(t, tc.responses...)
			err := f.Client().ApplicationPages(t.Context(), q, visit)
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("got %v, want prefix %q", err, tc.want)
			}
		})
	}
}

func TestOrganizationSelectsIDAndDisplayName(t *testing.T) {
	f := graphtest.New(t, graphtest.JSON(200, map[string]any{
		"value": []any{map[string]any{"id": "tenant", "displayName": "Contoso"}},
	}))
	orgs, err := f.Client().Organization(t.Context())
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
	client := f.Client()
	cred := models.NewPasswordCredential()
	cred.SetDisplayName(new("secret"))
	secret, err := client.AddPassword(t.Context(), "object", cred)
	if err != nil {
		t.Fatal(err)
	}
	if *secret.GetSecretText() != "fake-one-time-secret" || secret.GetKeyId() == nil {
		t.Fatalf("unexpected secret reply")
	}
	_, err = client.AddPassword(t.Context(), "object", models.NewPasswordCredential())
	if err == nil || err.Error() != "Graph response requires an object with string 'secretText'" {
		t.Fatalf("got %v", err)
	}
	r := f.Requests()[0]
	if got := requestLine(r); got != "POST /v1.0/applications/object/addPassword" {
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

func TestCreateServicePrincipalPostsAppIDAndRequiresID(t *testing.T) {
	f := graphtest.New(t,
		graphtest.JSON(201, map[string]any{"id": "sp", "appId": "client"}),
		graphtest.JSON(201, map[string]any{"appId": "client"}),
		raw(400, "not replicated"))
	client := f.Client()
	if id, err := client.CreateServicePrincipal(t.Context(), "client"); err != nil || id != "sp" {
		t.Fatalf("got %q, %v", id, err)
	}
	if _, err := client.CreateServicePrincipal(t.Context(), "client"); err == nil || err.Error() != "Graph response requires an object with string 'id'" {
		t.Errorf("reply without id: got %v", err)
	}
	if _, err := client.CreateServicePrincipal(t.Context(), "client"); err == nil || err.Error() != "POST /v1.0/servicePrincipals failed with HTTP 400: not replicated" {
		t.Errorf("status error: got %v", err)
	}
	r := f.Requests()[0]
	if r.Method != "POST" || r.Path != "/v1.0/servicePrincipals" || !strings.Contains(string(r.Body), `"appId":"client"`) {
		t.Errorf("request = %s %s %s", r.Method, r.Path, r.Body)
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
		"https://graph.micro\u017Foft.com/v1.0",
		"http://localhost:8080/v1.0",
		"http://10.0.0.1/v1.0",
		"https://127.0.0.1/v1.0",
		"ftp://127.0.0.1/v1.0",
		"graph.microsoft.com/v1.0",
		"http://127.0.0.1/v1.0?",
		"%zz",
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

func TestNewForBaseTrimsTrailingSlash(t *testing.T) {
	f := graphtest.New(t, raw(204, ""))
	client, err := graph.NewForBase(fakeCredential{}, f.Base+"/")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteApplication(t.Context(), "object"); err != nil {
		t.Fatal(err)
	}
	if got := requestLine(f.Requests()[0]); got != "DELETE /v1.0/applications/object" {
		t.Errorf("request = %q", got)
	}
	if _, err := client.ValidateNextLink(f.Base + "/applications"); err != nil {
		t.Errorf("rejected link under the normalized base: %v", err)
	}
}

func TestTransportFailureNamesRequest(t *testing.T) {
	client, err := graph.NewForBase(fakeCredential{}, "http://127.0.0.1:1/v1.0")
	if err != nil {
		t.Fatal(err)
	}
	err = client.DeleteApplication(t.Context(), "object")
	if err == nil || !strings.HasPrefix(err.Error(), "DELETE /v1.0/applications/object failed: ") {
		t.Fatalf("got %v", err)
	}
}
