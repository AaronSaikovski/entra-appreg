package app

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
)

var workflowArgs = []string{
	"--name", "App",
	"--redirect-urls", "https://example.com/Callback,https://example.com/callback,https://example.com/Callback",
	"--audience", "PersonalMicrosoftAccount",
	"--scope-name", "read", "--scope-consent", "users",
	"--create-secret", "--secret-expiry", "90",
}

func readReport(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func dirEntries(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// makeUnwritable makes dir read-only and reports whether that actually
// prevents file creation (it does not when running as root).
func makeUnwritable(t *testing.T, dir string) bool {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "probe")
	if f, err := os.Create(probe); err == nil {
		_ = f.Close()
		_ = os.Remove(probe)
		_ = os.Chmod(dir, 0o700)
		return false
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return true
}

func TestCreateWorkflowsPreserveIDsAndSecretRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "patch-failure", "secret-missing-key", "report-failure"} {
		t.Run(scenario, func(t *testing.T) {
			tio := newTestIO(t, "")
			if scenario == "report-failure" {
				if runtime.GOOS == "windows" {
					t.Skip("directory permissions cannot block file creation on Windows")
				}
				if !makeUnwritable(t, tio.env.ReportDir) {
					t.Skip("report directory stays writable (running as root)")
				}
			}
			responses := []graphtest.Response{
				ok(obj{"value": arr{}}),
				org,
				graphtest.JSON(201, obj{"id": "object", "appId": "client"}),
				graphtest.JSON(201, obj{"id": "sp", "appId": "client"}),
			}
			switch scenario {
			case "patch-failure":
				responses = append(responses, raw(403, "scope-denied"))
			case "secret-missing-key":
				responses = append(responses, raw(204, ""), ok(obj{"secretText": "fake-one-time-secret"}))
			default:
				responses = append(responses, raw(204, ""), ok(obj{"keyId": keyID, "secretText": "fake-one-time-secret"}))
			}
			f := graphtest.New(t, responses...)
			err := create(context.Background(), options(t, workflowArgs...), f.Client(t), tio.env)

			requests := f.Requests()
			if requests[0].Method != "GET" || requests[0].Path != "/v1.0/applications" {
				t.Fatalf("first request %s %s, want the name check", requests[0].Method, requests[0].Path)
			}
			assertOrgRequest(t, requests[1])
			requests = requests[2:]
			if requests[0].Method != "POST" || requests[0].Path != "/v1.0/applications" {
				t.Fatalf("request = %s %s", requests[0].Method, requests[0].Path)
			}
			if requests[1].Method != "POST" || requests[1].Path != "/v1.0/servicePrincipals" {
				t.Fatalf("request = %s %s, want the service principal", requests[1].Method, requests[1].Path)
			}
			if got := decode(t, requests[1])["appId"]; got != "client" {
				t.Errorf("service principal body = %v", got)
			}
			if requests[2].Method != "PATCH" || requests[2].Path != "/v1.0/applications/object" {
				t.Fatalf("request = %s %s", requests[2].Method, requests[2].Path)
			}
			post, patch := decode(t, requests[0]), decode(t, requests[2])
			wantRedirects := arr{"https://example.com/Callback", "https://example.com/callback"}
			if got := post["spa"].(obj)["redirectUris"]; !reflect.DeepEqual(got, wantRedirects) {
				t.Errorf("redirectUris = %v", got)
			}
			userRead := obj{"resourceAppId": msGraphAppID, "resourceAccess": arr{obj{"id": userReadScopeID, "type": "Scope"}}}
			if got := post["requiredResourceAccess"]; !reflect.DeepEqual(got, arr{userRead}) {
				t.Errorf("POST requiredResourceAccess = %v", got)
			}
			if got := post["api"].(obj)["requestedAccessTokenVersion"]; got != 2.0 {
				t.Errorf("POST requestedAccessTokenVersion = %v", got)
			}
			api := patch["api"].(obj)
			if got := api["requestedAccessTokenVersion"]; got != 2.0 {
				t.Errorf("PATCH requestedAccessTokenVersion = %v", got)
			}
			scope := api["oauth2PermissionScopes"].(arr)[0].(obj)
			if scope["type"] != "User" {
				t.Errorf("scope type = %v", scope["type"])
			}
			if got := patch["identifierUris"]; !reflect.DeepEqual(got, arr{"api://client"}) {
				t.Errorf("identifierUris = %v", got)
			}
			// The PATCH replaces the whole list: Graph User.Read must survive
			// next to the app's own new scope.
			ownScope := obj{"resourceAppId": "client", "resourceAccess": arr{obj{"id": scope["id"], "type": "Scope"}}}
			if got := patch["requiredResourceAccess"]; !reflect.DeepEqual(got, arr{userRead, ownScope}) {
				t.Errorf("PATCH requiredResourceAccess = %v", got)
			}

			stdout := strings.ReplaceAll(tio.stdout.String(), "\r\n", "\n")
			if !strings.Contains(stdout, "AUTH_APP_ID=object\nAUTH_CLIENT_ID=client\n") || !strings.Contains(stdout, "AUTH_SP_ID=sp\n") {
				t.Errorf("stdout lacks IDs:\n%s", stdout)
			}
			reportPath := filepath.Join(tio.env.ReportDir, "App.txt")
			switch scenario {
			case "patch-failure":
				if err == nil || !strings.Contains(err.Error(), "scope-denied") {
					t.Fatalf("err = %v", err)
				}
				if len(requests) != 3 {
					t.Errorf("%d requests after the name check, want 3", len(requests))
				}
				if n := dirEntries(t, tio.env.ReportDir); n != 0 {
					t.Errorf("%d files written", n)
				}
			case "report-failure":
				if err == nil {
					t.Fatal("report failure was not returned")
				}
				if !strings.Contains(stdout, "Secret value:            fake-one-time-secret") {
					t.Errorf("secret not recovered on stdout:\n%s", stdout)
				}
				if !strings.Contains(tio.stderr.String(), "Could not write the output file:") {
					t.Errorf("stderr = %q", tio.stderr.String())
				}
				if n := dirEntries(t, tio.env.ReportDir); n != 0 {
					t.Errorf("%d files written", n)
				}
			case "secret-missing-key":
				if err == nil || !strings.Contains(err.Error(), "keyId") {
					t.Fatalf("err = %v", err)
				}
				if isUsage(err) {
					t.Error("missing keyId must be a runtime error")
				}
				report := readReport(t, reportPath)
				for _, want := range []string{
					"Secret value:            fake-one-time-secret",
					"Secret ID:               (not returned by Graph)",
				} {
					if !strings.Contains(report, want) {
						t.Errorf("report lacks %q:\n%s", want, report)
					}
				}
				if !strings.Contains(stdout, "Wrote details to ") {
					t.Errorf("stdout lacks the report path:\n%s", stdout)
				}
			case "success":
				if err != nil {
					t.Fatal(err)
				}
				if len(requests) != 4 {
					t.Fatalf("%d requests after the name check, want 4", len(requests))
				}
				if requests[3].Method != "POST" || requests[3].Path != "/v1.0/applications/object/addPassword" {
					t.Fatalf("request = %s %s", requests[3].Method, requests[3].Path)
				}
				credential := decode(t, requests[3])["passwordCredential"].(obj)
				if _, present := credential["startDateTime"]; present {
					t.Error("startDateTime sent for a fixed lifetime")
				}
				if credential["displayName"] != "App secret" {
					t.Errorf("displayName = %v", credential["displayName"])
				}
				scopeID, _ := scope["id"].(string)
				if _, err := uuid.Parse(scopeID); err != nil {
					t.Errorf("scope id %q: %v", scopeID, err)
				}
				report := readReport(t, reportPath)
				for _, want := range []string{
					"Scope ID:                " + scopeID,
					"Secret ID:               " + keyID,
					"Secret value:            fake-one-time-secret",
					"Secret starts:           now",
					"Secret expires:          2028-05-28T16:30:45Z",
					"Directory (tenant) ID:   tenant",
					"https://example.com/Callback\n                         https://example.com/callback",
				} {
					if !strings.Contains(report, want) {
						t.Errorf("report lacks %q:\n%s", want, report)
					}
				}
			}
			if scenario != "report-failure" && strings.Contains(stdout, "fake-one-time-secret") {
				t.Errorf("secret leaked to stdout:\n%s", stdout)
			}
		})
	}
}

