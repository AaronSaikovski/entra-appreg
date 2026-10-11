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
  → auth.Authenticate(pinTenant = create) → graph.New(staticCredential)
  → app.Run(ctx, opts, client, tenantID, Env) → create | exposeAPI | list | deleteApp → report.Write (create only)
```

Requests are sequential. The context is `context.Background()`; only auth has an overall deadline.

- **`internal/cli`: three-phase parse.** Cobra must never broaden syntax or print output.
  1. **Pre-parse.** The command word is lowercased. An unknown command fails first. `--version` beats `--help` anywhere, including in a value position. `--` and `--x=y` are rejected.
  2. **Cobra** (`flags.go`). A fresh, silent tree is built per call, and all 17 flags are `PersistentFlags`.
     - `optionalString` rejects values starting with `--`.
     - `redirectList` accumulates values; `--redirect-url` is normalized to `--redirect-urls`.
     - A hidden `--help` with no `-h` replaces Cobra's own. A `ran` guard catches cases where no subcommand ran.
  3. **`validate()`.** Option-group rules run in a fixed order.
  - Creation fields are validated later, in `app.create`, after the `--appid` lookup. An existing ID is therefore a no-op with exit 0.
- **`internal/auth`: tenant check, then token.**
  - Runs `az account show --query tenantId --output tsv --only-show-errors` (`cmd.exe /d /c az` on Windows). The output must be a UUID.
  - Gets an `AzureCLICredential` token, all under a 10 s deadline. Only `create` pins `TenantID`.
  - Returns a `staticCredential` whose `String`/`GoString` are redacted, so Graph calls never start `az` again.
  - Error messages are fixed constants. Never wrap SDK errors; they carry `az` stderr.
- **`internal/graph`: `msgraph-sdk-go` on a hardened transport.**
  - Kiota transport with only `ParametersNameDecodingHandler` and `statusHandler`: no retry, redirect or compression. `http.Client` has a 10 s timeout and returns `ErrUseLastResponse` instead of following redirects.
  - `statusHandler` turns every non-2xx response into `*StatusError` carrying the raw body. The 403 guidance and the `Application.Read.All` hint (GET `/applications` only) live in `status.go`.
  - `describe()` wraps all other failures.
  - `GetApplication` tries the object ID first, then the `appId` alternate key, but only after a 404. Two 404s return `(nil, nil)`.
  - `ApplicationPages` rejects repeated next links. `ValidateNextLink` requires the same scheme, host and port, no userinfo or fragment, and a path under `/v1.0/`.
  - The base URL is fixed. `NewForBase` exists only for loopback tests; do not add an endpoint override.
- **Backing-store rule.** msgraph models fetched from Graph serialize only fields changed after the fetch.
  - Build every POST/PATCH body from fresh `models.New…()` objects. Never mutate and resend a fetched model.
  - `expose.go` `copyAPI`/`copyScope` show the pattern. Call each setter only when the fetched getter is non-nil, and copy `AdditionalData` with `maps.Clone`.
- **`internal/app`: the workflows.**
  - `create` sequence:
    1. Optional lookup.
    2. Validate name → audience → redirects → exact-string dedup → scope → lifetime.
    3. Duplicate-name preflight: a match is a usage error.
    4. POST.
    5. Print `AUTH_APP_ID`/`AUTH_CLIENT_ID` immediately.
    6. Scope PATCH.
    7. `addPassword`.
    8. Report.
  - `list`: `/organization` must return exactly one org. In `--json` mode stdout is only the JSON array; tenant lines and notices go to stderr. Text output quotes names with `strconv.Quote`.
  - `delete`: resolves the target before prompting. The prompt goes to stderr with quoted fields. Only `yes\n` or `yes\r\n` confirms; EOF cancels with exit 0.
- **`internal/report`**: sanitizes the name. Spaces become `-`. Windows reserved device names get a `_` prefix (`NUL` → `_NUL.txt`). The file is claimed with `O_EXCL` and mode 0600; on collision the name gets a `-1`, `-2`, … suffix. Reports go to the current directory and are never overwritten.

## Key Directories

| Path | Purpose |
|---|---|
| `cmd/entra-appreg/` | `main` plus testable `run(args, stdin *os.File, stdout io.Writer, stderr *os.File) int`. Stdin and stderr are `*os.File` so `term.IsTerminal` works. |
| `internal/cli/` | Parsing, validation, help text (`help.go`), version (`version.go`). |
| `internal/auth/` | Azure CLI login check and token. |
| `internal/graph/` | Graph client, `StatusError`, paging, next-link safety. |
| `internal/graphtest/` | Loopback Graph fixture. Non-test file; import it only from tests. |
| `internal/app/` | Workflows plus `scope.go`/`lifetime.go` rules. |
| `internal/report/` | Report writer; platform files `report_windows.go`/`report_other.go`. |

## Development Commands

Run from the repo root. `task check` is the canonical gate; it must pass after every change:

```sh
task check            # gofmt -l, go vet, GOOS=windows go vet, go build, go test, then a binary smoke
task test             # go test -v ./...
task run -- list --help
task build            # → bin/entra-appreg[.exe]
task release          # CGO_ENABLED=0 -trimpath -ldflags="-s -w" → bin/
task staticcheck | task seccheck   # optional, @latest via go run
```

Without Task:

```sh
test -z "$(gofmt -l .)" && go vet ./... && GOOS=windows go vet ./... && go build ./... && go test ./...
go build -o bin/entra-appreg ./cmd/entra-appreg && bin/entra-appreg --version
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
  - `auth.authenticateWith(ctx, pin, runFunc, credentialFunc, deadline)`
  - `graph.NewForBase`
