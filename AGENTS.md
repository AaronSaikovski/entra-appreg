# Repository Guidelines

## Project Overview

C# and Rust CLIs for Microsoft Entra ID SPA app registrations: `create` (default), `expose-api`, and read-only `list`. C# remains a single file; Rust is an isolated Cargo package. Optional API scopes and target client secrets are opt-in; this is not a hosted service.

## Architecture & Data Flow

`src/entra-appreg.cs` uses top-level async statements and local functions, not a layered project or Graph SDK:

1. Parse CLI arguments and command-specific options; normal help/version invocations exit without Azure authentication.
2. Acquire a Graph token through `AzureCliCredential`, using the current Azure CLI login. Create first reads tenant metadata with `GetTenantIdAsync` and pins authentication to it; access tokens remain opaque.
3. Share one bearer-authenticated `HttpClient` (10-second request timeout) against public-cloud Microsoft Graph v1.0.
4. Dispatch to inline create flow, `ExposeApiAsync`, or `ListAsync`.

Create checks optional `--appid`, validates creation inputs, POSTs the application, optionally PATCHes a scope and calls `addPassword`, then writes a report in the caller's working directory. An existing `--appid` exits without updating anything; an ID not found by either lookup falls through to creation. Explicitly blank IDs are rejected before authentication. Object ID (`id`) and client ID (`appId`) are distinct.

`ExposeApiAsync` reads, merges, and PATCHes API configuration. Preserve existing scopes, API settings, and identifier URIs; duplicate scope names are rejected case-insensitively. `ListAsync` follows Graph pagination, then sorts the collected results; preserve its next-link host restriction and JSON-only stdout contract.

Creation is not idempotent by name. There is no retry, rollback, or delete command; a later failure can leave an already-created registration. IDs printed before optional operations support recovery.

Rust uses Clap in `rust/src/cli.rs`, official `azure_identity::AzureCliCredential` in `auth.rs`, `reqwest` Graph HTTP in `graph.rs`, commands/helpers in `app.rs`, and atomic reports in `report.rs`. Both implementations use the current Azure CLI login: create discovers and pins the tenant, while list/expose use the active CLI context. Rust requires `az` at runtime; the SDK invokes it internally. Keep access tokens out of logs/reports and authentication errors. Preserve command/validation/output parity except documented edge-case differences.

## Key Directories

`src/` contains C#; `rust/` contains the Rust package and pinned `Cargo.lock`; `tests/` contains shared offline process checks. Root holds documentation and licensing. Reports belong to the invocation directory.

## Development Commands

Run from the repository root:

```sh
# Restore/build as needed and run authentication-free smoke checks
dotnet run src/entra-appreg.cs -- --version
dotnet run src/entra-appreg.cs -- --help
dotnet run src/entra-appreg.cs -- expose-api --help

# Pre-authentication validation check: expected exit 2
dotnet run src/entra-appreg.cs -- list --top 0

# Offline CLI regressions (.NET 10+ and Python 3.9+)
python3 -m unittest discover -s tests -v

# Authenticated, read-only integration check
dotnet run src/entra-appreg.cs -- list --top 1 --json
```

Keep `--` between the C# file and application arguments so the SDK does not consume help flags. `dotnet run` is the C# restore/build/run workflow; there is no project-based `dotnet test` suite.

Live operations require Azure CLI and tenant permissions for both implementations. Sign in deliberately with `az login --tenant <tenant-id>` and inspect `az account show`. `create` and `expose-api` mutate the tenant and are not routine smoke checks.

### Required Rust verification

After Rust changes, all of these commands MUST pass before declaring completion:

```sh
cargo fmt --manifest-path rust/Cargo.toml --check
cargo clippy --manifest-path rust/Cargo.toml --locked --all-targets -- -D warnings
cargo build --manifest-path rust/Cargo.toml --locked
cargo test --manifest-path rust/Cargo.toml --locked
ENTRA_APPREG_BINARY="$(pwd)/rust/target/debug/entra-appreg" python3 -m unittest discover -s tests -v
```

Fix formatting with `cargo fmt --manifest-path rust/Cargo.toml`, then rerun the check. Fix Clippy findings rather than weakening `-D warnings` or adding blanket allowances. Run the C# process suite too when shared tests or command contracts change. On Windows use the absolute `.exe` path via PowerShell's `$env:ENTRA_APPREG_BINARY`. Keep tests offline with fake Azure CLI executables, isolated process environments, and private SDK/loopback fixtures.