func TestCreateWithoutRedirectsSendsEmptyRedirectListAndWarns(t *testing.T) {
	tio := newTestIO(t, "")
	f := graphtest.New(t, ok(obj{"value": arr{}}), org, raw(403, "creation-denied"))
	err := create(context.Background(), options(t, "--name", "App"), f.Client(t), tio.env)
	if err == nil || !strings.Contains(err.Error(), "creation-denied") {
		t.Fatalf("err = %v", err)
	}
	requests := f.Requests()
	if len(requests) != 3 || requests[2].Method != "POST" || requests[2].Path != "/v1.0/applications" {
		t.Fatalf("requests = %+v", requests)
	}
	assertOrgRequest(t, requests[1])
	if !strings.Contains(string(requests[2].Body), `"redirectUris":[]`) {
		t.Errorf("body = %s", requests[2].Body)
	}
	if got := decode(t, requests[2])["spa"].(obj)["redirectUris"]; !reflect.DeepEqual(got, arr{}) {
		t.Errorf("redirectUris = %v", got)
	}
	if got := tio.stderr.String(); got != noRedirectNotice+"\n" {
		t.Errorf("stderr = %q", got)
	}
	if strings.Contains(tio.stdout.String(), "Warning") {
		t.Errorf("warning on stdout: %q", tio.stdout.String())
	}
}

