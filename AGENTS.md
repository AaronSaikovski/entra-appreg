# Repository Guidelines

## Project Overview

`entra-appreg` is a Go CLI for Microsoft Entra ID single-page-app registrations. It has four commands:

- `create`: the default command.
- `expose-api`: adds one scope to an existing registration.
- `list`: read-only.
- `delete`: deletes only after interactive confirmation.

Scopes and client secrets are opt-in. The CLI authenticates through the operator's existing `az login`. It is not a service, and it has no database or config file.

## Architecture & Data Flow

```
cmd/entra-appreg.run → cli.Parse → (help/version/no-args exit) → delete TTY gate
  → auth.Authenticate → graph.New(staticCredential)
  → app.Run(ctx, opts, client, Env) → create | exposeAPI | list | deleteApp → report.Write (create only)
```

Requests are sequential. The context is `context.Background()`; only auth has an overall deadline.

- **`internal/cli`: three-phase parse.** Cobra must never broaden syntax or print output.
  1. **Pre-parse.** The command word is lowercased. An unknown command fails first. `--version` beats `--help` anywhere, including in a value position. `--` and `--x=y` are rejected.
  2. **Cobra** (`flags.go`). A fresh, silent tree with only the requested subcommand is built per call, and all 18 flags are `PersistentFlags`.
     - `optionalString` rejects values starting with `--`.
     - `redirectList` accumulates values; `--redirect-url` is normalized to `--redirect-urls`.
     - A hidden `--help` with no `-h` replaces Cobra's own. A `ran` guard catches cases where no subcommand ran.
  2b. **`--params-file`** (`params.go`). `applyParamsFile` runs after Cobra and before `validate()`. A file is read only when `--params-file` is given; there is no default file. It decodes one JSON object (cap 1 MiB) into `map[string]json.RawMessage` and fills only options still unset; command-line redirects replace the file list. Keys are option names without `--`, checked in sorted order; unknown keys, wrong types and trailing values are usage errors. `--params-file` has no option group, so every command accepts it, and file-supplied options hit the same group rules. Adding a text option means adding a `paramsTargets` row too, and a key in `sampleFields` (`params_test.go`). The samples in `examples/` are pinned by three tests: `TestSampleParamsFilesParse` (every option value exact), `TestSampleParamsFileCommandLineOverrides` and `TestSampleParamsFileObeysCommandRules`. Editing a sample means updating its expected values there and the README snippets.
  3. **`validate()`** is a switch on the command. Forbidden-option messages come from one ordered table of all options, each tagged with group bits; adding an option means adding one row there.
  - Creation fields are validated later, in `app.create`, after the `--appid` lookup. An existing ID is therefore a no-op with exit 0.
- **`internal/auth`: one `az` process per command.**
  - `exec.LookPath("az")` checks the install without starting a process (on Windows, PATHEXT finds `az.cmd`). If `az` is missing, the error is the "not found" message.
  - `AzureCLICredential` with empty options requests the token for the active az context, under a 10 s deadline. There is no `az account show` pre-check and no tenant pinning. Each Python `az` start costs about a second, so don't add another `az` call. The one exception is `auth.KeyVault`: a second token (scope `keyvault.Scope`, own message `msgNoKeyVaultToken`) requested only when `create` gets `--keyvault-uri`. Both go through `tokenWith`.
  - Returns a `staticCredential` whose `String`/`GoString` are redacted, so Graph calls never start `az` again.
  - Error messages are fixed constants. Any `GetToken` failure maps to the one `msgNoToken` message: azidentity reports signed-out and expired sessions the same way, and its text carries `az` stderr, so never wrap it.
- **Tenant ID** comes from Graph `/organization` through the same token (`currentTenant` in `list.go`, which requires exactly one org). `create` calls it after validation and the name check, before the POST, so the reported tenant is exactly the token's tenant.
- **`internal/graph`: `msgraph-sdk-go` on a hardened transport.**
  - Kiota transport with only `ParametersNameDecodingHandler` and `statusHandler`: no retry, redirect or compression. `http.Client` has a 10 s timeout and returns `ErrUseLastResponse` instead of following redirects.
  - `statusHandler` turns every non-2xx response into `*StatusError` carrying the raw body, capped at 1 MiB (`maxErrorBody`). A longer body is cut at the cap and marked as truncated. The 403 guidance and the `Application.Read.All` hint (GET `/applications` only) live in `status.go`.
  - `describe()` wraps all other failures.
  - `GetApplication` tries the object ID first, then the `appId` alternate key, but only after a 404. Two 404s return `(nil, nil)`.
  - `ApplicationPages` rejects repeated next links. `validateNextLink` requires the same scheme, host and port, no userinfo or fragment, and a path under `/v1.0/`. `internal/graph/export_test.go` exposes unexported helpers to the external `graph_test` package.
  - `graph.MissingFieldError` is the one helper for "Graph response requires … '<field>'" errors, and `graph.Scope` is the only Graph scope constant.
  - The base URL is fixed. `NewForBase` exists only for loopback tests; do not add an endpoint override.