## Code Conventions & Common Patterns

The following naming/layout conventions describe C#; use standard rustfmt layout and Rust naming for `rust/`, preserving its existing module boundaries.

- Match four-space indentation, Allman braces, PascalCase helpers/types/constants, camelCase locals, `Arg` suffixes for raw CLI values, and `Async` suffixes for asynchronous helpers.
- Keep the lightweight top-level flow plus local functions and final `ScopeSpec` record. Options are local state; helpers capture shared state/client where needed. Pure helpers are static. There is no DI container, configuration-file loader, or application database.
- Use sequential `await`, `using var` for disposable HTTP resources, and existing `System.Text.Json.Nodes` payload patterns. Reuse `GetApplicationAsync`, `PostAsync`, `PatchAsync`, `ResolveScope`, and `BuildScope` rather than duplicating Graph/scope logic.
- User-input errors go to stderr with exit 2; runtime/Graph/file failures return 1; success/help/version return 0. Preserve Graph error bodies and distinguish lookup 404s from authorization or service failures. Not all semantic validation happens before authentication.
- When adding options, update parsing, validation, `ShowHelp`, source-header argument documentation, and README options. Include secret/scope flags in their shared supplied-option checks so other commands reject them.
- For releases, synchronize the source `Version` constant, header version, README version, root `CHANGELOG.md`, and affected smoke checks.
- After every review or change, update both `README.md` and `CHANGELOG.md` before declaring completion. Follow README's **After every review or change** checklist; include review outcomes even when no code fix is needed, actual verification evidence, and remaining limits.

## Important Files

- `src/entra-appreg.cs`: entry point, dependency directives, CLI/help, Graph operations, scope helpers, and `WriteResultFile`. Numbered sections identify the main workflow; shared helpers follow it.
- `README.md`: user-facing option reference and examples. Read **Smoke test checklist** for behavior verification and the development/release guidance when changing CLI behavior.
- `tests/test_cli.py`: standard-library real-process checks; both implementations use fake `az` to prevent real authentication.
- `CHANGELOG.md`: authoritative release history and unreleased changes.
- `LICENSE`: MIT license.

C# has no `.csproj`, solution, `global.json`, `.editorconfig`, or package lock. Rust dependencies are pinned in `rust/Cargo.lock`; retain the lockfile and ignore `rust/target/`.

## Runtime/Tooling Preferences

Use the **.NET 10 SDK or later**; a runtime alone is insufficient for this file-based app. NuGet dependencies are restored through the SDK via `#:package Azure.Identity@*`; the version floats. `#:property PublishAot=false` disables native AOT publishing. There is no Node/Bun package workflow.

Rust uses a current stable toolchain with rustfmt and Clippy installed. Azure CLI authentication uses the official Azure Identity SDK; Graph HTTP uses `reqwest` with Rustls. Fixed Graph endpoints, disabled Graph redirects/retries, and bounded authentication/HTTP operations are deliberate security/operational boundaries.

Help/version avoid Azure sign-in and Graph calls, but a cold run may require network access for package restore. Ancestor `Directory.Build.props`, `Directory.Packages.props`, or `nuget.config` can affect the generated project; keep this in mind when diagnosing environment-dependent builds.

## Testing & QA

Run `python3 -m unittest discover -s tests -v` for offline CLI regressions, then follow README's manual checklist in an explicitly intended test tenant for changed Graph behavior. No CI gate or coverage threshold is configured. Verify portal state, exit codes, JSON stdout, existing-ID handling, scope preservation/duplicate rejection, and secret lifetime/report output as applicable; clean up smoke registrations manually. Update the checklist when behavior changes. Local CLI smoke does not establish live-tenant correctness.

Reports can contain plaintext, one-time client secrets. `WriteResultFile` uses collision-safe names and owner-only Unix permissions; Windows inherits directory ACLs. On file-write failure the full report, including any secret, is printed to stdout. Keep reports and captured output out of source control and public logs; `.gitignore` excludes `*.txt` reports but does not protect already-tracked files. Avoid the checklist's `az logout` step during routine local checks because it changes shared login state.