// A failed service principal POST stops create before the scope and secret,
// keeps the printed app IDs, and tells the operator how to finish by hand.
func TestCreateServicePrincipalFailureStopsWithRecoveryHint(t *testing.T) {
	tio := newTestIO(t, "")
	f := graphtest.New(t,
		ok(obj{"value": arr{}}),
		org,
		graphtest.JSON(201, obj{"id": "object", "appId": "client"}),
		raw(400, "sp-replication-lag"))
	err := create(context.Background(), options(t, workflowArgs...), f.Client(t), tio.env)
	if err == nil || !strings.Contains(err.Error(), "sp-replication-lag") || isUsage(err) {
		t.Fatalf("err = %v", err)
	}
	requests := f.Requests()
	if len(requests) != 4 || requests[3].Path != "/v1.0/servicePrincipals" {
		t.Fatalf("requests = %+v", requests)
	}
	if !strings.Contains(tio.stdout.String(), "AUTH_APP_ID=object\nAUTH_CLIENT_ID=client\n") {
		t.Errorf("stdout lacks IDs: %q", tio.stdout.String())
	}
	if !strings.Contains(tio.stderr.String(), "az ad sp create --id client\n") {
		t.Errorf("stderr lacks recovery hint: %q", tio.stderr.String())
	}
	if n := dirEntries(t, tio.env.ReportDir); n != 0 {
		t.Errorf("%d files written", n)
	}
}

func TestCreateBlocksDuplicateNamesAndFailedChecks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reply     graphtest.Response
		wantUsage bool
	}{
		{"duplicate", ok(obj{"value": arr{obj{"id": "existing"}}}), true},
		{"missing value", ok(obj{}), false},
		{"forbidden", raw(403, "lookup-denied"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := graphtest.New(t, tc.reply)
			err := create(context.Background(), options(t, "--name", "O'Brien & Co"), f.Client(t), newTestIO(t, "").env)
			if err == nil {
				t.Fatal("created")
			}
			if got := isUsage(err); got != tc.wantUsage {
				t.Errorf("usage error = %v, want %v (%v)", got, tc.wantUsage, err)
			}
			requests := f.Requests()
			if len(requests) != 1 || requests[0].Method != "GET" {
				t.Fatalf("requests = %+v", requests)
			}
			query, err := url.ParseQuery(requests[0].RawQuery)
			if err != nil {
				t.Fatal(err)
			}
			if got := query.Get("$filter"); got != "displayName eq 'O''Brien & Co'" {
				t.Errorf("$filter = %q", got)
			}
		})
	}
}

func TestCreateNameCheckFollowsPagesAndRejectsCycles(t *testing.T) {
	t.Run("match on page 2", func(t *testing.T) {
		f := graphtest.New(t,
			ok(obj{"value": arr{}, "@odata.nextLink": "{base}/applications?page=2"}),
			ok(obj{"value": arr{obj{"id": "existing"}}}))
		err := create(context.Background(), options(t, "--name", "App"), f.Client(t), newTestIO(t, "").env)
		if !isUsage(err) {
			t.Fatalf("err = %v, want a usage error", err)
		}
		requests := f.Requests()
		if len(requests) != 2 {
			t.Fatalf("%d requests, want 2", len(requests))
		}
		assertNoMethod(t, requests, "POST")
	})
	t.Run("page cycle", func(t *testing.T) {
		f := graphtest.New(t,
			ok(obj{"value": arr{}, "@odata.nextLink": "{base}/applications?page=2"}),
			ok(obj{"value": arr{}, "@odata.nextLink": "{base}/applications?page=2"}))
		err := create(context.Background(), options(t, "--name", "App"), f.Client(t), newTestIO(t, "").env)
		if err == nil || !strings.Contains(err.Error(), "repeated next-page link") {
			t.Fatalf("err = %v", err)
		}
		assertNoMethod(t, f.Requests(), "POST")
	})
}

