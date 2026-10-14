package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
)

func TestDeleteRequiresCompleteExplicitConfirmation(t *testing.T) {
	for _, answer := range []string{"", "\n", "no\n", "y\n", "YES\n", " yes\n", "yes", "yes \n"} {
		t.Run(strconv.Quote(answer), func(t *testing.T) {
			f := graphtest.New(t, okJSON(obj{"id": "object", "appId": "client", "displayName": "App"}))
			tio := newTestIO(t, answer)
			if err := deleteApp(t.Context(), options(t, "delete", "--appid", "object"), f.Client(), tio.env); err != nil {
				t.Fatalf("%v", err)
			}
			if n := len(f.Requests()); n != 1 {
				t.Errorf("%d requests, want 1", n)
			}
			if !strings.Contains(tio.stderr.String(), "Nothing was deleted.") {
				t.Errorf("stderr = %q", tio.stderr.String())
			}
		})
	}
}

func TestDeleteConfirmsResolvedTargetAndUsesObjectIDAfterFallback(t *testing.T) {
	for _, answer := range []string{"yes\n", "yes\r\n"} {
		t.Run(strconv.Quote(answer), func(t *testing.T) {
			f := graphtest.New(t,
				raw(404, "missing"),
				okJSON(obj{"id": "object/id", "appId": "client", "displayName": "App\n\x1b[2J"}),
				raw(204, ""))
			tio := newTestIO(t, answer)
			if err := deleteApp(t.Context(), options(t, "delete", "--appid", "client"), f.Client(), tio.env); err != nil {
				t.Fatalf("%v", err)
			}
			requests := f.Requests()
			if len(requests) != 3 {
				t.Fatalf("%d requests, want 3", len(requests))
			}
			if requests[2].Method != "DELETE" || requests[2].Path != "/v1.0/applications/object%2Fid" || len(requests[2].Body) != 0 {
				t.Errorf("delete request = %s %s %q", requests[2].Method, requests[2].Path, requests[2].Body)
			}
			output := tio.stderr.String()
			target, _, found := strings.Cut(output, "Type 'yes'")
			if !found {
				t.Fatalf("no prompt in %q", output)
			}
			if !strings.Contains(target, `Object ID: "object/id"`) || !strings.Contains(target, `Name: "App\n\x1b[2J"`) {
				t.Errorf("target = %q", target)
			}
			if strings.Contains(target, "\x1b") {
				t.Errorf("raw ESC in target")
			}
			if !strings.Contains(output, "Deleted application") {
				t.Errorf("output = %q", output)
			}
			if tio.stdout.Len() != 0 {
				t.Errorf("stdout = %q", tio.stdout.String())
			}
		})
	}
}

func TestDeleteStopsBeforeConfirmationOnMissingOrInvalidLookup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses []graphtest.Response
		requests  int
	}{
		{"not found", []graphtest.Response{raw(404, "missing"), raw(404, "missing")}, 2},
		{"forbidden", []graphtest.Response{raw(403, "denied")}, 1},
		{"missing appId", []graphtest.Response{okJSON(obj{"id": "object"})}, 1},
		{"non-string name", []graphtest.Response{okJSON(obj{"id": "object", "appId": "client", "displayName": 42})}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := graphtest.New(t, tc.responses...)
			tio := newTestIO(t, "yes\n")
			if err := deleteApp(t.Context(), options(t, "delete", "--appid", "client"), f.Client(), tio.env); err == nil {
				t.Error("no error")
			}
			if n := len(f.Requests()); n != tc.requests {
				t.Errorf("%d requests, want %d", n, tc.requests)
			}
			if tio.stderr.Len() != 0 || tio.stdout.Len() != 0 {
				t.Errorf("output %q %q", tio.stdout.String(), tio.stderr.String())
			}
		})
	}
}

func TestDeleteReportsGraphFailureWithoutRetryOrSuccess(t *testing.T) {
	for _, status := range []int{403, 404, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			f := graphtest.New(t, okJSON(obj{"id": "object", "appId": "client"}), raw(status, "delete-failed"))
			tio := newTestIO(t, "yes\n")
			err := deleteApp(t.Context(), options(t, "delete", "--appid", "object"), f.Client(), tio.env)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d: delete-failed", status)) {
				t.Errorf("err = %v", err)
			}
			if n := len(f.Requests()); n != 2 {
				t.Errorf("%d requests, want 2", n)
			}
			if strings.Contains(tio.stderr.String(), "Deleted application") {
				t.Errorf("reported success")
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestDeleteDoesNotMutateWhenConfirmationIOFails(t *testing.T) {
	f := graphtest.New(t, okJSON(obj{"id": "object", "appId": "client"}), okJSON(obj{"id": "object", "appId": "client"}))
	client := f.Client()
	tio := newTestIO(t, "yes\n")
	tio.env.Stderr = failingWriter{}
	err := deleteApp(t.Context(), options(t, "delete", "--appid", "object"), client, tio.env)
	if err == nil || !strings.Contains(err.Error(), "display deletion target") {
		t.Errorf("write failure: err = %v", err)
	}
	tio = newTestIO(t, "")
	tio.env.Stdin = iotest.ErrReader(errors.New("read failed"))
	err = deleteApp(t.Context(), options(t, "delete", "--appid", "object"), client, tio.env)
	if err == nil || !strings.Contains(err.Error(), "read deletion confirmation") {
		t.Errorf("read failure: err = %v", err)
	}
	requests := f.Requests()
	if len(requests) != 2 {
		t.Fatalf("%d requests, want 2", len(requests))
	}
	for _, r := range requests {
		if r.Method != "GET" {
			t.Errorf("unexpected %s", r.Method)
		}
	}
}

func TestDeletePromptIsExact(t *testing.T) {
	const prompt = "Delete this app registration?\n  Name: \"(unnamed)\"\n  Object ID: \"object\"\n  Application (client) ID: \"client\"\n" +
		"Deletion can interrupt applications using this registration.\nType 'yes' to delete; any other answer cancels: "
	for _, tc := range []struct {
		answer    string
		responses []graphtest.Response
		want      string
	}{
		{"no\n", []graphtest.Response{okJSON(obj{"id": "object", "appId": "client"})},
			prompt + "Deletion cancelled. Nothing was deleted.\n"},
		{"yes\n", []graphtest.Response{okJSON(obj{"id": "object", "appId": "client"}), raw(204, "")},
			prompt + "Deleted application \"object\" (Application (client) ID: \"client\").\n"},
	} {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			f := graphtest.New(t, tc.responses...)
			tio := newTestIO(t, tc.answer)
			if err := deleteApp(t.Context(), options(t, "delete", "--appid", "object"), f.Client(), tio.env); err != nil {
				t.Fatal(err)
			}
			if got := tio.stderr.String(); got != tc.want {
				t.Errorf("stderr:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
