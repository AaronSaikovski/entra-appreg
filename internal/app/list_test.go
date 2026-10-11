package app

import (
	"bytes"
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
)

func ids(apps []listedApp) []string {
	out := make([]string, len(apps))
	for i, app := range apps {
		out[i] = app.ID
	}
	return out
}

func TestListPagesFilterSortStablyAndLimitBeforeSorting(t *testing.T) {
	f := graphtest.New(t,
		ok(obj{"value": arr{obj{"displayName": "Zulu", "appId": "c1", "id": "o1"}}, "@odata.nextLink": "{base}/next"}),
		ok(obj{"value": arr{
			obj{"displayName": "alpha", "appId": "c2", "id": "o2"},
			obj{"displayName": "ALPHA", "appId": "c3", "id": "o3"},
			obj{"displayName": "Aardvark", "appId": "c4", "id": "o4"},
		}}))
	apps, more, err := collectApplications(context.Background(), options(t, "list", "--name", " O'Brien ", "--top", "3"), f.Client(t))
	if err != nil {
		t.Fatal(err)
	}
	if !more || !reflect.DeepEqual(ids(apps), []string{"o2", "o3", "o1"}) {
		t.Errorf("apps %v more %v", ids(apps), more)
	}
	requests := f.Requests()
	if len(requests) != 2 {
		t.Fatalf("%d requests, want 2", len(requests))
	}
	query, err := url.ParseQuery(requests[0].RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	if got := query.Get("$filter"); got != "startswith(displayName,'O''Brien')" {
		t.Errorf("$filter = %q", got)
	}
	if got := query.Get("$top"); got != "3" {
		t.Errorf("$top = %q", got)
	}
	if requests[1].Method != "GET" || requests[1].Path != "/v1.0/next" {
		t.Errorf("second request = %s %s", requests[1].Method, requests[1].Path)
	}
}

func TestListCountsNextLinkTruncationAndAcceptsNullNames(t *testing.T) {
	f := graphtest.New(t, raw(200, `{"value":[{"displayName":null,"appId":"client","id":"object"}],"@odata.nextLink":"{base}/next"}`))
	apps, more, err := collectApplications(context.Background(), options(t, "list", "--top", "1"), f.Client(t))
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Error("more = false despite a next-page link")
	}
	if want := []listedApp{{Name: "", ClientID: "client", ID: "object"}}; !reflect.DeepEqual(apps, want) {
		t.Errorf("apps = %+v", apps)
	}
	if n := len(f.Requests()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func TestListRejectsSelfLinksAndTwoPageCycles(t *testing.T) {
	initial := "{base}/applications?%24select=id,appId,displayName&%24top=50"
	for _, tc := range []struct {
		name  string
		pages []graphtest.Response
	}{
		{"self link", []graphtest.Response{ok(obj{"value": arr{}, "@odata.nextLink": initial})}},
		{"two-page cycle", []graphtest.Response{
			ok(obj{"value": arr{}, "@odata.nextLink": "{base}/next"}),
			ok(obj{"value": arr{}, "@odata.nextLink": initial}),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := graphtest.New(t, tc.pages...)
			_, _, err := collectApplications(context.Background(), options(t, "list"), f.Client(t))
			if err == nil || !strings.Contains(err.Error(), "repeated next-page link") {
				t.Fatalf("err = %v", err)
			}
			if n, want := len(f.Requests()), len(tc.pages); n != want {
				t.Errorf("%d requests, want %d", n, want)
			}
		})
	}
}

func TestListValidatesNextLinksEvenAfterReachingLimit(t *testing.T) {
	for _, credentials := range []bool{false, true} {
		link := "https://example.invalid/steal"
		if credentials {
			// Exact origin rules are covered in package graph; here the point is
			// that the link is validated although --top 1 was already reached.
			link = strings.Replace(graphtest.New(t).Base, "http://", "http://user:password@", 1) + "/applications"
		}
		f := graphtest.New(t, ok(obj{"value": arr{obj{"appId": "client", "id": "object"}}, "@odata.nextLink": link}))
		if _, _, err := collectApplications(context.Background(), options(t, "list", "--top", "1"), f.Client(t)); err == nil {
			t.Errorf("credentials %v: untrusted link accepted", credentials)
		}
		if n := len(f.Requests()); n != 1 {
			t.Errorf("credentials %v: %d requests, want 1", credentials, n)
		}
	}
}

func TestListRejectsMalformedPagesAndConsumedRecords(t *testing.T) {
	for _, page := range []string{
		`[]`,
		`{}`,
		`{"value":{}}`,
		`{"value":[null]}`,
		`{"value":[{"id":"object"}]}`,
		`{"value":[{"id":42,"appId":"client"}]}`,
		`{"value":[{"id":"object","appId":"client","displayName":false}]}`,
	} {
		f := graphtest.New(t, raw(200, page))
		if _, _, err := collectApplications(context.Background(), options(t, "list"), f.Client(t)); err == nil {
			t.Errorf("page %s accepted", page)
		}
		if n := len(f.Requests()); n != 1 {
			t.Errorf("page %s: %d requests, want 1", page, n)
		}
	}
}

func TestListRequiresExactlyOneOrganization(t *testing.T) {
	for _, orgs := range []any{
		arr{},
		arr{obj{"id": "t1", "displayName": "One"}, obj{"id": "t2", "displayName": "Two"}},
	} {
		f := graphtest.New(t, ok(obj{"value": orgs}))
		err := list(context.Background(), options(t, "list"), f.Client(t), newTestIO(t, "").env)
		if err == nil || !strings.Contains(err.Error(), "exactly one tenant organization") {
			t.Errorf("orgs %v: err = %v", orgs, err)
		}
		if n := len(f.Requests()); n != 1 {
			t.Errorf("orgs %v: %d requests, want 1", orgs, n)
		}
	}
}

func TestWriteListTextEscapesRemoteNames(t *testing.T) {
	var buf bytes.Buffer
	apps := []listedApp{{Name: "evil\x1b[2Jname\nfake", ClientID: "client", ID: "object"}}
	if err := writeListText(&buf, apps, false, nil); err != nil {
		t.Fatal(err)
	}
	want := "1. \"evil\\x1b[2Jname\\nfake\"\n   Application (client) ID: client\n   Object ID: object\n\n1 app registration(s).\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Error("raw ESC in output")
	}
}

func TestListJSONKeepsStdoutMachineReadable(t *testing.T) {
	f := graphtest.New(t,
		ok(obj{"value": arr{obj{"id": "tenant", "displayName": "Contoso <&>"}}}),
		ok(obj{"value": arr{obj{"displayName": "A&B <x>", "appId": "client", "id": "object"}}}))
	tio := newTestIO(t, "")
	if err := list(context.Background(), options(t, "list", "--json"), f.Client(t), tio.env); err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"displayName\": \"A&B <x>\",\n    \"appId\": \"client\",\n    \"id\": \"object\"\n  }\n]\n"
	if got := tio.stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := tio.stderr.String(); got != "Tenant: \"Contoso <&>\"\nTenant ID: tenant\n" {
		t.Errorf("stderr = %q", got)
	}
}

func TestListOutputIsExact(t *testing.T) {
	tenant := ok(obj{"value": arr{obj{"id": "tenant", "displayName": "Contoso"}}})
	page := ok(obj{"value": arr{obj{"displayName": "App", "appId": "client", "id": "object"}}, "@odata.nextLink": "{base}/next"})
	for _, tc := range []struct {
		name             string
		args             []string
		wantOut, wantErr string
	}{
		{
			name:    "text",
			args:    []string{"list", "--top", "1"},
			wantOut: "Tenant: \"Contoso\"\nTenant ID: tenant\n\n1. \"App\"\n   Application (client) ID: client\n   Object ID: object\n\nShowing the first 1 app registrations; more exist. Use --top <n|all> or --name <text> to see others.\n",
		},
		{
			name:    "json",
			args:    []string{"list", "--top", "1", "--json"},
			wantOut: "[\n  {\n    \"displayName\": \"App\",\n    \"appId\": \"client\",\n    \"id\": \"object\"\n  }\n]\n",
			wantErr: "Tenant: \"Contoso\"\nTenant ID: tenant\nShowing the first 1; more exist. Use --top or --name.\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := graphtest.New(t, tenant, page)
			tio := newTestIO(t, "")
			if err := list(context.Background(), options(t, tc.args...), f.Client(t), tio.env); err != nil {
				t.Fatal(err)
			}
			if got := tio.stdout.String(); got != tc.wantOut {
				t.Errorf("stdout:\n got %q\nwant %q", got, tc.wantOut)
			}
			if got := tio.stderr.String(); got != tc.wantErr {
				t.Errorf("stderr:\n got %q\nwant %q", got, tc.wantErr)
			}
		})
	}
}

func TestWriteListTextEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter *string
		want   string
	}{
		{"unfiltered", nil, "No app registrations found.\n"},
		{"filtered", new(" Con "), "No app registrations found with a name starting with 'Con'.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeListText(&buf, nil, false, tc.filter); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