func TestAudienceMatchesCaseInsensitivelyAndReturnsCanonicalSpelling(t *testing.T) {
	for _, tc := range []struct {
		value *string
		want  string
	}{
		{nil, "AzureADMyOrg"},
		{new(" azureadmultipleorgs "), "AzureADMultipleOrgs"},
		{new("PERSONALMICROSOFTACCOUNT"), "PersonalMicrosoftAccount"},
	} {
		got, err := resolveAudience(tc.value)
		if err != nil || got != tc.want {
			t.Errorf("resolveAudience(%q) = %q, %v; want %q", deref(tc.value, "<nil>"), got, err, tc.want)
		}
	}
}

func TestCreateExistingAppIDIsNoopAndTwo404sCreate(t *testing.T) {
	tio := newTestIO(t, "")
	f := graphtest.New(t, ok(obj{"id": "object", "appId": "client"}))
	if err := create(context.Background(), options(t, "--appid", "existing", "--name", "App"), f.Client(t), tio.env); err != nil {
		t.Fatal(err)
	}
	if n := len(f.Requests()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
	if n := dirEntries(t, tio.env.ReportDir); n != 0 {
		t.Errorf("%d files written", n)
	}
	if !strings.Contains(tio.stdout.String(), "Application already exists, not creating new one.") {
		t.Errorf("stdout = %q", tio.stdout.String())
	}

	f = graphtest.New(t, raw(404, "missing"), raw(404, "missing"), ok(obj{"value": arr{}}), org, raw(403, "creation-denied"))
	err := create(context.Background(), options(t, "--appid", "missing", "--name", "App"), f.Client(t), newTestIO(t, "").env)
	if err == nil || !strings.Contains(err.Error(), "creation-denied") {
		t.Fatalf("err = %v", err)
	}
	requests := f.Requests()
	if len(requests) != 5 || requests[4].Method != "POST" || requests[4].Path != "/v1.0/applications" {
		t.Fatalf("requests = %+v", requests)
	}
	assertOrgRequest(t, requests[3])
}

// TestCreateOutputAndReportAreExact pins every printed line and the report
// layout, so restructuring create cannot reorder or reword them.
func TestCreateOutputAndReportAreExact(t *testing.T) {
	sp := graphtest.JSON(201, obj{"id": "sp", "appId": "client"})
	for _, tc := range []struct {
		name       string
		args       []string
		responses  []graphtest.Response
		wantStdout string // {report} is replaced with the report path
		wantStderr string
		wantReport string // {scope} is replaced with the generated scope ID
	}{
		{
			name: "everything",
			args: workflowArgs,
			responses: []graphtest.Response{
				ok(obj{"value": arr{}}),
				org,
				graphtest.JSON(201, obj{"id": "object", "appId": "client"}),
				sp,
				raw(204, ""),
				ok(obj{"keyId": keyID, "secretText": "fake-one-time-secret"}),
			},
			wantStdout: "Creating application registration 'App'\n" +
				"  Supported account types: PersonalMicrosoftAccount\n" +
				"  Redirect URI (SPA): https://example.com/Callback\n" +
				"  Redirect URI (SPA): https://example.com/callback\n" +
				"  API permission: Microsoft Graph User.Read (delegated)\n" +
				"AUTH_APP_ID=object\n" +
				"AUTH_CLIENT_ID=client\n" +
				"Creating service principal (enterprise application)\n" +
				"AUTH_SP_ID=sp\n" +
				"Exposing API: adding scope 'api://client/read' (Admins and users, Enabled)\n" +
				"Adding client secret 'App secret' to object\n" +
				"Wrote details to {report}\n" +
				"This file contains the client secret in plain text. Keep it out of source control.\n",
			wantReport: "Application name:        App\n" +
				"Application (client) ID: client\n" +
				"Object ID:               object\n" +
				"Service principal ID:    sp\n" +
				"Directory (tenant) ID:   tenant\n" +
				"Supported account types: PersonalMicrosoftAccount\n" +
				"Application ID URI:      api://client\n" +
				"Scope (full name):       api://client/read\n" +
				"Scope ID:                {scope}\n" +
				"Scope display name:      Access App\n" +
				"Scope description:       Allow the application to access App on behalf of the signed-in user.\n" +
				"Scope consent:           Admins and users\n" +
				"Scope state:             Enabled\n" +
				"Scope user display name: Access App\n" +
				"Scope user description:  Allow the application to access App on your behalf.\n" +
				"Secret description:      App secret\n" +
				"Secret ID:               " + keyID + "\n" +
				"Secret value:            fake-one-time-secret\n" +
				"Secret starts:           now\n" +
				"Secret expires:          2028-05-28T16:30:45Z\n" +
				"API permissions:         Microsoft Graph User.Read (delegated)\n" +
				"                         api://client/read (delegated)\n" +
				"Redirect URIs (SPA):     https://example.com/Callback\n" +
				"                         https://example.com/callback\n",
		},
		{
			name: "minimal",
			args: []string{"--name", "App"},
			responses: []graphtest.Response{
				ok(obj{"value": arr{}}),
				org,
				graphtest.JSON(201, obj{"id": "object", "appId": "client"}),
				sp,
			},
			wantStdout: "Creating application registration 'App'\n" +
				"  Supported account types: AzureADMyOrg\n" +
				"  API permission: Microsoft Graph User.Read (delegated)\n" +
				"AUTH_APP_ID=object\n" +
				"AUTH_CLIENT_ID=client\n" +
				"Creating service principal (enterprise application)\n" +
				"AUTH_SP_ID=sp\n" +
				"Skipping API scope (pass --scope-name to expose an API)\n" +
				"Skipping client secret (pass --create-secret to create one)\n" +
				"Wrote details to {report}\n",
			wantStderr: noRedirectNotice + "\n",
			wantReport: "Application name:        App\n" +
				"Application (client) ID: client\n" +
				"Object ID:               object\n" +
				"Service principal ID:    sp\n" +
				"Directory (tenant) ID:   tenant\n" +
				"Supported account types: AzureADMyOrg\n" +
				"API permissions:         Microsoft Graph User.Read (delegated)\n" +
				"Redirect URIs (SPA):     \n",
		},
		{
			name: "custom secret dates from Graph",
			args: []string{"--name", "App", "--create-secret", "--secret-start", "1/3/2028", "--secret-end", "2/3/2028", "--redirect-urls", "http://localhost:5173/"},
			responses: []graphtest.Response{
				ok(obj{"value": arr{}}),
				org,
				graphtest.JSON(201, obj{"id": "object", "appId": "client"}),
				sp,
				ok(obj{
					"keyId": keyID, "secretText": "fake-one-time-secret",
					"startDateTime": "2028-03-01T00:00:00Z", "endDateTime": "2028-03-02T23:59:59.5Z",
				}),
			},
			wantStdout: "Creating application registration 'App'\n" +
				"  Supported account types: AzureADMyOrg\n" +
				"  Redirect URI (SPA): http://localhost:5173/\n" +
				"  API permission: Microsoft Graph User.Read (delegated)\n" +
				"AUTH_APP_ID=object\n" +
				"AUTH_CLIENT_ID=client\n" +
				"Creating service principal (enterprise application)\n" +
				"AUTH_SP_ID=sp\n" +
				"Skipping API scope (pass --scope-name to expose an API)\n" +
				"Adding client secret 'App secret' to object\n" +
				"Wrote details to {report}\n" +
				"This file contains the client secret in plain text. Keep it out of source control.\n",
			wantReport: "Application name:        App\n" +
				"Application (client) ID: client\n" +
				"Object ID:               object\n" +
				"Service principal ID:    sp\n" +
				"Directory (tenant) ID:   tenant\n" +
				"Supported account types: AzureADMyOrg\n" +
				"Secret description:      App secret\n" +
				"Secret ID:               " + keyID + "\n" +
				"Secret value:            fake-one-time-secret\n" +
				"Secret starts:           2028-03-01T00:00:00Z\n" +
				"Secret expires:          2028-03-02T23:59:59.5Z\n" +
				"API permissions:         Microsoft Graph User.Read (delegated)\n" +
				"Redirect URIs (SPA):     http://localhost:5173/\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tio := newTestIO(t, "")
			f := graphtest.New(t, tc.responses...)
			if err := create(context.Background(), options(t, tc.args...), f.Client(t), tio.env); err != nil {
				t.Fatal(err)
			}
			reportPath := filepath.Join(tio.env.ReportDir, "App.txt")
			if got, want := tio.stdout.String(), strings.ReplaceAll(tc.wantStdout, "{report}", reportPath); got != want {
				t.Errorf("stdout:\n got %q\nwant %q", got, want)
			}
			if got := tio.stderr.String(); got != tc.wantStderr {
				t.Errorf("stderr:\n got %q\nwant %q", got, tc.wantStderr)
			}
			scopeID := ""
			if requests := f.Requests(); len(requests) > 4 && requests[4].Method == "PATCH" {
				scopeID, _ = decode(t, requests[4])["api"].(obj)["oauth2PermissionScopes"].(arr)[0].(obj)["id"].(string)
			}
			if got, want := readReport(t, reportPath), strings.ReplaceAll(tc.wantReport, "{scope}", scopeID); got != want {
				t.Errorf("report:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestCreateValidatesInOrderBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--audience", "bogus"}, "--name is required when creating an application."},
		{[]string{"--appid", "no-id", "--name", " "}, "--name is required when creating an application."},
		{[]string{"--name", "App", "--audience", "bogus", "--redirect-urls", "ftp://x"},
			"--audience must be one of AzureADMyOrg, AzureADMultipleOrgs, AzureADandPersonalMicrosoftAccount, PersonalMicrosoftAccount, got: bogus"},
		{[]string{"--name", "App", "--redirect-urls", "https://ok,ftp://x", "--scope-state", "on"},
			"--redirect-urls must contain absolute http(s) URIs, got: ftp://x"},
		{[]string{"--name", "App", "--scope-state", "disabled", "--scope-consent", "users", "--secret-expiry", "90"},
			"--scope-consent, --scope-state require --scope-name."},
		{[]string{"--name", "App", "--scope-name", "read", "--scope-state", "on", "--secret-expiry", "90"},
			"--scope-state must be 'enabled' or 'disabled', got: on"},
		{[]string{"--name", "App", "--secret-expiry", "90"},
			"--secret-expiry, --secret-start and --secret-end require --create-secret."},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			f := graphtest.New(t)
			err := create(context.Background(), options(t, tc.args...), f.Client(t), newTestIO(t, "").env)
			usage, ok := errors.AsType[*cli.UsageError](err)
			if !ok || usage.Message != tc.want {
				t.Errorf("err = %v, want usage error %q", err, tc.want)
			}
			if n := len(f.Requests()); n != 0 {
				t.Errorf("%d requests, want 0", n)
			}
		})
	}
}

