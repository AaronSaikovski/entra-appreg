// Package graphtest provides an offline Microsoft Graph fixture for tests: a
// loopback HTTP server that replies from a queue and records every request.
package graphtest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/AaronSaikovski/entra-appreg/internal/graph"
)

// Response is one queued fixture reply. A non-empty Body without a
// Content-Type header is sent as application/json, as Graph would. Every
// "{base}" in Body is replaced with the fixture's Base when sent, so replies
// can carry next-page links back to the same server. A zero Status means 200.
type Response struct {
	Status int
	Body   string
	Header map[string]string
}

// JSON marshals v into a response with Content-Type application/json.
func JSON(status int, v any) Response {
	body, err := json.Marshal(v)
	if err != nil {
		panic("graphtest.JSON: " + err.Error())
	}
	return Response{Status: status, Body: string(body), Header: map[string]string{"Content-Type": "application/json"}}
}

// Request is one recorded request. Path is the escaped (on-the-wire) path.
type Request struct {
	Method, Path, RawQuery, Authorization string
	Body                                  []byte
}

// Fixture is a loopback Graph server; Base is the server URL + "/v1.0".
type Fixture struct {
	Base string

	t         testing.TB
	mu        sync.Mutex
	responses []Response
	requests  []Request
}

// New starts a fixture that answers requests with responses in order. A
// request after the queue is exhausted gets HTTP 500 "unexpected request" and
// fails the test. The server is closed by t.Cleanup.
func New(t testing.TB, responses ...Response) *Fixture {
	t.Helper()
	f := &Fixture{t: t, responses: responses}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	f.Base = server.URL + "/v1.0"
	return f
}

func (f *Fixture) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("graphtest: reading request body: %v", err)
	}
	reply, ok := f.record(Request{
		Method:        r.Method,
		Path:          r.URL.EscapedPath(),
		RawQuery:      r.URL.RawQuery,
		Authorization: r.Header.Get("Authorization"),
		Body:          body,
	})
	if !ok {
		f.t.Errorf("graphtest: unexpected request %s %s", r.Method, r.URL.RequestURI())
		reply = Response{Status: http.StatusInternalServerError, Body: "unexpected request"}
	}
	f.write(w, reply)
}

// record appends req and dequeues the next reply; ok is false once the queue is empty.
func (f *Fixture) record(req Request) (reply Response, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if len(f.responses) == 0 {
		return Response{}, false
	}
	reply, f.responses = f.responses[0], f.responses[1:]
	return reply, true
}

// write sends reply, expanding "{base}" and defaulting a body's Content-Type to JSON.
func (f *Fixture) write(w http.ResponseWriter, reply Response) {
	header := w.Header()
	for name, value := range reply.Header {
		header.Set(name, value)
	}
	body := strings.ReplaceAll(reply.Body, "{base}", f.Base)
	if body != "" {
		if header.Get("Content-Type") == "" {
			header.Set("Content-Type", "application/json")
		}
		header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	status := reply.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// Requests returns a copy of the requests recorded so far.
func (f *Fixture) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Client returns a graph client for this fixture authenticated with "fake-token".
func (f *Fixture) Client() *graph.Client {
	f.t.Helper()
	client, err := graph.NewForBase(fakeCredential{token: "fake-token"}, f.Base)
	if err != nil {
		f.t.Fatalf("graphtest: creating client: %v", err)
	}
	return client
}

// fakeCredential returns a fixed token so tests never reach a real identity provider.
type fakeCredential struct{ token string }

func (c fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: c.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}
