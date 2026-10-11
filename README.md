# entra-appreg

**Version 1.0.1** — a Go CLI for Microsoft Entra ID single-page application registrations.

- **`create`** (default): create a SPA registration, optionally expose a scope and create a client secret, then save a report.
- **`expose-api`**: add a scope to an existing registration while preserving existing API configuration.
- **`list`**: read registrations, with optional filtering and JSON output.
- **`delete`**: delete an existing registration only after interactive confirmation.

Authentication reuses your **`az login`** session through the official Azure SDK for Go [AzureCLICredential](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity#AzureCLICredential). Graph calls use the typed [Microsoft Graph Go SDK](https://github.com/microsoftgraph/msgraph-sdk-go) (`msgraph-sdk-go`); the CLI uses [Cobra](https://github.com/spf13/cobra). The Go module is at the repository root; the earlier Rust implementation has been removed.

## Requirements and installation

To build, install [Go](https://go.dev/dl/) matching the `go` directive in `go.mod` (a newer local Go downloads the toolchain it needs automatically). `go.sum` pins dependencies. The first build compiles the large Graph SDK; it can take several minutes and several GB of RAM.

```sh
# From the repository root
go build -o entra-appreg ./cmd/entra-appreg
./entra-appreg --version
./entra-appreg --help
```

On Windows, use `-o entra-appreg.exe`. Local build outputs `entra-appreg[.exe]`, `build/` and `bin/` are ignored. The examples below assume `entra-appreg` is on `PATH`; otherwise use the path to the binary you built.

Prebuilt archives are published on the repository's **Releases** page by the [release workflow](.github/workflows/release.yml). Pick your platform, check its checksum, extract it, and put the executable on `PATH`. Running the binary needs no Go, Rust, Python or .NET toolchain.

Authenticated commands require **Azure CLI 2.54.0+** on `PATH` and appropriate tenant permissions. Azure CLI is an external runtime dependency: the SDK invokes it internally. Help and version do not need Azure login.

## Authentication and quick start

**Already signed in with `az login`?** No separate tool login, token export, or authentication app is needed. Inspect the selected identity and tenant before making changes:

```sh
az account show --query "{tenantId:tenantId,user:user.name}" --output json
entra-appreg list --top 5 --json
```

If not signed in, or selecting a different tenant, replace `YOUR_TENANT_ID`:

```sh
az login --tenant "YOUR_TENANT_ID"
```

For tenants without an Azure subscription, add `--allow-no-subscriptions`. The tool does not log in automatically or read Azure CLI's private token cache. `AZURE_CLIENT_SECRET` and other environment-based client-secret credentials are not selected as a fallback.

Every authenticated command first checks `az account show` for a usable login and validates its tenant ID. If no usable account is available, it stops before token acquisition and asks you to run `az login` or `az login --tenant <tenant-id>`. Failure to launch Azure CLI reports installation guidance; a token failure after a successful account check instead explains that the session may need reauthentication. A cached account is not proof of a valid session: token acquisition must still succeed. Errors go to stderr with exit 1, preserving empty stdout for failed JSON commands. The entire authentication operation retains its ten-second deadline.

Create pins token acquisition to the checked tenant. List, expose-api, and delete use the active Azure CLI context. The selected identity needs Graph read/manage permissions; token acquisition alone does not grant authorization. The SDK requires the numeric `expires_on` field supplied by Azure CLI 2.54.0+.

Login-check verification (Rust, historical) on macOS: all 50 tests, rustfmt, strict Clippy, compilation, and debug/release builds passed. Actual-binary smoke with an isolated fake Azure CLI verified signed-out, missing-executable, and token-failure guidance, secret-safe stderr, empty JSON stdout, and login-free help/version. The Go build ports the same messages and is covered by fake-runner tests; with no `az` on `PATH`, the real Go binary reports `Azure CLI was not found` and exits 1. No shared Azure session was changed. Live and Windows/Linux behavior remain unverified.

After verifying read access, this command **creates a real registration** and writes `My-SPA.txt` in your current directory:

```sh
entra-appreg create --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
```

Repeating it creates another registration unless `--appid` resolves to an existing app. Creation is not idempotent by name.

## Commands and options

Use space-separated values (`--name "My SPA"`, not `--name=...`). Option names are case-sensitive; commands are case-insensitive. Repeated scalar options use the last value; redirect options accumulate. Quote values containing spaces. `create` may be omitted when the first argument is an option.

```sh
entra-appreg --version
entra-appreg --help
entra-appreg create --help
entra-appreg expose-api --help
entra-appreg list --help
entra-appreg delete --help
```

Unknown commands fail before help/version handling. Otherwise `--version` takes precedence over help, and help takes precedence over option validation—even when these flags appear in a value position. No arguments prints help and exits 2.

### Create

| Option | Behavior |
|---|---|
| `--name <text>` | Required when creating a new registration; also used for the report filename. |
| `--redirect-urls <list>` / `--redirect-url <list>` | Optional; defaults to no redirect URIs. Comma-separated absolute HTTP(S) URIs; repeatable. Trims whitespace and removes exact duplicates, preserving path case and trailing slashes. |
| `--appid <id>` | Lookup by object ID or client ID. Existing app: exit without changes or report. Two lookup 404s: proceed to creation. Explicitly blank IDs are rejected; exact `no-id` sentinel skips lookup. |
| `--audience <value>` | Case-insensitive; default `AzureADMyOrg`. See account types below. |
| `--scope-*` | Optional API scope configuration; see Scope options. |
| `--create-secret` | Opt-in client secret on the target registration. Never embed this secret in browser code. |
| `--secret-expiry <value>` | `90`, `180`, `365`, `545`, `730` days, or `custom`; default 180. Requires `--create-secret`. |
| `--secret-start <DD/MM/YYYY>` | Custom start date, UTC; defaults to today and cannot be in the past. |
| `--secret-end <DD/MM/YYYY>` | Required for custom lifetime; strictly after start, valid through 23:59:59 UTC. |

Creation-field semantics are validated after authentication and optional existing-ID lookup, so an existing ID does not require name/redirect fields. Non-404 lookup failures never fall through to creation. Scope/secret detail options without their enabling option are rejected.

Redirects may be omitted: `entra-appreg create --name "My App"` sends an empty SPA redirect URI list (`"redirectUris":[]`; checked by a Go request-body test). Supplied URIs still require absolute HTTP(S) URLs. Live creation was not exercised.

Human-readable console identifiers use **Application (client) ID**, matching the existing report-file label. Machine-readable `AUTH_CLIENT_ID` and JSON keys remain unchanged.

Duplicate-name check: create pages through Graph's `displayName eq` results, rejects repeated next-page links, and never POSTs on a match or lookup failure. Go tests cover duplicate rejection, escaped names, paging, cycles, and creation after an empty lookup. No live tenant calls were made; uniqueness across concurrent creates is not guaranteed.

Dates imply custom expiry when `--secret-expiry` is omitted. Do not combine dates with a numeric preset. A future start is midnight UTC; today's start is omitted from the Graph payload. Tenant policy may cap secret lifetime independently.

Supported account types:

| Value | Allowed accounts |
|---|---|
| `AzureADMyOrg` | Current tenant only (default). |
| `AzureADMultipleOrgs` | Any Entra organization. |
| `AzureADandPersonalMicrosoftAccount` | Organizations and personal Microsoft accounts. |
| `PersonalMicrosoftAccount` | Personal Microsoft accounts only. |

Audiences supporting personal accounts request access-token version 2. Additional redirect restrictions are enforced by Graph.

```sh
# One scope with user consent; still no client secret
entra-appreg create --name "My SPA" --redirect-urls "http://localhost:5173/auth/callback,https://example.com/auth/callback" --scope-name access_as_user --scope-consent users

# Optional backend/client credential; report contains a plaintext secret
entra-appreg create --name "Backend client" --redirect-urls https://example.com/callback --create-secret --secret-expiry 90

# Existing registration: lookup-only no-op
entra-appreg create --appid "YOUR_CLIENT_OR_OBJECT_ID"
```

### Scope options

These apply to create and expose-api. Create exposes no API unless `--scope-name` is supplied; expose-api requires it.

| Option | Default / constraints |
|---|---|
| `--scope-name <value>` | 1–120 ASCII letters, digits, `.`, `_`, `-`; cannot start with `.`. |
| `--scope-consent <admins\|users>` | Admins only by default. Singular `admin`/`user` also accepted; case-insensitive. |
| `--scope-display-name <text>` | Admin display name; default `Access <app name>`. |
| `--scope-description <text>` | Default `Allow the application to access <app name> on behalf of the signed-in user.` |
| `--scope-user-display-name <text>` | Only for user consent; default `Access <app name>`. |
| `--scope-user-description <text>` | Only for user consent; default `Allow the application to access <app name> on your behalf.` |
| `--scope-state <enabled\|disabled>` | Enabled by default; a disabled scope exists but cannot be requested. |

Explicitly blank consent text is invalid. Admin-only scopes omit user-consent fields rather than send nulls. New registrations use Application ID URI `api://<client-id>`.

### Expose an existing API

```sh
entra-appreg expose-api --appid "YOUR_CLIENT_OR_OBJECT_ID" --scope-name access_as_user
```

Requires `--appid` and `--scope-name`; accepts the scope options above. Existing scopes, unknown API settings, and identifier URIs are preserved. A missing identifier URI is set to `api://<client-id>`. Duplicate scope names are rejected case-insensitively without a PATCH. A missing app fails; this command never creates an app, secret, or report. Create/list-only options are rejected.

### List registrations

```sh
entra-appreg list
entra-appreg list --name "My SPA" --top 10
entra-appreg list --top all --json
```

- `--name <text>`: trimmed, nonblank display-name prefix filter, not a substring search.
- `--top <n|all>`: positive signed-32-bit count, default 50; `all` traverses pages up to that integer limit.
- `--json`: pretty JSON array containing `displayName`, `appId`, and `id`, with no progress text on stdout.

List displays the tenant display name and tenant ID above the registrations, including when no registrations match. These details come from Graph `/organization?$select=id,displayName` using the same authenticated client, not the Azure subscription name. With `--json`, tenant details go to stderr so stdout remains the existing JSON array. The tenant lookup adds one read request; authorization errors or malformed organization responses stop the command rather than display an unknown or potentially misleading tenant.

List text output prints display names as Go-quoted strings (control characters escaped), so remote names cannot inject terminal escape sequences; the tenant line was already quoted. JSON output is unchanged. Live tenant authorization remains unverified.

The tool collects the first matching records returned by Graph, then sorts by name. It does not ask Graph to sort before limiting. Truncation notices go to stderr in JSON mode. Pagination rejects foreign-origin links and repeated links. List never writes a report or changes tenant state; options for other commands are rejected.

### Delete a registration

Verify the selected tenant with `az account show` first. This command can interrupt applications using the registration:

```sh
entra-appreg delete --appid "YOUR_CLIENT_OR_OBJECT_ID"
```

- `--appid <id>` is required. Accepts an application object ID or client ID, not a display name. Blank IDs, `no-id`, and options belonging to other commands are rejected before authentication.
- Looks up the registration, then shows its display name, object ID, and client ID. Remote fields are quoted and terminal control characters escaped.
- Requires terminal stdin and terminal stderr. Piped input and redirected prompts are rejected before authentication (exit 2); there is no `--yes` or force bypass.
- Type exactly **`yes`**, then Enter, to delete. Enter alone, any other answer, or EOF cancels without a DELETE request (exit 0). Input/output errors before confirmation also prevent deletion.
- Deletes the resolved **object ID**, even when lookup used a client ID. Only a lookup 404 allows the client-ID fallback; a missing app or Graph error exits 1. No retries, report, or local report cleanup.
- Uses the active `az login` context and requires permission to delete the target. Graph's delegated permission is `Application.ReadWrite.All`, with applicable user ownership/role requirements.
- Graph [soft-deletes applications for 30 days](https://learn.microsoft.com/en-us/graph/api/application-delete?view=graph-rest-1.0). This CLI does not restore or permanently purge deleted registrations.

## Output, failures, and security

Create prints `AUTH_APP_ID` (object ID) and `AUTH_CLIENT_ID` (client/application ID) immediately after Graph creates the registration, before optional mutations. Save these IDs for recovery. Reports contain IDs, tenant, redirects, and any requested scope/secret details. Filenames replace spaces with hyphens and are sanitized and collision-safe: `My-SPA.txt`, `My-SPA-1.txt`, etc.; existing files are not overwritten. Display names and report contents are unchanged.

On Windows, names that match a reserved device name (`CON`, `PRN`, `AUX`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`, ignoring case and any extension) get a leading `_`, for example `_NUL.txt`. Otherwise the report path would be a device and the secret would be lost.

| Exit | Meaning |
|---|---|
| 0 | Success, cancelled deletion, help, or version. |
| 1 | Authentication, Graph, file, or confirmation I/O failure. |
| 2 | Invalid arguments, no arguments, or non-interactive deletion. |

Errors go to stderr. Graph errors retain response bodies. Authentication diagnostics omit raw token output; access tokens stay in memory and are not included in reports.

**Reports can contain live plaintext client secrets.** Normal success does not print the secret. If report writing fails, the complete report—including the one-time secret—is printed to stdout for recovery and the command exits 1. If Graph's `addPassword` reply omits `keyId`, the report records `Secret ID: (not returned by Graph)`, keeps the secret, and the command then exits 1 with an error naming the missing field. Protect captured output; move secrets to a secure store such as Key Vault promptly. `.gitignore` excludes `*.txt` but cannot protect already-tracked files.

Unix reports are created with mode 0600; Windows inherits directory ACLs. Check directory access on Windows. Secret descriptions are capped at 128 UTF-16 units without splitting Unicode scalars. Reports use UTF-8 without BOM and platform line endings.

### Operational limits

- Create checks Graph's `displayName eq` filter before POST and rejects an existing name (exit 2). Name lookup failures prevent creation. This adds a Graph read-permission requirement and is not atomic: concurrent creates or delayed directory visibility can still produce duplicates. Existing `--appid` matches remain no-ops.
- No retry, rollback, or restore command. A later failure can leave an app already created. Recover using the printed IDs; do not blindly rerun create.
- Expose-api preserves the fetched snapshot, not concurrent edits; no conditional update/ETag scheme is used.
- One scope per invocation; no pre-authorization, custom Application ID URI editor, or add-secret-to-existing-app command.
- Authentication is bounded to ten seconds; cancellation kills the directly spawned process, but not necessarily all shell descendants. Graph requests have separate ten-second timeouts and no redirects/retries.
- Create/expose human output includes remote text verbatim. List text output and delete's confirmation prompt quote and escape remote fields. Prefer JSON when processing untrusted names.
- The Go build uses the typed Graph SDK, which has some visible effects:
  - A Graph field with an unexpected JSON type is read as absent instead of failing. For example, an `api` value that is not an object is treated as no API settings.
  - A malformed date in the `addPassword` reply fails the whole call, and that one-time secret cannot be recovered.
  - Report secret dates are reformatted as RFC 3339 UTC instead of printed exactly as Graph returned them.
  - Expose-api copies the fetched API settings into fresh models before PATCH (the SDK's backing store would otherwise send only changed fields). Unknown `api`/scope properties are kept; a test checks this.

### Troubleshooting

| Symptom | Check |
|---|---|
| Authentication failure | Azure CLI 2.54.0+ on PATH, `az account show`, then deliberate `az login` to the intended tenant. |
| Graph 403 | The error now leads with permission guidance. Check the identity/tenant with `az account show`; use `az login --tenant <tenant-id>` only if the context is wrong. Otherwise ask your Entra administrator to review Azure CLI's Graph permissions/admin consent and your directory roles or app ownership. Signing in alone does not grant access. Original Graph details remain attached for support. |
| Wrong tenant/app not found | Active CLI account; either object or client ID works with `--appid`. |
| Duplicate scope | Choose a new name; matching is case-insensitive. |
| `--name=value` rejected | Use a space: `--name value`. Single-dash options and `--` are also rejected. |
| Exit 2 | Command-specific required/forbidden options; run that command's `--help`. |
| Secret expiry rejected | Tenant lifetime policy; try a shorter supported lifetime. |

Application-read 403 errors name the delegated Graph permission `Application.Read.All` (or a sufficient broader permission), requiring admin consent for Azure CLI rather than the target registration. The signed-in user also needs directory read access; default member permissions can suffice, and Directory Readers is a supported read-only role. Azure subscription Owner/Contributor roles do not provide this access. This guidance appears for application GET requests, not for creation POST requests; Go tests check both the exact text and that the raw Graph body (including the request ID) is kept.

## Architecture and development

The Go module is at the repository root (`github.com/AaronSaikovski/entra-appreg`):

| Package | Role |
|---|---|
| `cmd/entra-appreg` | Entry point: parse → delete TTY gate → authenticate → Graph client → workflow → exit code. |
| `internal/cli` | Strict pre-parse, Cobra flag parsing (with Cobra's extra syntax turned off), validation, and help text. `Version` in `version.go` is the authoritative release version. |
| `internal/auth` | `az account show` tenant check, `AzureCLICredential` token under a ten-second deadline, then a static credential so Graph calls don't start `az` again. |
| `internal/graph` | `msgraph-sdk-go` client on a fixed public-cloud v1.0 base URL. 10 s timeout. Kiota's retry, redirect, and compression handlers are removed. Non-2xx responses become `StatusError` with the raw body. Next-page links are validated. |
| `internal/graphtest` | Loopback Graph fixture for tests only. |
| `internal/app` | create / expose-api / list / delete workflows, plus scope and lifetime helpers. |
| `internal/report` | Sanitized, collision-safe report files: 0600 on Unix, Windows reserved names prefixed with `_`. |

Requests run one at a time. There is no service, database or DI container. See [AGENTS.md](AGENTS.md) for code conventions.

From the repository root:

```sh
test -z "$(gofmt -l .)"
go vet ./...
go build ./...
go test ./...

go build -o entra-appreg ./cmd/entra-appreg
./entra-appreg --version
./entra-appreg delete --help
# Expected exit 2 before authentication
./entra-appreg list --top 0
```

Tests are offline. They use `graphtest`, fake `az` runners and credentials, frozen clocks, and temporary directories. They cover parser strictness, tenant selection, secret-safe auth failures, lookup fallback, paging safety, API preservation, confirmed deletion, report collisions/permissions/recovery, and exit codes.

### CI and releases

[CI](.github/workflows/ci.yml) runs `Go checks (...)` on branch pushes, pull requests and manual dispatch, on Linux, macOS and Windows: gofmt, `go vet`, build, tests, and a real-binary smoke. The smoke checks `--version` against `version.go`, all five help topics, `list --top 0` (exit 2), and non-terminal `delete` (exit 2). Set up branch protection separately.

The repository also contains `build.yml`, `test.yml`, `goreleaser.yml` and a GoReleaser-based `release.yml`; see those files for release behavior. Release tags should match `Version` in `internal/cli/version.go`.

#### Making a release

1. Update `Version` in `internal/cli/version.go`, this README, the changelog, and any affected tests.
2. Run the Go quality gates and smoke commands, then commit.
3. Push a matching `v`-prefixed tag, e.g. `git tag -a v1.0.1 -m "Release v1.0.1" && git push origin v1.0.1`.

### After every review or change

Update README behavior/usage/limits and [CHANGELOG.md](CHANGELOG.md) Unreleased before completion, including review outcomes and actual verification evidence. New options require parser, validation, supplied-option groups, help, and README updates. Keep unreleased work at the current version until deliberately releasing. Distinguish historical results from current checks, and offline checks from live integration.

## Smoke test checklist

Offline: run version and all five help topics without login, then `list --top 0`, blank `--appid`, and `delete` without `--appid` validation. Also check that `delete --appid <id>` rejects piped input or redirected stderr with exit 2 before authentication. None of these invalid invocations should authenticate, mutate Graph, or create a report.

For live acceptance, deliberately choose a **test tenant**; do not change or log out a shared operator session as a routine check:

1. Read-only `list --top 1 --json`: valid JSON-only stdout, correct tenant, no report.
2. Create a test SPA: inspect portal audience/redirect settings, distinct IDs, and report. Exact URI duplicates are removed; case-distinct paths remain.
3. Repeat using existing `--appid`: no creation, mutation, or new report.
4. Expose a scope: preserve existing API settings/scopes/URIs; a case-variant duplicate must fail without PATCH.
5. Opt in to a test secret: inspect expiry, report contents, and file permissions; keep captured output private.
6. Check invalid scope/lifetime options before mutations, and signed-out behavior only in an isolated CLI profile.
7. Remove only disposable test registrations deliberately: run `delete --appid <test-id>`, inspect the displayed IDs, first cancel and confirm the registration remains, then rerun and type `yes`. Confirm removal with read-only list or the portal. Protect or dispose of local secret-bearing reports separately; deletion does not remove them.

Offline tests do not prove live Graph authorization, Windows ACL behavior, or GitHub publication. Current verification evidence is recorded in the changelog.

## License and history

[MIT license](LICENSE). See [CHANGELOG.md](CHANGELOG.md) for release history and unreleased changes.