// org is the /organization reply create reads the tenant ID from.
var org = ok(obj{"value": arr{obj{"id": "tenant", "displayName": "Contoso"}}})

func assertOrgRequest(t *testing.T, r graphtest.Request) {
	t.Helper()
	if r.Method != "GET" || r.Path != "/v1.0/organization" || r.RawQuery != "%24select=id,displayName" && r.RawQuery != "$select=id,displayName" {
		t.Fatalf("request = %s %s?%s, want the organization lookup", r.Method, r.Path, r.RawQuery)
	}
}

func TestCreateTenantLookupFailureStopsBeforePost(t *testing.T) {
	for name, reply := range map[string]graphtest.Response{
		"forbidden": raw(403, "org-denied"),
		"empty":     ok(obj{"value": arr{}}),
	} {
		t.Run(name, func(t *testing.T) {
			tio := newTestIO(t, "")
			f := graphtest.New(t, ok(obj{"value": arr{}}), reply)
			if err := create(context.Background(), options(t, "--name", "App"), f.Client(t), tio.env); err == nil {
				t.Fatal("created")
			}
			requests := f.Requests()
			if len(requests) != 2 {
				t.Fatalf("requests = %+v", requests)
			}
			assertOrgRequest(t, requests[1])
			assertNoMethod(t, requests, "POST")
			if n := dirEntries(t, tio.env.ReportDir); n != 0 {
				t.Errorf("%d files written", n)
			}
		})
	}
}
