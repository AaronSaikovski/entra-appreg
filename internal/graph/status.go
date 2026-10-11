package graph

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	khttp "github.com/microsoft/kiota-http-go"
)

// StatusError is a non-2xx Graph response with its raw body preserved.
type StatusError struct {
	Method string
	Path   string // URL path plus "?" and the raw query when present
	Status int
	Body   string

	// applicationsPath is the base path + "/applications"; GET requests below it
	// get the Application.Read.All guidance on HTTP 403.
	applicationsPath string
}

// Error renders the status line and raw body; HTTP 403 adds access guidance.
func (e *StatusError) Error() string {
	if e.Status == http.StatusForbidden {
		permission := ""
		if e.Method == http.MethodGet && e.applicationsPath != "" && strings.HasPrefix(e.Path, e.applicationsPath) {
			permission = "Reading app registrations requires the delegated Microsoft Graph permission " +
				"`Application.Read.All` (or a sufficient broader permission), with admin consent " +
				"for the calling client, Azure CLI, not the target app registration. Your user " +
				"also needs directory read access: default member permissions can suffice; " +
				"Directory Readers is a supported read-only role. Azure subscription " +
				"Owner/Contributor roles do not grant this access.\n"
		}
		return "Microsoft Graph denied access (HTTP 403). Your signed-in Azure CLI account " +
			"does not have permission to perform this operation in the selected tenant.\n" +
			permission +
			"Check the account and tenant with `az account show`. If they are incorrect, " +
			"sign in to the intended tenant with `az login --tenant <tenant-id>`.\n" +
			"If they are correct, ask your Microsoft Entra administrator to check the " +
			"Microsoft Graph permissions and admin consent for Azure CLI, and your " +
			"account's directory roles or app ownership. Signing in again alone does not " +
			"grant permissions.\n" +
			fmt.Sprintf("Technical details: %s %s failed with HTTP 403: %s", e.Method, e.Path, e.Body)
	}
	return fmt.Sprintf("%s %s failed with HTTP %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// statusHandler turns every non-2xx response into a StatusError carrying the raw
// body. Because the error is returned from the transport, Kiota never sees the
// response: 3xx is not followed, the CAE 401 retry never runs, and Kiota's
// ODataError (whose Error() can panic) is never produced.
type statusHandler struct {
	applicationsPath string
}

// Intercept implements khttp.Middleware.
func (h *statusHandler) Intercept(p khttp.Pipeline, i int, req *http.Request) (*http.Response, error) {
	resp, err := p.Next(req, i)
	if err != nil || (resp.StatusCode >= 200 && resp.StatusCode <= 299) {
		return resp, err
	}
	path := requestPath(req.URL)
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%s %s HTTP %d: could not read response: %w", req.Method, path, resp.StatusCode, readErr)
	}
	return nil, &StatusError{
		Method:           req.Method,
		Path:             path,
		Status:           resp.StatusCode,
		Body:             string(body),
		applicationsPath: h.applicationsPath,
	}
}
