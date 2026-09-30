# entra-appreg

**Version 1.0.0** — a Rust CLI for Microsoft Entra ID single-page application registrations.

- **`create`** (default): create a SPA registration, optionally expose a scope and create a client secret, then save a report.
- **`expose-api`**: add a scope to an existing registration while preserving existing API configuration.
- **`list`**: read registrations, with optional filtering and JSON output.

Authentication reuses your **`az login`** session through the official [AzureCliCredential](https://docs.rs/azure_identity/latest/azure_identity/struct.AzureCliCredential.html). Graph HTTP uses `reqwest`. The repository now supports Rust only; the former .NET implementation and shared Python test suite have been removed.

## Requirements and installation

To build, install a current stable [Rust toolchain](https://rustup.rs/) with Cargo. The root package uses edition 2024; `Cargo.lock` pins dependencies. First builds may need network access.

```sh
# From the repository root
cargo build --locked --release
target/release/entra-appreg --version
target/release/entra-appreg --help
```

Use `.exe` on Windows. For development, omit `--release` and use `target/debug/entra-appreg`. Alternatively:

```sh
cargo run --locked -- list --help
```

Keep `--` between Cargo options and application arguments; omit it when invoking the binary directly. Examples below use `entra-appreg` on `PATH`; substitute the built binary's path if not installed there.

Prebuilt archives are published on the repository's **Releases** page by the [release workflow](.github/workflows/release.yml). Choose your platform, verify its `.sha256` checksum, extract it, and put the executable on `PATH`. Downloads contain the executable, README, and MIT license. Running the binary requires neither a Rust toolchain nor Python or .NET.

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

Create reads and validates the current tenant and pins token acquisition to it. List and expose-api use the active Azure CLI context. The selected identity needs Graph read/manage permissions; token acquisition alone does not grant authorization. The SDK requires the numeric `expires_on` field supplied by Azure CLI 2.54.0+.

After verifying read access, this command **creates a real registration** and writes `My SPA.txt` in your current directory:

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
```

Unknown commands fail before help/version handling. Otherwise `--version` takes precedence over help, and help takes precedence over option validation—even when these flags appear in a value position. No arguments prints help and exits 2.

### Create

| Option | Behavior |
|---|---|
| `--name <text>` | Required when creating a new registration; also used for the report filename. |
| `--redirect-urls <list>` / `--redirect-url <list>` | Required for new registrations. Comma-separated absolute HTTP(S) URIs; repeatable. Trims whitespace and removes exact duplicates, preserving path case and trailing slashes. |
| `--appid <id>` | Lookup by object ID or client ID. Existing app: exit without changes or report. Two lookup 404s: proceed to creation. Explicitly blank IDs are rejected; exact `no-id` sentinel skips lookup. |
| `--audience <value>` | Case-insensitive; default `AzureADMyOrg`. See account types below. |
| `--scope-*` | Optional API scope configuration; see Scope options. |
| `--create-secret` | Opt-in client secret on the target registration. Never embed this secret in browser code. |
| `--secret-expiry <value>` | `90`, `180`, `365`, `545`, `730` days, or `custom`; default 180. Requires `--create-secret`. |
| `--secret-start <DD/MM/YYYY>` | Custom start date, UTC; defaults to today and cannot be in the past. |
| `--secret-end <DD/MM/YYYY>` | Required for custom lifetime; strictly after start, valid through 23:59:59 UTC. |

Creation-field semantics are validated after authentication and optional existing-ID lookup, so an existing ID does not require name/redirect fields. Non-404 lookup failures never fall through to creation. Scope/secret detail options without their enabling option are rejected.

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

The tool collects the first matching records returned by Graph, then sorts by name. It does not ask Graph to sort before limiting. Truncation notices go to stderr in JSON mode. Pagination rejects foreign-origin links and repeated links. List never writes a report or changes tenant state; options for other commands are rejected.

## Output, failures, and security

Create prints `AUTH_APP_ID` (object ID) and `AUTH_CLIENT_ID` (client/application ID) immediately after Graph creates the registration, before optional mutations. Save these IDs for recovery. Reports contain IDs, tenant, redirects, and any requested scope/secret details. Filenames are sanitized and collision-safe: `My SPA.txt`, `My SPA-1.txt`, etc.; existing files are not overwritten.

| Exit | Meaning |
|---|---|
| 0 | Success, help, or version. |
| 1 | Authentication, Graph, or file failure. |
| 2 | Invalid arguments or no arguments. |

Errors go to stderr. Graph errors retain response bodies. Authentication diagnostics omit raw token output; access tokens stay in memory and are not included in reports.

**Reports can contain live plaintext client secrets.** Normal success does not print the secret. If report writing fails, the complete report—including the one-time secret—is printed to stdout for recovery and the command exits 1. Protect captured output; move secrets to a secure store such as Key Vault promptly. `.gitignore` excludes `*.txt` but cannot protect already-tracked files.

Unix reports are created with mode 0600; Windows inherits directory ACLs. Check directory access on Windows. Secret descriptions are capped at 128 UTF-16 units without splitting Unicode scalars. Reports use UTF-8 without BOM and platform line endings.

### Operational limits

- No retry, rollback, delete command, or name-based deduplication. A later failure can leave an app already created. Recover using the printed IDs; do not blindly rerun create.
- Expose-api preserves the fetched snapshot, not concurrent edits; no conditional update/ETag scheme is used.
- One scope per invocation; no pre-authorization, custom Application ID URI editor, or add-secret-to-existing-app command.
- Authentication is bounded to ten seconds; cancellation kills the directly spawned process, but not necessarily all shell descendants. Graph requests have separate ten-second timeouts and no redirects/retries.
- Human output includes remote text verbatim. Prefer JSON when processing untrusted names.

### Troubleshooting

| Symptom | Check |
|---|---|
| Authentication failure | Azure CLI 2.54.0+ on PATH, `az account show`, then deliberate `az login` to the intended tenant. |
| Graph 403 | Selected identity's Graph permissions/roles and target ownership. |
| Wrong tenant/app not found | Active CLI account; either object or client ID works with `--appid`. |
| Duplicate scope | Choose a new name; matching is case-insensitive. |
| Cargo shows its own help | Put `--` before the application's `--help`. |
| Exit 2 | Command-specific required/forbidden options; run that command's `--help`. |
| Secret expiry rejected | Tenant lifetime policy; try a shorter supported lifetime. |

## Architecture and development

`src/main.rs` orchestrates a current-thread Tokio runtime. `cli.rs` parses via Clap and handles early exits; `auth.rs` uses Azure Identity SDK; `graph.rs` owns direct Graph REST transport; `app.rs` implements workflows, scope/lifetime helpers and list output; `report.rs` handles atomic reports. Requests are sequential. There is no service, Graph SDK, database, or DI container. See [AGENTS.md](AGENTS.md) for code conventions and assistant guidance.

From the repository root:

```sh
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo check --locked --all-targets
cargo build --locked
cargo test --locked

cargo run --locked -- --version
cargo run --locked -- --help
# Expected exit 2 before authentication
cargo run --locked -- list --top 0
```

Fix formatting with `cargo fmt`. Preserve `Cargo.lock`; build output `/target/` and release artifacts `/dist/` are ignored. Tests live beside private helpers with `#[test]`/`#[tokio::test]`, injected SDK executors, loopback HTTP, and temporary files. They cover parser semantics, tenant selection, safe failures, date/scope/Unicode boundaries, lookup/pagination safety, API preservation, and report collisions/permissions. They do not use a live login. There is no separate Python test suite or coverage threshold.

### CI and releases

[CI](.github/workflows/ci.yml) runs on branch pushes, pull requests, and manual dispatch across Linux, macOS, and Windows. It checks formatting, strict Clippy, compilation, build, Rust tests, and real-binary smoke. Builds use read-only repository permissions and cancel superseded CI runs. Configure branch protection separately to require all three `Rust checks (...)` statuses.

[Release automation](.github/workflows/release.yml) runs on every pushed tag, with five native targets:

| Platform | Target | Archive |
|---|---|---|
| Linux x64 | `x86_64-unknown-linux-gnu` | `.tar.gz` |
| Linux ARM64 | `aarch64-unknown-linux-gnu` | `.tar.gz` |
| macOS Intel | `x86_64-apple-darwin` | `.tar.gz` |
| macOS Apple Silicon | `aarch64-apple-darwin` | `.tar.gz` |
| Windows x64 | `x86_64-pc-windows-msvc` | `.zip` |

Tags must equal the package version with an optional `v` prefix. The built CLI's version must match too. Each target runs quality gates, Rust tests, and smoke checks; publication waits for all targets to pass. Archives are named `entra-appreg-<tag>-<target>.<extension>` with separate `.sha256` checksums. Linux uses glibc (Ubuntu 22.04 for x64, 24.04 for ARM64), not static musl binaries. macOS/Windows binaries are unsigned; macOS is not notarized.

Python 3.12 is used only by workflow helper scripts for smoke, version validation, and packaging; it is not needed by end users or `cargo test`. Actions are commit-pinned. Only the publication job has `contents: write`, using `GITHUB_TOKEN`; no Azure credentials or personal token are required. Organization policy must allow the runners and release publication.

#### Making a release

1. Synchronize root `Cargo.toml`, generated `Cargo.lock`, version output in `src/main.rs`, help version in `src/cli.rs`, this README, changelog, and any affected tests.
2. Run the quality gates and smoke commands. Commit the changes before tagging.
3. Push a matching tag (replace the example version as needed):

   ```sh
   git tag -a v1.0.0 -m "Release v1.0.0"
   git push origin v1.0.0
   ```

Prerelease versions such as `v1.1.0-rc.1` produce GitHub prereleases. The publisher creates a new release with generated notes; it does not overwrite an existing one. Do not pre-create a release for the same tag. Failed builds can be rerun before publication.

### After every review or change

Update README behavior/usage/limits and [CHANGELOG.md](CHANGELOG.md) Unreleased before completion, including review outcomes and actual verification evidence. New options require parser, validation, supplied-option groups, help, and README updates. Keep unreleased work at the current version until deliberately releasing. Distinguish historical results from current checks, and offline checks from live integration.

## Smoke test checklist

Offline: run version and all four help topics without login, then `list --top 0` and blank `--appid` validation. Neither invalid invocation should authenticate or create a report.

For live acceptance, deliberately choose a **test tenant**; do not change or log out a shared operator session as a routine check:

1. Read-only `list --top 1 --json`: valid JSON-only stdout, correct tenant, no report.
2. Create a test SPA: inspect portal audience/redirect settings, distinct IDs, and report. Exact URI duplicates are removed; case-distinct paths remain.
3. Repeat using existing `--appid`: no creation, mutation, or new report.
4. Expose a scope: preserve existing API settings/scopes/URIs; a case-variant duplicate must fail without PATCH.
5. Opt in to a test secret: inspect expiry, report contents, and file permissions; keep captured output private.
6. Check invalid scope/lifetime options before mutations, and signed-out behavior only in an isolated CLI profile.
7. Remove test registrations deliberately after inspecting results. The CLI does not delete them.

Offline tests do not prove live Graph authorization, Windows ACL behavior, or GitHub publication. Current verification evidence is recorded in the changelog; historical Python-suite results predate the Rust-only cutover and are not current coverage.

Root-layout verification on macOS: all 43 Rust tests passed, along with rustfmt, strict Clippy, cargo check, debug/ARM64 release builds, and actionlint for both workflows. Both workflows' actual binary-smoke scripts and root-manifest tag guard passed locally. Archive checks covered tar extraction/executable permissions, checksums, and ZIP packaging with a fixture binary. Root `target/` is ignored; `Cargo.lock` and source remain trackable. Hosted Linux/Windows execution, GitHub publication, and live-tenant operations remain unverified.

## License and history

[MIT license](LICENSE). See [CHANGELOG.md](CHANGELOG.md) for release history and unreleased changes.
