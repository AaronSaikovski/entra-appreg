# entra-appreg

**In testing (first release: v0.0.1).** A Go CLI for Microsoft Entra ID single-page application (SPA) registrations. `entra-appreg --version` reports the git tag the binary was built from.

| Command | What it does |
|---|---|
| `create` (default) | Creates a SPA registration. It can also expose an API scope and create a client secret. It then writes a report file. |
| `expose-api` | Adds a scope to an existing registration and keeps its existing API configuration. |
| `list` | Reads registrations; optional name filter and JSON output. |
| `delete` | Deletes a registration, but only after you confirm interactively. |

The CLI signs in through your existing **`az login`** session, using the Azure SDK for Go [`AzureCLICredential`](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity#AzureCLICredential). Graph calls use the typed [Microsoft Graph Go SDK](https://github.com/microsoftgraph/msgraph-sdk-go), and the command line is built with [Cobra](https://github.com/spf13/cobra).

> This release is in testing. Use a test tenant until you have checked the behavior you rely on.

## Install

**Prebuilt binaries.** Download an archive from the repository's **Releases** page. Builds exist for Linux and macOS (amd64 and arm64) and Windows (amd64). Verify it against `entra-appreg-<tag>-checksums.txt`, extract it, and put `entra-appreg` on your `PATH`.

**From source.** You need [Go](https://go.dev/dl/) matching the `go` directive in `go.mod`; a newer Go downloads the right toolchain automatically. The first build compiles the large Graph SDK, which can take several minutes and several GB of RAM.

```sh
task build                         # bin/entra-appreg, version from `git describe --tags`
bin/entra-appreg --version
```

On Windows the binary is `entra-appreg.exe`.

**Runtime requirement.** Commands that talk to Graph need **Azure CLI 2.54.0+** on `PATH`, plus the right tenant permissions. Help and version work without signing in.

## Quick start

Check which identity and tenant are selected before changing anything:

```sh
az account show --query "{tenantId:tenantId,user:user.name}" --output json
entra-appreg list --top 5 --json
```

To sign in, or to switch tenant, run `az login --tenant "YOUR_TENANT_ID"`. For a tenant with no Azure subscription, add `--allow-no-subscriptions`.

The following command **creates a real registration** and writes `My-SPA.txt` in the current directory:

```sh
entra-appreg create --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
```

Running it again creates a second registration. Creation is not idempotent; use `--appid` to refer to an existing app.

## Authentication

- Each Graph command starts Azure CLI **once**: the SDK runs `az account get-access-token` for the active `az login` context. First the CLI checks that `az` is on `PATH`, without starting it. Sign-in must finish within 10 seconds.
- The tenant ID shown by `list` and written to `create` reports comes from Microsoft Graph (`/organization`) using the same token, so it always matches the tenant the token was issued for. To work in a different tenant, switch with `az login --tenant <tenant-id>` first.
- The CLI never signs in for you or reads Azure CLI's token cache. It does not fall back to `AZURE_CLIENT_SECRET` or any other environment credential.
- Auth error messages are fixed text and never include Azure CLI output or tokens. They go to stderr and the command exits 1, so stdout stays empty for `--json`.
- Getting a token does not grant permissions. The signed-in identity still needs the right Graph permissions; see [Permissions and 403 errors](#permissions-and-403-errors).

## Commands and options

Separate options from their values with a space: `--name "My SPA"`, not `--name=...`. Single-dash options and a bare `--` are rejected.

Option names are case-sensitive; command names are not. If an option is repeated, the last value wins, except redirect options, which add up. If the first argument is an option, the command defaults to `create`.

`--version` takes priority over `--help`, and `--help` takes priority over option checks, even when they appear where a value is expected. Running with no arguments prints help and exits 2.

```sh
entra-appreg --help
entra-appreg create --help      # also: expose-api, list, delete
```

### create

| Option | Behavior |
|---|---|
| `--name <text>` | Required for a new registration. Also used for the report filename. |
| `--redirect-urls <list>` / `--redirect-url <list>` | Optional. Comma-separated absolute HTTP(S) URIs; you can repeat the option. Whitespace is trimmed and exact duplicates removed. Case and trailing slashes are kept. If omitted, an empty redirect list is sent. |
| `--appid <id>` | Looks up an existing app by object ID or client ID. If found, the command exits 0 without changing anything or writing a report. If both lookups return 404, it creates a new app. A blank value is rejected; `no-id` skips the lookup. |
| `--audience <value>` | Case-insensitive; default `AzureADMyOrg`. See the table below. |
| `--scope-*` | Optional API scope. See [Scope options](#scope-options). |
| `--create-secret` | Opt in to a client secret on the new registration. Never embed this secret in browser code. |
| `--secret-expiry <value>` | `90`, `180`, `365`, `545` or `730` days, or `custom`. Default 180. Requires `--create-secret`. |
| `--secret-start <DD/MM/YYYY>` | Custom start date in UTC. Defaults to today and cannot be in the past. |
| `--secret-end <DD/MM/YYYY>` | Required for a custom lifetime. Must be after the start; valid until 23:59:59 UTC. |

| Audience | Who can sign in |
|---|---|
| `AzureADMyOrg` | This tenant only (default). |
| `AzureADMultipleOrgs` | Any Entra organization. |
| `AzureADandPersonalMicrosoftAccount` | Organizations and personal Microsoft accounts. |
| `PersonalMicrosoftAccount` | Personal Microsoft accounts only. |

Validation runs in this order:

- **Lookup first.** Name, redirect, scope and secret options are checked only after sign-in and any `--appid` lookup, so an existing app needs no other options. A lookup failure other than 404 never falls through to creation.
- **Duplicate names.** Before creating, `create` checks Graph for an app with the same `displayName`. A match stops the command with exit 2.
- **Personal accounts.** Audiences that include personal accounts request access-token version 2.
- **Secret dates.** Giving a date without `--secret-expiry` implies `custom`. Don't combine dates with a numeric preset. A future start date is midnight UTC; a start of today is not sent to Graph. Tenant policy may cap the lifetime further.

```sh
# Scope with user consent, no secret
entra-appreg create --name "My SPA" --redirect-urls "http://localhost:5173/auth/callback,https://example.com/auth/callback" --scope-name access_as_user --scope-consent users

# Client secret (the report contains it in plaintext)
entra-appreg create --name "Backend client" --redirect-urls https://example.com/callback --create-secret --secret-expiry 90

# Existing registration: look it up only; nothing is changed
entra-appreg create --appid "YOUR_CLIENT_OR_OBJECT_ID"
```

### Scope options

These apply to `create` and `expose-api`. `create` exposes no API unless you pass `--scope-name`; `expose-api` requires it.

| Option | Default / constraints |
|---|---|
| `--scope-name <value>` | 1–120 ASCII letters, digits, `.`, `_` or `-`. Cannot start with `.`. |
| `--scope-consent <admins\|users>` | Default `admins`. `admin`/`user` also work; case-insensitive. |
| `--scope-display-name <text>` | Admin display name. Default `Access <app name>`. |
| `--scope-description <text>` | Default `Allow the application to access <app name> on behalf of the signed-in user.` |
| `--scope-user-display-name <text>` | User consent only. Default `Access <app name>`. |
| `--scope-user-description <text>` | User consent only. Default `Allow the application to access <app name> on your behalf.` |
| `--scope-state <enabled\|disabled>` | Default `enabled`. A disabled scope exists but cannot be requested. |

Blank consent text is rejected. Admin-only scopes leave out the user-consent fields instead of sending nulls. New registrations get the Application ID URI `api://<client-id>`.

### expose-api

```sh
entra-appreg expose-api --appid "YOUR_CLIENT_OR_OBJECT_ID" --scope-name access_as_user
```

- Requires `--appid` and `--scope-name`.
- Keeps existing scopes, API settings (including ones the CLI doesn't know about) and identifier URIs. If the app has no identifier URI, it sets `api://<client-id>`.
- A scope name that already exists, compared case-insensitively, fails without sending any update.
- It never creates an app, a secret or a report.

### list

```sh
entra-appreg list
entra-appreg list --name "My SPA" --top 10
entra-appreg list --top all --json
```

| Option | Behavior |
|---|---|
| `--name <text>` | Display-name prefix filter (trimmed, not blank). It matches the start of the name, not anywhere in it. |
| `--top <n\|all>` | Positive 32-bit count; default 50. `all` reads every page. |
| `--json` | Prints only a JSON array of `displayName`, `appId` and `id` on stdout. |

- **Tenant line.** The tenant name and ID are printed first; they come from Graph `/organization`. With `--json` they go to stderr.
- **Sorting.** The first matching records are collected, then sorted by name.
- **Safe names.** In text mode, names are printed in quotes with control characters escaped, so a crafted name can't control your terminal.
- **Paging safety.** Next-page links must stay on Graph's `/v1.0/` path, and a repeated link stops the command.
- **Read-only.** `list` never writes a report or changes the tenant.

### delete

```sh
entra-appreg delete --appid "YOUR_CLIENT_OR_OBJECT_ID"
```

- Requires `--appid`: an object ID or client ID, not a display name.
- Requires a terminal on both stdin and stderr. Piped input or a redirected prompt exits 2 before sign-in. There is no `--yes`/force option.
- Looks up the app, then shows its name, object ID and client ID (quoted and escaped).
- Type exactly **`yes`** and press Enter to delete. Anything else, a bare Enter, or end-of-input cancels with exit 0.
- Deletes by the resolved **object ID**, even if you passed a client ID. There are no retries.
- Needs delete permission (`Application.ReadWrite.All` or ownership). Graph [soft-deletes for 30 days](https://learn.microsoft.com/en-us/graph/api/application-delete?view=graph-rest-1.0); this CLI does not restore or purge.

## Output, reports and secrets

- **IDs printed early.** `create` prints `AUTH_APP_ID=<object id>` and `AUTH_CLIENT_ID=<client id>` right after the app is created, before the optional scope and secret steps. Save them so you can recover if a later step fails.
- **Report files.** Each report gets a new file in the current directory: `My-SPA.txt`, then `My-SPA-1.txt`, and so on. Existing files are never overwritten.
  - Spaces in the name become hyphens, and unsafe characters are replaced.
  - On Windows, reserved device names get a `_` prefix (e.g. `_NUL.txt`).
  - On Unix the file is created with mode 0600. On Windows it inherits the folder's permissions.
- **Reports can contain a plaintext client secret.**
  - A successful run never prints the secret to the terminal.
  - If writing the report fails, the whole report, including the secret, is printed to stdout so it isn't lost, and the command exits 1.
  - If Graph doesn't return the secret's `keyId`, the report records `(not returned by Graph)` and keeps the secret, and the command exits 1.
  - Move secrets to a secure store such as Key Vault. `.gitignore` excludes `*.txt`, but that can't protect files already committed.

| Exit | Meaning |
|---|---|
| 0 | Success, cancelled delete, help, or version. |
| 1 | Sign-in, Graph, file, or confirmation I/O failure. |
| 2 | Invalid arguments, no arguments, duplicate name, or a non-interactive delete. |

Errors go to stderr. Graph errors include Graph's response body, capped at 1 MiB; anything longer is cut there and marked as truncated. Tokens are kept in memory only.

## Limits

- **No undo.** There is no retry, rollback or restore. A failure after creation can leave the app created; recover with the printed IDs instead of rerunning `create`.
- **Duplicate-name check isn't atomic.** Two creates running at the same time, or a slow directory sync, can still produce duplicates.
- **Concurrent edits.** `expose-api` works from the copy it fetched; it does not protect against someone else editing the app at the same moment.
- **Missing features.** One scope per run. No pre-authorization, Application ID URI editor, or adding a secret to an existing app.
- **Timeouts.** Each Graph request has a 10-second timeout and never follows redirects. If the 10-second sign-in deadline expires, the CLI kills the `az` process it started; processes `az` itself started may keep running.
- **Unescaped output.** `create` and `expose-api` print remote text as-is. Use JSON when you process untrusted names.
- **Side effects of the typed Graph SDK:**
  - A field with an unexpected JSON type is treated as missing instead of causing an error.
  - A malformed date in the `addPassword` reply fails the call, and that secret can't be recovered.
  - Report dates are written in RFC 3339 UTC, not exactly as Graph returned them.

## Permissions and 403 errors

A Graph 403 error starts with plain-English guidance and keeps Graph's original details for support.

Reading applications needs the delegated Graph permission `Application.Read.All` or broader, with admin consent for **Azure CLI**. The signed-in user also needs directory read access; default member permissions can be enough, and Directory Readers is a supported read-only role. Azure subscription Owner/Contributor roles do not grant this access.

| Symptom | Check |
|---|---|
| Sign-in failure | Azure CLI 2.54.0+ is on `PATH`; run `az account show`, then `az login` to the intended tenant. |
| Graph 403 | Confirm identity and tenant with `az account show`. If they're right, ask your Entra admin to review Azure CLI's Graph consent and your roles or app ownership. |
| App not found / wrong tenant | Check the active CLI account. Both object ID and client ID work with `--appid`. |
| `--name=value` rejected | Use a space: `--name value`. |
| Exit 2 | A required option is missing or an option doesn't belong to that command; run the command's `--help`. |
| Secret expiry rejected | Tenant lifetime policy; try a shorter lifetime. |

## Development

The Go module `github.com/AaronSaikovski/entra-appreg` is at the repository root:

| Package | Role |
|---|---|
| `cmd/entra-appreg` | Entry point: parse → delete terminal check → sign in → Graph client → command → exit code. |
| `internal/cli` | Strict parsing, Cobra flags, validation, help text; `version.go` resolves the build version (see [Versioning](#versioning)). |
| `internal/auth` | Checks `az` is on `PATH`, then gets one token from Azure CLI within 10 seconds. |
| `internal/graph` | Graph client: no retries or redirects, raw error bodies, next-page link checks. |
| `internal/graphtest` | Local fake Graph server for tests. |
| `internal/app` | The four commands, plus scope and secret-lifetime rules. |
| `internal/report` | Safe, collision-free report files. |

Common tasks ([Task](https://taskfile.dev) is optional):

```sh
task check        # gofmt, vet (native + GOOS=windows), build, tests, binary smoke incl. --version == git describe
task test         # go test -v ./...
task run -- list --help
task staticcheck  # pinned Go-1.27-compatible staticcheck
task seccheck     # govulncheck
```

Without Task:

```sh
test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test ./...
```

Tests are offline. They use the fake Graph server, a fake Azure CLI runner and credential, a frozen clock and temporary folders; they never sign in. Code conventions for contributors and AI assistants are in [AGENTS.md](AGENTS.md).

### CI and releases

| Workflow | Runs on | What it does |
|---|---|---|
| [`ci.yml`](.github/workflows/ci.yml) | All pushes, PRs, manual | Linux/macOS/Windows: gofmt, vet, build, tests, and a smoke test of the built binary (version, all help topics, exit codes). |
| [`build.yml`](.github/workflows/build.yml) | Push to `main`, PRs | vet, a `go fmt` diff check, build, and `goreleaser check`. |
| [`test.yml`](.github/workflows/test.yml) | Push/PR to `main` | `go test -race`, vet, staticcheck, govulncheck. |
| [`release.yml`](.github/workflows/release.yml) | `v*` tags | Checks that a build stamped with the tag reports it, runs gofmt, vet and the tests, then GoReleaser ([`.goreleaser.yaml`](.goreleaser.yaml)). |

All workflows use the Go version from `go.mod`. `staticcheck.conf` turns off only the style check for error-message format, because the CLI's user-facing messages are intentionally capitalised and punctuated.

### Versioning

The version comes from **git tags**; no version number is stored in the source. The build stamps it into the binary via `-ldflags "-X github.com/AaronSaikovski/entra-appreg/internal/cli.version=<tag>"`. `--version` and the help heading print it without the leading `v`.

| How it was built | `--version` shows |
|---|---|
| GoReleaser release from tag `v0.0.1` | `0.0.1` |
| `task build` / `task release` / `task run` | `git describe --tags --always --dirty`, e.g. `0.0.1` on the tag, or `0.0.1-3-gabc1234-dirty` between tags |
| `go install github.com/AaronSaikovski/entra-appreg/cmd/entra-appreg@v0.0.1` | `0.0.1` (module version Go records) |
| plain `go build` in a git checkout | Go's VCS pseudo-version, e.g. `0.0.2-0.2026…-abc123+dirty` |
| no tag or build info | `dev` |

To release:

1. Update `CHANGELOG.md` (move Unreleased entries under the new version), run `task check`, then commit.
2. Tag and push: `git tag -a v0.0.1 -m "Release v0.0.1" && git push origin v0.0.1`. The tag must be semver with a `v` prefix; a hyphenated tag (e.g. `v0.1.0-rc.1`) publishes a prerelease.

For a local dry run, use `goreleaser release --snapshot --clean`; output goes to `dist/`, which is ignored by git.

After every change, update this README for behavior or limit changes, and add a `CHANGELOG.md` Unreleased entry listing the checks you actually ran.

## Smoke test checklist

**Offline** (no sign-in needed):

- `--version` and all five help topics.
- `list --top 0`, a blank `--appid`, and `delete` without `--appid` each exit 2.
- `delete --appid <id>` with piped input exits 2 before sign-in.

**Live**, only in a deliberately chosen **test tenant**. Never log out or switch a shared operator session.

1. `list --top 1 --json`: stdout is valid JSON only, the tenant is correct, and no report is written.
2. Create a test SPA. Check audience and redirects in the portal, the two IDs, and the report.
3. Run create again with the existing `--appid`: nothing is created or changed, and no report is written.
4. Expose a scope. Existing settings are kept, and a duplicate name that differs only in case fails without an update.
5. Create a test secret. Check the expiry, the report contents, and the file permissions.
6. Remove disposable registrations with `delete`: cancel once, check the app still exists, then confirm with `yes`.

## License

[MIT](LICENSE). Release history is in [CHANGELOG.md](CHANGELOG.md).
