# Repository Guidelines

## Project Overview

Rust CLI for Microsoft Entra ID SPA app registrations: `create` (default), `expose-api`, read-only `list`, and interactively confirmed `delete`. API scopes and target client secrets are opt-in. The Cargo package lives at the repository root; this is not a hosted service.

## Architecture & Data Flow

`main.rs` runs a current-thread Tokio runtime: parse → require terminal stdin/stderr for delete → authenticate → construct one Graph client → dispatch command → map exit status. Requests are sequential; there is no Graph SDK, DI container, database, or configuration-file loader.

- `cli.rs`: Clap-derived options plus an explicit pre-parser. Unknown commands fail before version/help; version precedes help; help precedes validation/authentication. Options are case-sensitive and space-separated; scalars use the last value, redirect aliases accumulate. Preserve supplied-empty values and deferred creation semantics.
- `auth.rs`: official `AzureCliCredential` reuses `az login`. Create discovers and validates the tenant before pinning token acquisition; list/expose/delete use the active CLI context. Tokens remain opaque. The complete authentication operation has a ten-second deadline; cancellation kills the directly spawned process, not necessarily shell descendants.
- `graph.rs`: one bearer-authenticated `reqwest` client, fixed public-cloud Graph v1.0 endpoint, ten-second request timeout, no redirects/retries. Only 404 permits fallback from object-ID to client-ID lookup; malformed success, authorization errors, and service failures must stop the operation.
- `app.rs`: create checks optional `--appid` before creation-field validation. Existing registrations are no-ops; two lookup 404s permit creation. Sequence: POST app → flush recovery IDs → optional scope PATCH → optional `addPassword` → report. Expose merges fetched API settings/scopes and preserves identifier URIs; reject duplicate scope names case-insensitively. List validates next-link origin, rejects cycles, collects then sorts; JSON stdout stays JSON-only.
- Delete resolves the target before prompting, escapes remote fields, and requires exact `yes` plus a newline. Refusal/Enter/EOF cancels with exit 0; confirmation I/O failure prevents the DELETE. Delete only the resolved object ID, with no force bypass, retry, restore, or report cleanup.
- `report.rs`: sanitize filename, atomically claim a collision-safe file in the invocation directory, write and flush. Unix mode is 0600; Windows inherits directory ACLs. Genuine report failure prints the complete report, including any one-time secret, to stdout for recovery.

No name-based idempotency, rollback, or concurrent-update protection is provided. Preserve distinct object `id` and client `appId`, and the `AUTH_APP_ID`/`AUTH_CLIENT_ID` output contract.

## Key Directories

- `src/`: implementation and inline tests beside private helpers/workflows.
- `.github/workflows/`: branch/PR CI and tag-triggered release automation.
- `target/` and `dist/`: generated builds and release archives; ignored by Git.

Reports belong to the caller's working directory, not the source tree. The former .NET implementation and shared Python process suite are removed; do not use their paths or commands.

## Development Commands

Run from the repository root. After Rust changes, all quality gates must pass:

```sh
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo check --locked --all-targets
cargo build --locked
cargo test --locked

# Authentication-free actual-binary smoke
cargo run --locked -- --version
cargo run --locked -- --help
cargo run --locked -- expose-api --help
cargo run --locked -- delete --help
# Expected exit 2, before authentication
cargo run --locked -- list --top 0

# Optimized build
cargo build --locked --release
```

Fix formatting with `cargo fmt`; fix Clippy findings rather than weaken `-D warnings` or add blanket allowances. Keep `--` between Cargo options and application arguments. On Windows, built binaries end in `.exe`.

## Code Conventions & Common Patterns

- Use rustfmt, `snake_case` functions/locals, `PascalCase` types, and existing module boundaries. Keep local CLI state in `Options`; pass the Graph client and tenant explicitly. Reuse scope, lookup, and report helpers rather than duplicate them.
- Use sequential `await`, `serde_json` payloads, `AppError::Usage` versus runtime `anyhow` errors. Exit codes: 0 success/help/version, 1 operational failure, 2 invalid input/no arguments. Errors go to stderr; preserve Graph error bodies, but never expose raw token/credential output in authentication diagnostics.
- Preserve exact redirect URI spelling while deduplicating exact strings. Scope rules, UTC lifetime validation, Unicode-safe secret-description truncation, and validation ordering are observable behavior.
- Private executor/client construction seams and explicit clocks support deterministic tests; do not turn them into production endpoint overrides or a second dependency-injection framework. Use `parking_lot` for test locks where poisoning is not handled.
- New options require parsing, validation, help, README reference, and supplied-option group updates. Cargo.toml's package version is authoritative: version/help use compile-time `CARGO_PKG_VERSION`, not hardcoded strings. For releases, update Cargo.toml/Cargo.lock, README, changelog, and affected tests, then rebuild.
- After every review/change, update README and CHANGELOG with behavior, actual verification, review outcomes, and remaining limits. Keep unreleased work under the current version until an intentional release.

## Important Files

- `Cargo.toml` / `Cargo.lock`: root package manifest and pinned resolutions; keep the lockfile tracked and use `--locked`.
- `src/main.rs`: entry point and exit mapping; `cli.rs`: option and help contract.
- `auth.rs`, `graph.rs`, `app.rs`, `report.rs` under `src/`: authentication, transport, workflows, and secret-bearing report handling respectively.
- `.github/workflows/ci.yml`: Linux/macOS/Windows checks on branch pushes, PRs, and manual dispatch.
- `.github/workflows/release.yml`: five native release targets; tag must match package and executable versions. Publication needs all builds to pass; see README release instructions.
- `README.md`: usage, options, release instructions, and live smoke checklist. `CHANGELOG.md`: release history. `.gitignore`: report/build/archive exclusions, not protection for already-tracked secrets.

## Runtime/Tooling Preferences

Use current stable Rust (edition 2024), Cargo, rustfmt, and Clippy. No pinned compiler/MSRV is declared; CI installs stable. HTTP uses reqwest with Rustls. There is no Node/Bun or .NET workflow. Python 3.12 is used by GitHub automation for smoke/version/packaging helpers, not by the application or `cargo test`.

Live operations require Azure CLI 2.54.0+ on PATH and a deliberately selected `az login` identity with Graph permissions. No environment-client-secret fallback or automatic login exists. Help/version avoid authentication, though cold Cargo builds may download dependencies.

## Testing & QA

Rust tests use `#[test]`/`#[tokio::test]`, private SDK executors, loopback HTTP fixtures, and temporary files. Keep tests offline and isolated: do not mutate global PATH, credentials, or working directory; subprocess workers isolate cwd-sensitive cases. Cover behavior and failure boundaries: lookup fallback, scope preservation, pagination safety, lifetime dates, secret-safe errors, and report collisions/permissions/write failures.

CI checks formatting, lint, compilation, build, unit tests, and real-binary smoke; releases add packaging/version guards. Branch protection requires separate configuration; no coverage threshold is set. Local macOS results do not prove Windows/Linux behavior or hosted publication.

For live QA, use README's **Smoke test checklist** only in an explicitly intended test tenant. Start with read-only list; create/expose/delete mutate the tenant and are not routine smoke commands. Never log out or switch the operator's shared Azure session as part of offline verification. Keep reports and captured secret recovery output out of source control/public logs; clean up live smoke registrations deliberately.
