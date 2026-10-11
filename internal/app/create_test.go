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
				graphtest.JSON(201, obj{"id": "object", "appId": "client"}),
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
			err := create(context.Background(), options(t, workflowArgs...), f.Client(t), "tenant", tio.env)

			requests := f.Requests()
			if requests[0].Method != "GET" {
				t.Fatalf("first request %s, want the name check", requests[0].Method)
			}
			requests = requests[1:]
			if requests[0].Method != "POST" || requests[0].Path != "/v1.0/applications" {
				t.Fatalf("request = %s %s", requests[0].Method, requests[0].Path)
			}
			if requests[1].Method != "PATCH" || requests[1].Path != "/v1.0/applications/object" {
				t.Fatalf("request = %s %s", requests[1].Method, requests[1].Path)
			}
			post, patch := decode(t, requests[0]), decode(t, requests[1])
			wantRedirects := arr{"https://example.com/Callback", "https://example.com/callback"}
			if got := post["spa"].(obj)["redirectUris"]; !reflect.DeepEqual(got, wantRedirects) {
				t.Errorf("redirectUris = %v", got)
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

			stdout := strings.ReplaceAll(tio.stdout.String(), "\r\n", "\n")
			if !strings.Contains(stdout, "AUTH_APP_ID=object\nAUTH_CLIENT_ID=client\n") {
				t.Errorf("stdout lacks IDs:\n%s", stdout)
			}
			reportPath := filepath.Join(tio.env.ReportDir, "App.txt")
			switch scenario {
			case "patch-failure":
				if err == nil || !strings.Contains(err.Error(), "scope-denied") {
					t.Fatalf("err = %v", err)
				}
				if len(requests) != 2 {
					t.Errorf("%d requests after the name check, want 2", len(requests))
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
				var usage *cli.UsageError
				if errors.As(err, &usage) {
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
				if len(requests) != 3 {
					t.Fatalf("%d requests after the name check, want 3", len(requests))
				}
				if requests[2].Method != "POST" || requests[2].Path != "/v1.0/applications/object/addPassword" {
					t.Fatalf("request = %s %s", requests[2].Method, requests[2].Path)
				}
				credential := decode(t, requests[2])["passwordCredential"].(obj)
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

func TestCreateWithoutRedirectsSendsEmptyRedirectList(t *testing.T) {
	f := graphtest.New(t, ok(obj{"value": arr{}}), raw(403, "creation-denied"))
	err := create(context.Background(), options(t, "--name", "App"), f.Client(t), "tenant", newTestIO(t, "").env)
	if err == nil || !strings.Contains(err.Error(), "creation-denied") {
		t.Fatalf("err = %v", err)
	}
	requests := f.Requests()
	if len(requests) != 2 || requests[1].Method != "POST" || requests[1].Path != "/v1.0/applications" {
		t.Fatalf("requests = %+v", requests)
	}
	if !strings.Contains(string(requests[1].Body), `"redirectUris":[]`) {
		t.Errorf("body = %s", requests[1].Body)
	}
	if got := decode(t, requests[1])["spa"].(obj)["redirectUris"]; !reflect.DeepEqual(got, arr{}) {
		t.Errorf("redirectUris = %v", got)
	}
}

func TestCreateBlocksDuplicateNamesAndFailedChecks(t *testing.T) {
	for i, reply := range []graphtest.Response{
		ok(obj{"value": arr{obj{"id": "existing"}}}),
		ok(obj{}),
		raw(403, "lookup-denied"),
	} {
		f := graphtest.New(t, reply)
		err := create(context.Background(), options(t, "--name", "O'Brien & Co"), f.Client(t), "tenant", newTestIO(t, "").env)
		if err == nil {
			t.Fatalf("case %d: created", i)
		}
		var usage *cli.UsageError
		if isUsage := errors.As(err, &usage); isUsage != (i == 0) {
			t.Errorf("case %d: usage error = %v (%v)", i, isUsage, err)
		}
		requests := f.Requests()
		if len(requests) != 1 || requests[0].Method != "GET" {
			t.Fatalf("case %d: requests = %+v", i, requests)
		}
		query, err := url.ParseQuery(requests[0].RawQuery)
		if err != nil {
			t.Fatal(err)
		}
		if got := query.Get("$filter"); got != "displayName eq 'O''Brien & Co'" {
			t.Errorf("case %d: $filter = %q", i, got)
		}
	}
}

func TestCreateNameCheckFollowsPagesAndRejectsCycles(t *testing.T) {
	f := graphtest.New(t,
		ok(obj{"value": arr{}, "@odata.nextLink": "{base}/applications?page=2"}),
		ok(obj{"value": arr{obj{"id": "existing"}}}))
	err := create(context.Background(), options(t, "--name", "App"), f.Client(t), "tenant", newTestIO(t, "").env)
	var usage *cli.UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("err = %v, want a usage error", err)
	}
	requests := f.Requests()
	if len(requests) != 2 {
		t.Fatalf("%d requests, want 2", len(requests))
	}
	for _, r := range requests {
		if r.Method == "POST" {
			t.Error("created despite a match on page 2")
		}
	}

	f = graphtest.New(t,
		ok(obj{"value": arr{}, "@odata.nextLink": "{base}/applications?page=2"}),
		ok(obj{"value": arr{}, "@odata.nextLink": "{base}/applications?page=2"}))
	err = create(context.Background(), options(t, "--name", "App"), f.Client(t), "tenant", newTestIO(t, "").env)
	if err == nil || !strings.Contains(err.Error(), "repeated next-page link") {
		t.Fatalf("err = %v", err)
	}
	for _, r := range f.Requests() {
		if r.Method == "POST" {
			t.Error("created despite a page cycle")
		}
	}
}

func TestCreateExistingAppIDIsNoopAndTwo404sCreate(t *testing.T) {
	tio := newTestIO(t, "")
	f := graphtest.New(t, ok(obj{"id": "object", "appId": "client"}))
	if err := create(context.Background(), options(t, "--appid", "existing", "--name", "App"), f.Client(t), "tenant", tio.env); err != nil {
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

	f = graphtest.New(t, raw(404, "missing"), raw(404, "missing"), ok(obj{"value": arr{}}), raw(403, "creation-denied"))
	err := create(context.Background(), options(t, "--appid", "missing", "--name", "App"), f.Client(t), "tenant", newTestIO(t, "").env)
	if err == nil || !strings.Contains(err.Error(), "creation-denied") {
		t.Fatalf("err = %v", err)
	}
	requests := f.Requests()
	if len(requests) != 4 || requests[3].Method != "POST" || requests[3].Path != "/v1.0/applications" {
		t.Fatalf("requests = %+v", requests)
	}
}
