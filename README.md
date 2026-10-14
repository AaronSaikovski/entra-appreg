# entra-appreg

**In testing (first release: v0.0.1).** A Go CLI for Microsoft Entra ID single-page application (SPA) registrations. `entra-appreg --version` reports the git tag, commit and build time the binary was built from.

| Command | What it does |
|---|---|
| `create` (default) | Creates a SPA registration together with its service principal and the Microsoft Graph `User.Read` permission. It can also expose an API scope and create a client secret. It then writes a report file. |
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

To keep the settings in a file, use the samples in [`examples/`](examples/); see [Parameters file](#parameters-file). A browser app that calls its own API needs two registrations, and the API comes first:

```sh
# 1. API registration: no redirect URIs, admins-only access_as_user scope, v2 access
#    tokens with auth_time, and a server-side client secret stored in Key Vault
entra-appreg create --params-file examples/api.params.json

# 2. SPA registration: redirect URIs only, no scope and no secret (a public client)
entra-appreg create --params-file examples/spa.params.json
```

Copy the samples and change `name`, the redirect URIs and the vault first. Then, in the portal, give the SPA's **API permissions** `My APIs → <api> → access_as_user` and grant admin consent; `create` doesn't link the two registrations.

## Authentication

- Each Graph command starts Azure CLI **once**: the SDK runs `az account get-access-token` for the active `az login` context. First the CLI checks that `az` is on `PATH`, without starting it. Sign-in must finish within 10 seconds. `create --keyvault-uri` starts it a second time, for a Key Vault token, before anything is created.
- The tenant ID shown by `list` and written to `create` reports comes from Microsoft Graph (`/organization`) using the same token, so it always matches the tenant the token was issued for. To work in a different tenant, switch with `az login --tenant <tenant-id>` first.
- The CLI never signs in for you or reads Azure CLI's token cache. It does not fall back to `AZURE_CLIENT_SECRET` or any other environment credential.
- Auth error messages are fixed text and never include Azure CLI output or tokens. They go to stderr and the command exits 1, so stdout stays empty for `--json`.
- Getting a token does not grant permissions. The signed-in identity still needs the right Graph permissions; see [Permissions and 403 errors](#permissions-and-403-errors).

## Commands and options

Separate options from their values with a space: `--name "My SPA"`, not `--name=...`. Single-dash options and a bare `--` are rejected.

Option names are case-sensitive; command names are not (ASCII letters only, so `LİST` is an unknown command). If an option is repeated, the last value wins, except redirect options, which add up. If the first argument is an option, the command defaults to `create`.

`--version` takes priority over `--help`, and `--help` takes priority over option checks, even when they appear where a value is expected. Running with no arguments prints help and exits 2.

```sh
entra-appreg --help
entra-appreg create --help      # also: expose-api, list, delete
```

### Parameters file

Every command accepts `--params-file <file.json>`. The file holds one JSON object; its keys are the long option names without `--`. The samples are both `create` inputs: [`examples/api.params.json`](examples/api.params.json) for the API registration and [`examples/spa.params.json`](examples/spa.params.json) for the SPA:

```json
{
  "name": "my-web-dev",
  "audience": "AzureADMyOrg",
  "redirect-urls": ["https://dev.example.com", "https://www.dev.example.com", "https://dev.example.com/auth/callback"]
}
```

- **Only when asked.** A file is read only when you pass `--params-file`; there is no default file, and the samples in `examples/` are never loaded on their own.
- **Command line wins.** An option given on the command line replaces the file's value. For `redirect-urls`, command-line values replace the file's whole list; they are not merged.
- **Same rules.** File values are checked exactly like command-line ones, including which options a command accepts. For example, `top` in a file used with `create` exits 2.
- **Types.** Text options take JSON strings. `redirect-urls` takes an array of strings or one comma-separated string. `create-secret` and `json` take `true`/`false`; `false` is the same as leaving the key out. `top` and `secret-expiry` also accept whole numbers (`"top": 10`). `null` counts as absent.
- **Strict.** Unknown keys (including the `redirect-url` alias and a nested `params-file`), wrong types, invalid JSON, more than one top-level value, or a file over 1 MiB exit 2 before sign-in. Help and version never read the file.
- The path is relative to the current directory. Don't put secrets in the file; no option takes one.
- **Samples are tested.** `go test ./internal/cli` loads both samples through the real parser and checks every value, the command-line override, and that `list` rejects the SPA sample. If you edit a sample in the repo, update `TestSampleParamsFilesParse`; edit a copy to keep your own values.

### create

| Option | Behavior |
|---|---|
| `--name <text>` | Required for a new registration. Also used for the report filename. |
| `--redirect-urls <list>` / `--redirect-url <list>` | Optional. Comma-separated absolute HTTP(S) URIs; you can repeat the option. Whitespace is trimmed and exact duplicates removed. Case and trailing slashes are kept. If omitted, an empty redirect list is sent and a warning goes to stderr, because a SPA without a redirect URI cannot sign anyone in. |
| `--appid <id>` | Looks up an existing app by object ID or client ID. If found, the command exits 0 without changing anything or writing a report. If both lookups return 404, it creates a new app. A blank value is rejected; `no-id` skips the lookup. |
| `--audience <value>` | Case-insensitive; default `AzureADMyOrg`. See the table below. |
| `--scope-*` | Optional API scope. See [Scope options](#scope-options). |
| `--create-secret` | Opt in to a client secret on the new registration. Never embed this secret in browser code. |
| `--secret-expiry <value>` | `90`, `180`, `365`, `545` or `730` days, or `custom`. Default 180. Requires `--create-secret`. |
| `--secret-start <DD/MM/YYYY>` | Custom start date in UTC. Defaults to today and cannot be in the past. |
| `--secret-end <DD/MM/YYYY>` | Required for a custom lifetime. Must be after the start; valid until 23:59:59 UTC. |
| `--keyvault-uri <uri>` / `--keyvault-secret-name <name>` | Also store the new secret in Azure Key Vault. See [Store the secret in Key Vault](#store-the-secret-in-key-vault). |
| `--params-file <file.json>` | Reads any of the options above from JSON. See [Parameters file](#parameters-file). |

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
- **Exposed APIs.** Whenever a registration exposes a scope (`create --scope-name`, `expose-api`), the scope PATCH sets `api.requestedAccessTokenVersion` to 2 and adds the access-token optional claim `auth_time` (`essential: false`). Access tokens belong to the resource they are issued for, so both go on the API registration itself. A `create` without a scope sets neither. `expose-api` keeps existing optional claims and doesn't add `auth_time` twice.
- **Secret dates.** Giving a date without `--secret-expiry` implies `custom`. Don't combine dates with a numeric preset. A future start date is midnight UTC; a start of today is not sent to Graph. Tenant policy may cap the lifetime further.

#### Single-page app setup

Graph's `POST /applications` creates only the registration. `create` adds what the Entra admin center adds, so the SPA can sign users in:

| Step | Why |
|---|---|
| `spa.redirectUris` | Authorization code flow with PKCE for MSAL.js. No implicit grant is enabled. |
| Service principal (`POST /servicePrincipals`), printed as `AUTH_SP_ID` | The enterprise application in your tenant. Without it, admins can't grant consent, tenants that restrict user consent block sign-in, and tokens for the app's own `api://` scope fail with AADSTS500011. |
| Delegated Microsoft Graph `User.Read` in `requiredResourceAccess` | The portal default. It lets an admin use **Grant admin consent** before anyone signs in. |
| With `--scope-name`: the app's own scope added to `requiredResourceAccess` | Admin consent then covers `api://<client-id>/<scope>` too. This matters most for the default admins-only scope, which users can't consent to. |

Still up to you: granting consent (users at first sign-in, or an admin via **Enterprise applications → Permissions**), and adding production redirect URIs later in the portal. `expose-api` doesn't change `requiredResourceAccess`.

#### SPA readiness and known gaps

For a new single-tenant SPA, `create` sets every registration setting that MSAL.js sign-in needs: SPA redirect URIs (auth code + PKCE, no implicit grant), the audience, the service principal, Graph `User.Read`, and optionally its own API scope. Known gaps:

| Gap | Impact | Workaround |
|---|---|---|
| Not yet verified against a live tenant | All Graph calls are tested only against a local fake. Not yet confirmed: Graph accepting the app's own scope in `requiredResourceAccess` in the same PATCH that defines it, and service principal creation right after the app. | Run the [smoke test checklist](#smoke-test-checklist) in a test tenant. |
| Redirect URIs can't be changed after creation | Production URLs added later need the portal. | Entra admin center → **Authentication**. |
| No admin consent grant | Tenants that block user consent, and admins-only scopes (the default), need an admin before users can sign in. | **Grant admin consent** in the portal. |
| Loose redirect validation | Any `http://` host is accepted locally. Entra is expected to allow plain `http` only for localhost, so such URIs fail at Graph or sign-in instead of exiting 2. | Use `https` except for `localhost`. |
| `expose-api` doesn't update `requiredResourceAccess` | Admin consent on an existing app doesn't cover the new scope. | Add it under **API permissions → My APIs**. |

Examples:

```sh
# API registration: admins-only scope (sets v2 tokens and auth_time), secret only in Key Vault
entra-appreg create --name my-api-dev --scope-name access_as_user --scope-consent admins --create-secret --secret-expiry 365 --keyvault-uri https://my-vault.vault.azure.net --keyvault-secret-name GraphClientSecret

# SPA registration that calls that API: redirects only, no scope, no secret
entra-appreg create --name my-web-dev --redirect-urls "https://dev.example.com,https://www.dev.example.com,https://dev.example.com/auth/callback"

# Client secret (the report contains it in plaintext)
entra-appreg create --name "Backend client" --create-secret --secret-expiry 90

# Add a scope to an API registration that already exists (also sets v2 and auth_time)
entra-appreg expose-api --appid "YOUR_API_CLIENT_OR_OBJECT_ID" --scope-name access_as_user

# Existing registration: look it up only; nothing is changed
entra-appreg create --appid "YOUR_CLIENT_OR_OBJECT_ID"
```

#### Store the secret in Key Vault

With `--create-secret`, add both `--keyvault-uri https://<vault-name>.vault.azure.net` and `--keyvault-secret-name <name>` to also write the new secret value to Azure Key Vault. This uses the [Azure SDK for Go `azsecrets`](https://learn.microsoft.com/en-us/azure/key-vault/secrets/quick-create-go) client.

- **Validation.** Both options are required together and only with `--create-secret`. The URI must be `https://<vault-name>.vault.azure.net` (public cloud; no path, port or query; only ASCII letters fold case, so look-alike characters such as the Kelvin sign `K` are rejected), and the name must be 1–127 letters, digits or hyphens. Like the other `create` options, they are checked after sign-in and any `--appid` lookup. Bad values exit 2 before anything is created and before the Key Vault token is requested.
- **Sign-in.** A Key Vault token comes from a second `az` call, made after the read-only checks and before the app is created, so a missing token changes nothing. The vault must be in the tenant of your active `az login`. The SDK handles Key Vault's challenge and checks that the challenge comes from a `vault.azure.net` resource.
- **Permission.** Your `az login` identity needs the **Key Vault Secrets Officer** role on the vault. See [Key Vault permissions](#key-vault-permissions).
- **What is stored.** The secret value, with `exp` set to the secret's expiry, `nbf` set to a future custom start date, and tags `entra-client-id` and `entra-key-id`. If the name already exists, Key Vault adds a new version. A soft-deleted secret with the same name fails with HTTP 409.
- **Output.** stdout prints `Key Vault secret ID: <version id>`, and the report adds `Key Vault secret:` and `Key Vault secret ID:` lines. Once Key Vault confirms the store, the report **does not contain the secret**: `Secret value:` reads `(stored in Key Vault: <version id>)`.
- **Failure.** If storing fails after the secret is created, the report records `Key Vault secret ID: (not stored)` and keeps the secret, stderr says so, and the command exits 1. There are no retries; each request has a 10-second timeout and redirects aren't followed.

##### Key Vault permissions

The secret is written as the signed-in `az login` identity. **Assign that identity the built-in Azure role `Key Vault Secrets Officer` on the vault.** It is the least-privileged built-in role that can create secrets (data action `Microsoft.KeyVault/vaults/secrets/setSecret/action`).

```sh
az role assignment create \
  --role "Key Vault Secrets Officer" \
  --assignee "<your-upn-or-object-id>" \
  --scope "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.KeyVault/vaults/<vault-name>"
```

- **Who can assign it.** Assigning the role needs **Owner**, **User Access Administrator** or **Role Based Access Control Administrator** at the vault's scope. New assignments can take a few minutes to apply.
- **The vault must use Azure RBAC** (**Settings → Access configuration → Azure role-based access control**, the default for new vaults). If it still uses the legacy **Vault access policy** model, role assignments are ignored; instead add an access policy for your user with the **Secret permission: Set**.
- **Roles that are not enough** (HTTP 403):
  - subscription or resource-group **Owner** / **Contributor**, and **Key Vault Contributor** (management-plane only);
  - **Key Vault Secrets User** and **Key Vault Reader** (read-only).
  - **Key Vault Administrator** works but grants far more than needed.
- **Same tenant.** The vault must be in the tenant of your active `az login`; otherwise HTTP 401.
- **Network.** If the vault's firewall restricts access, allow your client IP or run from a network that reaches its private endpoint; otherwise HTTP 403 (`ForbiddenByFirewall`).
- **Graph is separate.** Creating the app, service principal and client secret still needs the Graph permissions in [Permissions and 403 errors](#permissions-and-403-errors). The Key Vault role is needed only with `--keyvault-uri`.

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
- **Sorting.** The first matching records are collected, then sorted by name, ignoring case for ASCII letters only (stable for equal names).
- **Safe names.** In text mode, names are printed in quotes with control characters escaped, so a crafted name can't control your terminal.
- **Paging safety.** Next-page links must stay on Graph's origin (scheme, host compared with ASCII-only case folding, and port) and `/v1.0/` path, and a repeated link stops the command.
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

- **IDs printed early.** `create` prints `AUTH_APP_ID=<object id>` and `AUTH_CLIENT_ID=<client id>` right after the app is created, then `AUTH_SP_ID=<service principal id>` once the service principal exists. These come before the optional scope and secret steps. Save them so you can recover if a later step fails. If the service principal step fails, stderr shows the `az ad sp create --id <client-id>` command to finish it by hand.
- **Report files.** Each report gets a new file in the current directory: `My-SPA.txt`, then `My-SPA-1.txt`, and so on. Existing files are never overwritten.
  - Spaces in the name become hyphens, and unsafe characters are replaced.
  - On Windows, reserved device names get a `_` prefix (e.g. `_NUL.txt`).
  - On Unix the file is created with mode 0600. On Windows it inherits the folder's permissions.
- **Reports can contain a plaintext client secret.**
  - A successful run never prints the secret to the terminal.
  - If writing the report fails, the whole report, including the secret, is printed to stdout so it isn't lost, and the command exits 1.
  - If Graph doesn't return the secret's `keyId`, the report records `(not returned by Graph)` and keeps the secret, and the command exits 1.
  - With `--keyvault-uri`, a successfully stored secret is left out of the report (see [Store the secret in Key Vault](#store-the-secret-in-key-vault)). Without it, move the secret to a secure store and delete the report. `.gitignore` excludes `*.txt`, but that can't protect files already committed.

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
- **Missing features.** One scope per run. No pre-authorization, consent grant, Application ID URI editor, adding redirect URIs or a secret to an existing app, or token-version option. See [SPA readiness and known gaps](#spa-readiness-and-known-gaps).
- **Service principal timing.** It is created immediately after the app, with no retry. If Graph hasn't yet replicated the new app, that request can fail; run the printed `az ad sp create` command, then `expose-api` if you wanted a scope.
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
| Key Vault 401 / 403 / 409 | 401: the vault is in another tenant. 403: grant **Key Vault Secrets Officer** or check the firewall. 409: recover or purge the soft-deleted secret, or pick another name. |
| App not found / wrong tenant | Check the active CLI account. Both object ID and client ID work with `--appid`. |
| `--name=value` rejected | Use a space: `--name value`. |
| Exit 2 | A required option is missing or an option doesn't belong to that command; run the command's `--help`. |
| Secret expiry rejected | Tenant lifetime policy; try a shorter lifetime. |

## Development

The Go module `github.com/AaronSaikovski/entra-appreg` is at the repository root:

| Package | Role |
|---|---|
| `cmd/entra-appreg` | Entry point: parse → delete terminal check → sign in → Graph client → command → exit code. |
| `internal/cli` | Strict parsing, Cobra flags, validation, help text. |
| `internal/auth` | Checks `az` is on `PATH`, then gets one token from Azure CLI within 10 seconds. |
| `internal/graph` | Graph client: no retries or redirects, raw error bodies, next-page link checks. |
| `internal/graphtest` | Local fake Graph server for tests. |
| `internal/app` | The four commands, plus scope and secret-lifetime rules. |
| `internal/report` | Safe, collision-free report files. |
| `internal/keyvault` | Stores the client secret with the Azure SDK `azsecrets` client: vault URI/name validation, no retries or redirects, permission guidance. |

Common tasks ([Task](https://taskfile.dev) is optional):

```sh
task check        # gofmt, vet (native + GOOS=windows), build, tests, binary smoke incl. the exact --version line
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

The version comes from **git tags**; no version number is stored in the source. Build metadata lives in `package main` (`cmd/entra-appreg/main.go`) and is stamped in at link time:

```sh
go build -ldflags "-X main.version=0.0.1 -X main.commit=abc1234 -X main.date=2026-10-12T01:02:03Z" ./cmd/entra-appreg
```

`--version` prints all three: `entra-appreg 0.0.1 (commit abc1234, built 2026-10-12T01:02:03Z)`. The general help heading shows only the version. `version` is injected without the leading `v`.

| How it was built | `--version` shows |
|---|---|
| GoReleaser release from tag `v0.0.1` | `0.0.1 (commit <short sha>, built <release time UTC>)` |
| `task build` / `task release` / `task run` / `task check` | `git describe --tags --always --dirty` without the `v` (e.g. `0.0.1`, or `0.0.1-3-gabc1234-dirty` between tags), the short commit, and the build time in UTC |
| plain `go build`, `go run` or `go install …@v0.0.1` (no `-ldflags`) | `dev (commit none, built unknown)` |

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
- `list --params-file <file>` with an unknown key, or with `"scope-name"`, exits 2 before sign-in.

**Live**, only in a deliberately chosen **test tenant**. Never log out or switch a shared operator session.

1. `list --top 1 --json`: stdout is valid JSON only, the tenant is correct, and no report is written.
2. Create a test API registration from a copy of `examples/api.params.json` (unique `name`, a test vault). In the portal check **Expose an API** shows `api://<client-id>/access_as_user` (admins only), **Manifest** shows `"requestedAccessTokenVersion": 2`, **Token configuration** lists `auth_time` for the Access token type, and the secret is in the vault, not the report.
3. Create a test SPA from a copy of `examples/spa.params.json`. Check the audience and redirects, that **Enterprise applications** lists it under the printed `AUTH_SP_ID`, and that **API permissions** shows Graph `User.Read`. Add `My APIs → <test api> → access_as_user`, grant admin consent, and sign in once with an MSAL.js sample. A decoded token for the API scope must have `ver` `2.0` and an `auth_time` claim.
4. Run create again with an existing `--appid`: nothing is created or changed, and no report is written. Run `expose-api` on the test API with a second scope name: existing settings and optional claims are kept, `auth_time` appears once, and a duplicate name that differs only in case fails without an update.
5. Create a test secret. Check the expiry, the report contents, and the file permissions. Repeat with `--keyvault-uri`/`--keyvault-secret-name` against a test vault: the secret version, expiry and tags appear in the vault, and the report lists its ID.
6. Remove disposable registrations with `delete`: cancel once, check the app still exists, then confirm with `yes`.

## License

[MIT](LICENSE). Release history is in [CHANGELOG.md](CHANGELOG.md).