- **Go version.** The code uses Go 1.26+ `new(expr)` for pointers (e.g. `new(int32(1))`). Keep this rather than adding `ptr` helpers.
- **Secrets.**
  - Never print a secret on success.
  - If the report write fails, print the full report to stdout and exit 1.
  - A missing `keyId` writes `(not returned by Graph)` and keeps the secret, then exits 1.
  - Never log tokens.
- **Report contents.** Labels are padded to column 25. Line endings are CRLF on Windows. Dates use RFC3339Nano UTC, not Graph's original strings.
- **New options.** Each needs a flag in `flags.go`, validation and option-group membership in `cli.go`, help text, README, and tests.
- **After every change,** update README (behavior and limits) and CHANGELOG `## Unreleased`, recording the verification actually run.

## Important Files

- `cmd/entra-appreg/main.go`: entry point, exit mapping, delete TTY gate.
- `internal/cli/version.go`: `const Version`, the only version source. It is used by `--version`, the help heading, `main_test.go`, and the CI smoke regex. The README "Version" line must be bumped by hand. No release workflow checks the tag against it, despite what the comment says.
- `internal/graph/status.go`: Graph error and 403 guidance text. Tests assert it exactly.
- `go.mod`: `go 1.27.1`. Main direct deps: cobra/pflag, azidentity/azcore, msgraph-sdk-go(-core), kiota-http/abstractions, uuid, x/term.
- `Taskfile.yml`: dev tasks.
- `.github/workflows/ci.yml`: the only valid CI. Runs on Linux, macOS and Windows: gofmt, vet, build, test, and a Python binary smoke.
- `.gitignore`: ignores `*.txt` (reports may contain plaintext secrets), `/bin/`, `/build/`, `/dist/` and root binaries.

## Runtime/Tooling Preferences

- **Go.** Use Go per `go.mod` (1.27.1; newer local Go downloads it automatically) with Go modules and `go.sum`. Optionally install Task (taskfile.dev). Python 3.12 is used only by CI smoke scripts.
- **Azure CLI.** Live use needs Azure CLI 2.54.0+ on `PATH` and a deliberately chosen `az login` identity with Graph permissions. There is no automatic login and no environment-secret fallback. Help and version need no login.
- **Broken workflows.** These are owner-managed; check with the owner before relying on or changing them:
  - `build.yml` builds `./cmd/gogoodwe`, which doesn't exist.
  - `test.yml` runs `./tests/...`, which doesn't exist.
  - `release.yml` and `goreleaser.yml` both run on `v*` tags, and there is no `.goreleaser.yaml`.
  - All four pin Go 1.26.

## Testing & QA

- **Framework.** Standard `testing` only: no testify, mocks, golden files or `testdata/`. 78 top-level tests across `cmd/entra-appreg`, `app`, `auth`, `cli`, `graph` and `report`. No coverage threshold.
- **Graph tests.** `graphtest.New(t, graphtest.JSON(200, v), …)` queues replies in order. `{base}` in a reply body becomes the fixture URL, for next-page links. An unexpected request returns 500 and fails the test.
  - Assert on `f.Requests()`: method, escaped path, query, `Authorization: Bearer fake-token`, and body. Use `url.ParseQuery` for `$filter`.
  - `graph_test.go` is `package graph_test` (external), to avoid an import cycle with `graphtest`.
- **App tests.** In-package. `helpers_test.go` provides:
  - `options(t, args...)`, which goes through the real `cli.Parse`;
  - `newTestIO`, which uses buffers, `frozenNow` = 2028-02-28T16:30:45Z, and a `t.TempDir()` report directory;
  - `ok`, `raw` and `decode`.
- **Auth tests.** Fake `runFunc`/`credentialFunc`. Assert that strings like `private-cli-output-must-not-leak` never appear in errors.
- **Main tests.** Call `run` directly, with stdin from an `os.Pipe` (not a TTY), stderr as a temp file, and `t.Setenv("PATH", t.TempDir())`.
- **Platform-specific tests.** `report_unix_test.go` (build tag `unix`) and `report_windows_test.go` (`windows`). The Windows test is compiled locally via `GOOS=windows go vet` but only runs on Windows CI. The report-failure case skips on Windows or when running as root.
- **Hermeticity.** Keep tests offline. Use only `t.Setenv`/`t.TempDir`; never touch the real Azure session, global cwd or credentials.
- **Live QA.** Use README's **Smoke test checklist** only in a deliberately chosen test tenant, starting with read-only `list --top 1 --json`. Create, expose and delete mutate the tenant. Keep reports and captured secret output out of git and logs.