- **Backing-store rule.** msgraph models fetched from Graph serialize only fields changed after the fetch.
  - Build every POST/PATCH body from fresh `models.New…()` objects. Never mutate and resend a fetched model.
  - `expose.go` `copyAPI`/`copyScope` show the pattern. Call each setter only when the fetched getter is non-nil, and copy `AdditionalData` with `maps.Clone`.
- **`internal/app`: the workflows.**
  - `create` sequence, each step taking the validated `createPlan`:
    1. Optional `--appid` lookup (`applicationExists`). A found app means exit 0 with nothing changed.
    2. Validate (`planCreate`): name → audience → redirects → exact-string dedup → scope → lifetime.
    3. Duplicate-name preflight (`checkNameAvailable`). A match is a usage error.
    4. Tenant lookup (`currentTenant`). A failure stops the command before anything is created.
    5. POST (`postApplication`) with `spa.redirectUris` and `requiredResourceAccess` = Graph `User.Read` (delegated), then print `AUTH_APP_ID`/`AUTH_CLIENT_ID` immediately. With no redirects, `noRedirectNotice` goes to stderr first.
    5b. Service principal (`addServicePrincipal` → `graph.CreateServicePrincipal`, `POST /servicePrincipals {appId}`), then print `AUTH_SP_ID`. On failure, stderr gets the `az ad sp create --id <client>` recovery hint; there is no retry.
    6. Scope PATCH (`addScope`): identifier URI, scope, and `requiredResourceAccess` resent in full (`requiredAccess`: Graph `User.Read` plus the app's own new scope).
    7. `addPassword` (`addSecret`), then, with `--keyvault-uri`, `storeSecret` (`vault.go`). `openVault` → `Env.KeyVault` runs earlier, after step 4 and before the POST, so a missing Key Vault token mutates nothing. A store failure records `(not stored)`, still writes the report, and is returned (exit 1) via `errors.Join` with any missing-`keyId` error. After a confirmed store (`storedInVault`), the report's `Secret value:` is `(stored in Key Vault: <id>)` and the plain-text notice is skipped; the plaintext is kept only when the store failed.
    8. Report (`reportContents` builds the text without side effects; `writeReport` writes it).
  - `resolveScope`/`resolveLifetime` return `*cli.UsageError` as `error`; build usage errors with `cli.Usagef`.
  - `list`: `/organization` must return exactly one org. In `--json` mode stdout is only the JSON array; tenant lines and notices go to stderr. Text output quotes names with `strconv.Quote`.
  - `delete`: resolves the target before prompting. The prompt goes to stderr with quoted fields. Only `yes\n` or `yes\r\n` confirms; EOF cancels with exit 0.
- **`internal/report`**: sanitizes the name. Spaces become `-`. Windows reserved device names get a `_` prefix (`NUL` → `_NUL.txt`). The file is claimed with `O_EXCL` and mode 0600; on collision the name gets a `-1`, `-2`, … suffix. Reports go to the current directory and are never overwritten.

## Key Directories

| Path | Purpose |
|---|---|
| `cmd/entra-appreg/` | `main` plus testable `run(args, stdin *os.File, stdout io.Writer, stderr *os.File) int`. Stdin and stderr are `*os.File` so `term.IsTerminal` works. |
| `internal/cli/` | Parsing, validation, help text (`help.go`; `HelpText(topic, version)` takes the short version from `main`). |
| `internal/auth/` | `az` presence check and the single Azure CLI token request. |
| `internal/graph/` | Graph client, `StatusError`, paging, next-link safety. |
| `internal/graphtest/` | Loopback Graph fixture. Non-test file; import it only from tests. |
| `internal/app/` | Workflows plus `scope.go`/`lifetime.go` rules. |
| `internal/report/` | Report writer; platform files `report_windows.go`/`report_other.go`. |
| `internal/keyvault/` | `azsecrets` (Azure SDK for Go) wrapper: `ParseVaultURI` (public `*.vault.azure.net` only), `ValidSecretName`, `Client.SetSecret`. The SDK pipeline runs with `MaxRetries: -1` on a 10 s, no-redirect `http.Client`; the challenge-resource check stays on. Tests use `newClient` with a TLS loopback server that sends Key Vault's 401 challenge, with verification disabled. Errors add 401/403/409 guidance and never include the secret. |
| `examples/` | `api.params.json` (API registration: scope, secret in Key Vault, no redirects) and `spa.params.json` (SPA: three redirects including `/auth/callback`, no scope, no secret), the sample `--params-file` inputs for `create`. Values are pinned by `TestSampleParamsFilesParse`. |

## Development Commands

Run from the repo root. `task check` is the canonical gate; it must pass after every change:

```sh
task check            # gofmt -l, go vet, GOOS=windows go vet, go build, go test, then a binary smoke (--version must equal "<git describe without v> (commit <short sha>, built <UTC time>)")
task test             # go test -v ./...
task run -- list --help
task build            # → bin/entra-appreg[.exe]
task release          # CGO_ENABLED=0 -trimpath -ldflags="-s -w" → bin/
task staticcheck      # pinned Go-1.27-compatible staticcheck commit
task seccheck         # govulncheck@latest
```

Without Task:

```sh
test -z "$(gofmt -l .)" && go vet ./... && GOOS=windows go vet ./... && go build ./... && go test ./...
go build -ldflags "-X main.version=$(git describe --tags --always --dirty | sed 's/^v//') -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/entra-appreg ./cmd/entra-appreg && bin/entra-appreg --version
bin/entra-appreg list --top 0          # must exit 2 before auth
bin/entra-appreg delete --appid x </dev/null   # must exit 2 (TTY gate)
```

`task lint` (go fmt, `go mod tidy`, `go fix`) and `task deps` (`go get -u ./...`) change files and dependencies. Run them only when you intend those changes. The first build compiles msgraph-sdk-go, which takes minutes and several GB of RAM.

## Code Conventions & Common Patterns

- **Errors and exit codes.**
  - `*cli.UsageError` exits 2 and prints the bare message.
  - Other errors exit 1 with an `Error: …` prefix; auth failures print `Authentication failed: …`.
  - Success, help, version and cancelled delete exit 0.
  - All diagnostics go to stderr. Keep stdout clean for `AUTH_*` lines and JSON.
- **Exit-code gotcha.** No arguments, parse errors and a non-TTY delete exit 2. "Not found" on expose or delete exits 1.
- **Optional options.** `*string` in `cli.Options`: `nil` means not supplied, `""` means supplied empty. Scalars take the last value; redirect values accumulate and keep their raw spelling.
- **Case-insensitive matching.** Use `cli.EqualFoldASCII` for audience, consent, state, `all`/`custom`, redirect scheme and duplicate scope names.
- **Injection seams.** Use only these; no DI framework:
  - `app.Env{Stdout, Stderr, Stdin, Now, ReportDir}`
  - `auth.authenticateWith(ctx, lookPathFunc, credentialFunc, deadline)`
  - `graph.NewForBase`
- **Go version.** The code uses Go 1.26+ `new(expr)` for pointers (e.g. `new(int32(1))`). Keep this rather than adding `ptr` helpers.
- **Secrets.**
  - Never print a secret on success.
  - If the report write fails, print the full report to stdout and exit 1.
  - A missing `keyId` writes `(not returned by Graph)` and keeps the secret, then exits 1.
  - Never log tokens.
- **Report contents.** Labels are padded to column 25. Line endings are CRLF on Windows. Dates use RFC3339Nano UTC, not Graph's original strings.
- **New options.** Each needs a flag in `flags.go`, validation and option-group membership in `cli.go`, a `paramsTargets` row (or a case in `applyParamsFile`) in `params.go`, help text, README, and tests.
- **After every change,** update README (behavior and limits) and CHANGELOG `## Unreleased`, recording the verification actually run.

## Important Files

- `cmd/entra-appreg/main.go`: entry point, exit mapping, delete TTY gate, and build metadata. `version`/`commit`/`date` (defaults `dev`/`none`/`unknown`) are set at link time with `-X main.version=… -X main.commit=… -X main.date=…`. GoReleaser uses `{{ .Version }}`/`{{ .ShortCommit }}`/`{{ .Date }}`; the Taskfile uses `git describe` minus the `v`, `git rev-parse --short HEAD`, and a UTC timestamp. There is no `v` stripping or build-info fallback in code, so pipelines must inject `version` without a `v`. `--version` prints `entra-appreg ` + `fullVersion()`; the general help heading shows only `version`. Never hard-code a version number. The CI smoke injects `0.0.0-ci`/`ci-commit`/`ci-date` and checks the exact line; `release.yml` checks the tag build.
- `internal/graph/status.go`: Graph error and 403 guidance text. Tests assert it exactly.
- `go.mod`: `go 1.27.2`. Direct deps: azcore/azidentity, security/keyvault/azsecrets, msgraph-sdk-go(-core), kiota-http/abstractions, cobra/pflag, google/uuid (scope IDs), x/term.
- `Taskfile.yml`: dev tasks.
- `.goreleaser.yaml`: release builds and archives. Validate it with `goreleaser check`. `staticcheck.conf` disables only the error-string style checks.
- `.github/workflows/`: all workflows use `go-version-file: go.mod`.
  - `ci.yml`: runs on Linux, macOS and Windows: gofmt, vet, build, test, and a Python binary smoke.
  - `build.yml`: vet, fmt diff, build, and `goreleaser check`.
  - `test.yml`: race tests, vet, staticcheck (pinned to a Go-1.27-compatible commit), and govulncheck.
  - `release.yml`: checks that a build stamped with the tag reports it, runs the quality gates, then GoReleaser.
- `.gitignore`: ignores `*.txt` (reports may contain plaintext secrets), `/bin/`, `/build/`, `/dist/` and root binaries.

## Runtime/Tooling Preferences

- **Go.** Use Go per `go.mod` (1.27.2; newer local Go downloads it automatically) with Go modules and `go.sum`. Optionally install Task (taskfile.dev). Python 3.12 is used only by CI smoke scripts. Before raising the Go version, check that the pinned staticcheck commit in `test.yml`/`Taskfile.yml` still loads the code.

## Testing & QA

- **Framework.** Standard `testing` only: no testify, mocks, golden files or `testdata/`. There are 114 top-level tests across `cmd/entra-appreg`, `app`, `auth`, `cli`, `graph`, `keyvault` and `report`. No coverage threshold.
- **Graph tests.** `graphtest.New(t, graphtest.JSON(200, v), …)` queues replies in order. `{base}` in a reply body becomes the fixture URL, for next-page links. An unexpected request returns 500 and fails the test.
  - Assert on `f.Requests()`: method, escaped path, query, `Authorization: Bearer fake-token`, and body. Use `url.ParseQuery` for `$filter`.
  - `graph_test.go` is `package graph_test` (external), to avoid an import cycle with `graphtest`.
- **App tests.** In-package. `helpers_test.go` provides:
  - `options(t, args...)`, which goes through the real `cli.Parse`;
  - `newTestIO`, which uses buffers, `frozenNow` = 2028-02-28T16:30:45Z, and a `t.TempDir()` report directory;
  - `ok`, `raw` and `decode`.
- **Auth tests.** Fake `lookPathFunc`/`credentialFunc`. Assert that strings like `private-cli-output-must-not-leak` never appear in errors.
- **Main tests.** Call `run` directly, with stdin from an `os.Pipe` (not a TTY), stderr as a temp file, and `t.Setenv("PATH", t.TempDir())`.
- **Exact-output tests.** `TestCreateOutputAndReportAreExact`, `TestListOutputIsExact` and `TestValidationOrderAndMessages` check stdout, report text and validation order byte-for-byte. Update them only when you mean to change behavior.
- **Platform-specific tests.** `report_unix_test.go` (build tag `unix`) and `report_windows_test.go` (`windows`). The Windows test is compiled locally via `GOOS=windows go vet` but only runs on Windows CI. The report-failure case skips on Windows or when running as root.
- **Hermeticity.** Keep tests offline. Use only `t.Setenv`/`t.TempDir`; never touch the real Azure session, global cwd or credentials.
- **Live QA.** Use README's **Smoke test checklist** only in a deliberately chosen test tenant, starting with read-only `list --top 1 --json`. Create, expose and delete mutate the tenant. Keep reports and captured secret output out of git and logs.

## Known SPA Gaps

README **SPA readiness and known gaps** is the source of truth; keep it in sync when closing any of these. Open items, by impact:

1. No live-tenant verification yet. Self-referencing `requiredResourceAccess` in the scope PATCH and SP creation right after the POST are unconfirmed.
2. No command to add redirect URIs to an existing app. If added, rebuild the `spa` body from fresh models and keep existing URIs (backing-store rule).
3. `validRedirect` accepts `http` for any host; restricting it to `localhost`/`127.0.0.1` would change `TestValidationOrderAndMessages`-style expectations.
4. `expose-api` leaves `requiredResourceAccess` untouched (unlike `create`).
5. Admin consent (`oauth2PermissionGrants`) is deliberately out of scope: it needs privileged roles.
