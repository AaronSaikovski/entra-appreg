package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
)

func TestDeleteRequiresCompleteExplicitConfirmation(t *testing.T) {
	for _, answer := range []string{"", "\n", "no\n", "y\n", "YES\n", " yes\n", "yes", "yes \n"} {
		f := graphtest.New(t, ok(obj{"id": "object", "appId": "client", "displayName": "App"}))
		tio := newTestIO(t, answer)
		if err := deleteApp(context.Background(), options(t, "delete", "--appid", "object"), f.Client(t), tio.env); err != nil {
			t.Fatalf("%q: %v", answer, err)
		}
		if n := len(f.Requests()); n != 1 {
			t.Errorf("%q: %d requests, want 1", answer, n)
		}
		if !strings.Contains(tio.stderr.String(), "Nothing was deleted.") {
			t.Errorf("%q: stderr = %q", answer, tio.stderr.String())
		}
	}
}

func TestDeleteConfirmsResolvedTargetAndUsesObjectIDAfterFallback(t *testing.T) {
	for _, answer := range []string{"yes\n", "yes\r\n"} {
		f := graphtest.New(t,
			raw(404, "missing"),
			ok(obj{"id": "object/id", "appId": "client", "displayName": "App\n\x1b[2J"}),
			raw(204, ""))
		tio := newTestIO(t, answer)
		if err := deleteApp(context.Background(), options(t, "delete", "--appid", "client"), f.Client(t), tio.env); err != nil {
			t.Fatalf("%q: %v", answer, err)
		}
		requests := f.Requests()
		if len(requests) != 3 {
			t.Fatalf("%q: %d requests, want 3", answer, len(requests))
		}
		if requests[2].Method != "DELETE" || requests[2].Path != "/v1.0/applications/object%2Fid" || len(requests[2].Body) != 0 {
			t.Errorf("%q: delete request = %s %s %q", answer, requests[2].Method, requests[2].Path, requests[2].Body)
		}
		output := tio.stderr.String()
		target, _, found := strings.Cut(output, "Type 'yes'")
		if !found {
			t.Fatalf("%q: no prompt in %q", answer, output)
		}
		if !strings.Contains(target, `Object ID: "object/id"`) || !strings.Contains(target, `Name: "App\n\x1b[2J"`) {
			t.Errorf("%q: target = %q", answer, target)
		}
		if strings.Contains(target, "\x1b") {
			t.Errorf("%q: raw ESC in target", answer)
		}
		if !strings.Contains(output, "Deleted application") {
			t.Errorf("%q: output = %q", answer, output)
		}
		if tio.stdout.Len() != 0 {
			t.Errorf("%q: stdout = %q", answer, tio.stdout.String())
		}
	}
}

func TestDeleteStopsBeforeConfirmationOnMissingOrInvalidLookup(t *testing.T) {
	for i, tc := range []struct {
		responses []graphtest.Response
		requests  int
	}{
		{[]graphtest.Response{raw(404, "missing"), raw(404, "missing")}, 2},
		{[]graphtest.Response{raw(403, "denied")}, 1},
		{[]graphtest.Response{ok(obj{"id": "object"})}, 1},
		{[]graphtest.Response{ok(obj{"id": "object", "appId": "client", "displayName": 42})}, 1},
	} {
		f := graphtest.New(t, tc.responses...)
		tio := newTestIO(t, "yes\n")
		if err := deleteApp(context.Background(), options(t, "delete", "--appid", "client"), f.Client(t), tio.env); err == nil {
			t.Errorf("case %d: no error", i)
		}
		if n := len(f.Requests()); n != tc.requests {
			t.Errorf("case %d: %d requests, want %d", i, n, tc.requests)
		}
		if tio.stderr.Len() != 0 || tio.stdout.Len() != 0 {
			t.Errorf("case %d: output %q %q", i, tio.stdout.String(), tio.stderr.String())
		}
	}
}

func TestDeleteReportsGraphFailureWithoutRetryOrSuccess(t *testing.T) {
	for _, status := range []int{403, 404, 500} {
		f := graphtest.New(t, ok(obj{"id": "object", "appId": "client"}), raw(status, "delete-failed"))
		tio := newTestIO(t, "yes\n")
		err := deleteApp(context.Background(), options(t, "delete", "--appid", "object"), f.Client(t), tio.env)
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d: delete-failed", status)) {
			t.Errorf("%d: err = %v", status, err)
		}
		if n := len(f.Requests()); n != 2 {
			t.Errorf("%d: %d requests, want 2", status, n)
		}
		if strings.Contains(tio.stderr.String(), "Deleted application") {
			t.Errorf("%d: reported success", status)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestDeleteDoesNotMutateWhenConfirmationIOFails(t *testing.T) {
	f := graphtest.New(t, ok(obj{"id": "object", "appId": "client"}), ok(obj{"id": "object", "appId": "client"}))
	client := f.Client(t)
	tio := newTestIO(t, "yes\n")
	tio.env.Stderr = failingWriter{}
	err := deleteApp(context.Background(), options(t, "delete", "--appid", "object"), client, tio.env)
	if err == nil || !strings.Contains(err.Error(), "display deletion target") {
		t.Errorf("write failure: err = %v", err)
	}
	tio = newTestIO(t, "")
	tio.env.Stdin = iotest.ErrReader(errors.New("read failed"))
	err = deleteApp(context.Background(), options(t, "delete", "--appid", "object"), client, tio.env)
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
