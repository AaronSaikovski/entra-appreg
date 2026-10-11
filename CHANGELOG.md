# Changelog

Notable changes to entra-appreg are recorded here. Unreleased changes have not been assigned a new version.

## Unreleased

### Added

- **Security review fixes:**
  - **Rust build output untracked.** `target/` (about 2,000 artifacts that embedded local paths) was removed from the index and added to `.gitignore`. The unused `cmd/entra-appreg/get_version.sh` was deleted. The files remain in the pushed `go-dev` history until that branch is squashed or rebased.
  - **No plaintext secret after a Key Vault store.** Once Key Vault confirms the store, the report writes `Secret value: (stored in Key Vault: <id>)` and drops the plain-text notice. The value is kept only if the store failed.
  - **Taskfile version injection.** `git describe` and commit output are now accepted only if they match `[A-Za-z0-9._+-]` / hex, because tag names can carry shell metacharacters into the templated commands.
  - **Release workflow pinned.** `release.yml` pins checkout, setup-go and goreleaser-action to commit SHAs, and goreleaser to `v2.18.3`.
  - Verification on macOS: `task check` and actionlint pass. The Key Vault success test now asserts that the secret value is absent from the report and that no plain-text notice is printed.
- **Store the new client secret in Azure Key Vault**: `create --create-secret --keyvault-uri https://<vault>.vault.azure.net --keyvault-secret-name <name>`.
  - Uses the Azure SDK for Go `azsecrets` client (new direct dependency `sdk/security/keyvault/azsecrets` v1.5.0), following Microsoft's Key Vault Go quickstart. The quickstart's `DefaultAzureCredential` is replaced by `AzureCLICredential`, and its deprecated `sdk/keyvault` import path by the current one.
  - The SDK pipeline has retries disabled and runs on a 10 s, no-redirect `http.Client`. Challenge authentication and the challenge-resource check stay on.
  - Stores the value with `exp`/`nbf` attributes and `entra-client-id`/`entra-key-id` tags. The version ID is printed and added to the report; the report still holds the plaintext secret.
  - The Key Vault token is a second `az` request (`auth.KeyVault`, via the shared `tokenWith`). It is requested only when the option is given, after the read-only checks and before the app POST, so a token failure changes nothing.
  - If storing fails, the report records `(not stored)` and keeps the secret, and the command exits 1. Errors add guidance for 401 (wrong tenant), 403 (**Key Vault Secrets Officer** / firewall) and 409 (soft-deleted name), and never contain the secret.
  - New `internal/keyvault` package. The options are `create`-only, accepted in `--params-file`, and described in help and in the README, including a **Key Vault permissions** section built around the Key Vault Secrets Officer role.
  - Tests:
    - The client runs against a TLS loopback vault that sends Key Vault's 401 challenge. Covered: body, attributes and tags, token scope and challenge tenant, no retries, no redirects, guidance, no secret in errors, and URI/name validation.
    - Workflow tests: success, store failure, token failure before the POST, and validation order.
    - Also: auth scope and message, and CLI option groups.
  - Verification on macOS: `task check`, staticcheck, `go test -race ./...` and govulncheck pass (111 tests). An offline binary smoke (empty `PATH`) shows `list --keyvault-uri` exits 2 and `create` with the options reaches sign-in. No live vault was used.
- **`create` now sets up what a SPA needs to sign users in**, as the Entra admin center does:
  - Creates the **service principal** (`POST /servicePrincipals {appId}`) right after the app and prints `AUTH_SP_ID`. Without it, admin consent was impossible, tenants that restrict user consent blocked sign-in, and tokens for the app's own `api://` scope failed (AADSTS500011). On failure, stderr prints `az ad sp create --id <client-id>` to finish by hand. There is no retry.
  - Adds delegated **Microsoft Graph `User.Read`** to `requiredResourceAccess` on the POST. With `--scope-name`, the scope PATCH resends that list with the app's own new scope added, so **Grant admin consent** covers both.
  - **Warns on stderr** when `--redirect-urls` is omitted, because such an app cannot sign anyone in.
  - The report adds `Service principal ID:` and `API permissions:` lines. `TestCreateOutputAndReportAreExact` was updated on purpose.
- **`--params-file <file.json>` on every command.** It reads options from one JSON object keyed by option name without `--`.
  - Command-line options win; command-line redirects replace the file's list.
  - Values go through the same validation and option-group rules. Unknown keys, wrong types, invalid JSON, trailing values and files over 1 MiB exit 2 before sign-in. `top`/`secret-expiry` also accept whole numbers, and `null` means absent.
  - New sample `examples/spa.params.json`, parsed by a test.
- **Verification on macOS:** `task check` (gofmt, vet, Windows vet, build, tests, binary smoke), `task staticcheck`, and `go test -race` for `cli`/`app`/`graph` all pass.
  - New tests: service principal failure and recovery hint, the no-redirect warning, `requiredResourceAccess` on POST and PATCH, and five `--params-file` tests.
  - Binary smoke with an empty `PATH`: the sample file parses and stops at the expected `Azure CLI was not found` (exit 1). An unknown key, a list-incompatible key, CLI `--top` with create, a wrong type and a missing file each exit 2 before sign-in.
  - Real `az`, live Graph and an MSAL.js sign-in were not run. Graph accepting the self-referencing `requiredResourceAccess` in the same PATCH that defines the scope, and service-principal replication timing, are unverified.
- **SPA readiness documented.** README has a new **SPA readiness and known gaps** table and AGENTS.md a **Known SPA Gaps** list. New `TestCreateServicePrincipalPostsAppIDAndRequiresID` covers the request body, a reply missing `id`, and a status error. Verification: `task check` and `go test -race ./...` pass (99 tests).
- **Build metadata moved to `package main`.** `cmd/entra-appreg/main.go` now holds `version`, `commit` and `date` (defaults `dev`/`none`/`unknown`), set with `-ldflags "-X main.version=… -X main.commit=… -X main.date=…"`.
  - `--version` prints `entra-appreg <version> (commit <commit>, built <date>)` via `fullVersion()`. The general help heading shows only the version; `cli.HelpText` now takes it as a parameter.
  - `internal/cli/version.go` and its test are removed, along with the code that stripped a leading `v` and fell back to Go build info. Pipelines inject the version without the `v`.
  - **Behavior change:** a binary built without `-ldflags`, including `go install …@v0.0.1`, now reports `dev (commit none, built unknown)` instead of the module version.
  - Wiring:
    - GoReleaser: `{{ .Version }}`, `{{ .ShortCommit }}`, `{{ .Date }}`.
    - Taskfile: `git describe` minus the `v`, `git rev-parse --short HEAD`, and a UTC timestamp from the `now` template.
    - `ci.yml` smoke: injects `0.0.0-ci`/`ci-commit`/`ci-date` and checks the exact line.
    - `release.yml`: builds with the tag, the short SHA and the build time, and checks the exact line.
  - `TestVersionOutputIsExact` sets all three variables and pins the `--version` line and the help heading.
  - Verification on macOS:
    - `task check` passes; its smoke compares the full stamped line: `entra-appreg 0.0.1-1-g7a03e39-dirty (commit 7a03e39, built 2026-10-11T21:47:18Z)`.
    - `go run ./cmd/entra-appreg --version` prints `entra-appreg dev (commit none, built unknown)`.
    - actionlint (all workflows) and `goreleaser check` pass. A single-target GoReleaser snapshot build prints `entra-appreg 0.0.1-SNAPSHOT-7a03e39 (commit 7a03e39, built 2026-10-11T21:50:46Z)`.
- **Docs and help text synced.** The help AUTHENTICATION sections had still said `create` "discovers and pins" the tenant; they now describe one `az` call in the active context, with the tenant read from Graph. README and AGENTS.md were updated for the single `az` call, the error-body cap, the create step order, versioning, and the test count.
- **Verification on macOS:**
  - `task check`, staticcheck and actionlint (all workflows) pass, and `goreleaser check` passes.
  - `task build` → `entra-appreg 1.0.1-5-gf5a00f1-dirty`; `-X …version=v0.0.1` → `entra-appreg 0.0.1`; a plain `go build` → a VCS pseudo-version; a GoReleaser snapshot → `entra-appreg 1.0.1-SNAPSHOT-f5a00f1`.
  - The old Rust-era `v1.0.1` tag is the nearest tag until `v0.0.1` is pushed.
- Performance and memory fixes:
  - **Auth starts Azure CLI once instead of twice.** The `az account show` pre-check is gone. `exec.LookPath` checks that `az` is installed without starting a process, and azidentity's `AzureCLICredential` gets the token for the active context. `create` now takes the report's tenant ID from Graph `/organization` (msgraph-sdk-go, same token) after validation and before the POST, so the recorded tenant always matches the token.
  - **Auth messages.** Signed-out and expired-token failures now share one fixed message, because azidentity reports them the same way. The invalid-tenant and could-not-run messages no longer apply.
  - **Graph error bodies are capped at 1 MiB** and marked when truncated, so memory stays bounded.
  - **Binary size is unchanged** (33.6 MB release). Unused msgraph request builders are already dropped by the linker. The remaining size is msgraph-sdk-go's typed models, which are needed to keep the typed SDK. Build flags are already `-trimpath -s -w`.
  - **Build cost:** CI already caches Go builds; no change.
  - **Verification on macOS:**
    - `task check`, `go test -race ./...` and staticcheck pass.
    - With a fake `az` that logs each call, `list` and `create` each started exactly one process (`account get-access-token -o json --resource https://graph.microsoft.com`). The fake's stderr never appeared in the output.
    - With no `az` on `PATH` the output is still `Azure CLI was not found` (exit 1).
    - New tests cover the org lookup failing before the POST and the body cap.
    - Real `az` and live Graph were not exercised.
- Refactored the Go code toward idiomatic Go, per package, with no change in behavior:
  - `cli`: a command table, smaller pre-parse helpers, and one ordered option-group table for validation. The Cobra tree holds only the requested subcommand.
  - `auth`: the deadline is handled in one place, and `runAz` now really discards az stderr. With `nil` a copy was kept inside `*exec.ExitError`, though it was never printed.
  - `graph`: smaller constructors, early returns, and an `ApplicationPages` loop with a dead branch removed.
  - `app`: `create` is split into `planCreate` → `checkNameAvailable` → `postApplication` → `addScope` → `addSecret` → `reportContents`/`writeReport`; expose/list/delete helpers were extracted.
  - `report`: the claim loop was split into helpers.
  - `cmd`: named exit codes, with one place mapping errors to exits.
  - Tests: helpers use `t.Helper`, repetitive tests are table-driven, and new exact-output and validation-order tests were written and passed before the refactor.
  - Verification on macOS:
    - `task check` and `go test -race` pass; staticcheck is clean.
    - Coverage moved from cmd 53.8% → 71.8%, app 88.8 → 93.0, auth 84.6 → 93.8, cli 96.0 → 98.8, graph 89.2 → 90.4, report 89.2 → 88.6.
    - Running 41 invocations against the binary built from `HEAD` and the refactored one gave byte-identical stdout, stderr and exit codes.
    - 20,001 random argument lists parsed identically by the old and new parser.
- Rewrote README for the Go CLI at version 0.0.1 (testing). It now covers install (release archives or source), auth, command reference, reports and secrets, limits, permissions and troubleshooting, development, CI/release and the smoke checklist. Per-change verification history moved out of the README; it stays in this changelog.
- GitHub Actions updated for the Go layout:
  - `release.yml` checks that the tag equals `v` + `Version`, runs the quality gates, and then runs GoReleaser with a new `.goreleaser.yaml`. It no longer deletes existing releases or passes `--force`.
  - `goreleaser.yml` was a duplicate trigger on the same tags and has been removed.
  - `build.yml` builds `./cmd/entra-appreg` and runs `goreleaser check`.
  - `test.yml` runs `go test -race ./...` (there is no `./tests` directory), staticcheck pinned to a Go-1.27-compatible commit with `staticcheck.conf`, and govulncheck.
  - All workflows take Go from `go.mod`.
  - Security: govulncheck found reachable vulnerabilities in `golang.org/x/net` v0.58.0 and in the Go 1.27.1 standard library (`net/http`). The fix moved `go.mod` to Go 1.27.2 and `x/net` to v0.60.0+.
  - Verification on macOS:
    - `task check` passed; staticcheck and actionlint (all workflows) are clean; govulncheck reports no called vulnerabilities.
    - `goreleaser check` and `goreleaser release --snapshot` produced all five archives plus the checksums file, each archive holding the binary, README and LICENSE.
    - The tag guard accepted `v0.0.1` and rejected `v1.0.1`.
    - Hosted runs were not exercised.
- The Go port at the repository root (module `github.com/AaronSaikovski/entra-appreg`) is now the CLI. Its version is reset to `0.0.1` while it is in testing (`internal/cli/version.go`); the 1.0.x entries below are for the former Rust implementation. It uses Cobra for the command line, the Azure SDK for Go `azidentity.AzureCLICredential` for auth, and `msgraph-sdk-go` for Graph. Messages, ordering, exit codes and report layout match the Rust implementation, which has been deleted. Changes from Rust:
  - List text output quotes display names, so control characters are escaped.
  - A missing `keyId` in the `addPassword` reply no longer loses the secret: it is written to the report and the command then exits 1.
  - Windows reserved device names get a `_` prefix in report filenames.
  - Next-page links must stay under `/v1.0/`.
  - Effects of using the typed SDK are listed under README operational limits.
- CI runs a `Go checks` job on Linux, macOS and Windows. Its smoke test checks the version, the help topics, and that `list --top 0` and non-terminal `delete` exit 2. `.gitignore`, README, AGENTS.md and `Taskfile.yml` (including a new `task check`) describe the root layout. The user-owned `release.yml`, `build.yml`, `test.yml` and `goreleaser.yml` were not changed or verified.
- Verification on macOS: `task check` passed (gofmt, vet native and `GOOS=windows`, build, tests, binary smoke). A real-binary smoke checked help precedence, rejection of `--name=x` and of a missing value, no arguments (exit 2), and missing `az` (exit 1). `actionlint` passed on `ci.yml`. Not verified: live Azure, Windows/Linux runs, and the Windows-only reserved-name test (compiled only).
- Two-axis review of the uncommitted Go port against `HEAD`, followed by an idiomatic refactor with no behavior change:
  - `resolveScope`/`resolveLifetime` return `error`.
  - `cli.Usagef`, `graph.MissingFieldError` and `graph.Scope` are the single shared helpers.
  - `displayPath` and `errRepeatedNextLink` replace duplicated code. List alone checks the organization count.
  - The scope type is now a typed `userConsent` bool, and `auth.Context` is renamed `auth.Session`.
  - Test-only graph exports moved to `export_test.go`, and doc comments were added.
  - The doc comment on `Version`, a no-op CI `working-directory`, and stale CHANGELOG/README text were corrected.
  - Verification: `task check` passed. staticcheck reports only ST1005 for the intentionally preserved user-facing messages (53 before, 48 after). A binary smoke confirmed messages and exit codes are unchanged.

### Review

- Reviewed the current Rust codebase for modularity and correctness. Existing module split is sound; recommend narrower implementation visibility and shared scope-option membership rather than a framework or wholesale rewrite. Reproduced an outstanding expose-api defect: non-array `identifierUris` is treated as missing and replaced in a PATCH. All 54 tests, formatting, strict Clippy, compilation, debug/release builds, and authentication-free CLI smoke passed on macOS. Review only; production code unchanged, no live Azure calls.

### Fixed

- Resolve all Rust review findings: reject malformed identifier URI collections before expose-api mutation, make scope helpers/create/Graph construction private, and share CLI scope-option membership across list/delete. Preserve missing/null URI behavior and deferred creation validation. Verification: all 55 tests and Rust quality/build gates passed on macOS; authentication-free CLI smoke passed, and an offline executable confirmed the malformed response now fails without PATCH. No live Azure calls made.

- Replace spaces with hyphens in report filenames (for example, `Test App Delete1` becomes `Test-App-Delete1.txt`), retaining edge trimming, collision suffixes, and unchanged report contents. Verification: offline report-writing executable, all 54 tests, formatting, strict Clippy, compilation, and debug/release builds passed on macOS.

- Block creation when a preflight Graph `displayName eq` lookup finds an existing registration; escape OData names, follow validated pagination, reject cycles/malformed responses, and stop on lookup errors. Existing `--appid` no-ops are preserved. This requires Graph read access and cannot guarantee uniqueness across concurrent creates or delayed directory visibility.
- Use `Application (client) ID` in list/delete console output, matching the report-file label; preserve `AUTH_CLIENT_ID` and JSON contracts. Verification: all 53 tests, formatting, strict Clippy, compilation, debug/release builds, offline duplicate-rejection executable, and actual-binary create/list help passed on macOS. No live tenant calls made.

- Make create's `--redirect-urls` / `--redirect-url` optional, defaulting to an empty SPA redirect URI list while retaining validation of supplied URIs. Update CLI help and README. Verification: all 52 tests, including an offline empty-redirect POST regression, formatting, strict Clippy, compilation, debug/release builds, and actual-binary create help passed on macOS. No live tenant creation performed.

- Name delegated `Application.Read.All` and Azure CLI admin consent in application-read 403 diagnostics, explain user directory read access and Directory Readers, and distinguish Entra access from Azure subscription roles. Keep read-permission advice off write failures. Verification: offline production-client GET/POST 403 smoke, all 51 tests, formatting, strict Clippy, compilation, debug/release builds, and authentication-free CLI smoke passed on macOS. No live tenant operations performed.

- Make Graph HTTP 403 errors lead with a plain-English access-denied explanation, Azure CLI account/tenant checks, and administrator guidance for Graph consent, directory roles, and ownership. Preserve the complete original response under technical details; no retries or lookup fallback are added. Verification on macOS: all 51 tests, rustfmt, strict Clippy, compilation, debug/release builds, authentication-free CLI smoke, and an offline production-client 403 replay passed. Live tenant and Windows/Linux behavior remain unverified.

- Rust: detect repeated Graph pagination links instead of looping on empty cyclic pages; retain the next-link origin restriction and reject redirects.
- Rust: cap secret descriptions at 128 UTF-16 units without splitting a Unicode scalar. The additive port leaves C# unchanged.
- Rust parity: require explicit HTTP(S) redirect authority instead of accepting WHATWG-repaired inputs such as `http:example.com`; verified C# rejection and retained a shared regression.
- Reject missing option values instead of consuming a following long option; reject empty redirect options on `list` and `expose-api` before authentication.
- Reject explicitly empty or whitespace-only `--appid` values before authentication, preventing an empty variable from silently opting into creation.
- Reject scope names beginning with `.` before Graph writes, matching Graph's scope-name restriction.
- Claim report filenames atomically and skip occupied files, directories, and symlinks, including concurrent-writer collisions, without overwriting existing secrets or treating genuine write failures as collisions.
- Preserve case-distinct redirect URI paths while removing only exact duplicates and keeping input order.
- Reject malformed successful Graph lookup responses instead of treating them as a missing application and potentially creating a duplicate.
- Reject malformed Graph list pages instead of reporting an empty or incomplete listing as successful.
- Report list truncation when the requested limit is reached within the final Graph page, even without a next-page link.
- Read tenant metadata from Azure CLI and pin create authentication to that tenant instead of assuming Graph access tokens are decodable JWTs.
- Correct README and CLI-help commands to use `src/entra-appreg.cs` from the repository root.

### Changed

- Refresh `.gitignore` for the root Rust project: retain target/dist, secret reports, IntelliJ state, and macOS metadata; ignore Rust formatter backups and profiling data; remove obsolete .NET, Visual Studio, and Python-test cache patterns. All 17 `git check-ignore --no-index` cases passed, including trackable Cargo.lock, source, shared Cargo configuration, and workflows.
- Derive CLI `--version` and the general help heading from Cargo.toml's package version via compile-time `CARGO_PKG_VERSION`, removing duplicated source version strings. The standalone executable does not read or require a manifest at runtime.
- Version verification: synchronized the stale lockfile package entry to the manifest's 1.0.0 using offline Cargo resolution. Formatting, strict Clippy, compilation, debug/release builds, and all 43 tests passed. A copied release executable printed the manifest version in both `--version` and help from a temporary directory with no Cargo.toml and an empty PATH.
- Move the Cargo package to the repository root (`Cargo.toml`, `Cargo.lock`, `src/`). Update CI/release manifest, build, binary-smoke, and packaging paths; simplify CLI help and development commands to root-level Cargo invocations; ignore `/target/` instead of `/rust/target/`. README and AGENTS.md now describe the flattened layout.
- Make Rust the sole supported implementation after removal of the .NET source and shared Python process suite. Rewrite README and root AGENTS.md for current Rust usage, architecture, conventions, tooling, tests, CI, and releases; synthesize four parallel research slices covering source, tests, build configuration, and documentation.
- Remove dangling Python unittest invocations from CI/release workflows. Retain formatting, strict Clippy, compilation/build gates, Rust tests, binary smoke, and release packaging/version checks. Historical C# and Python verification below describes removed implementations/coverage, not the current repository.
- Ignore root release packaging output in `/dist/` and anchor the Rust build-output rule to `/rust/target/`. Verified with `git check-ignore --no-index` that archives, checksums, build output, and reports are ignored while Cargo.lock, workflow files, and Rust source remain trackable.
- Switch Rust authentication to the official `azure_identity::AzureCliCredential`, matching C#'s use of the current `az login` session. Azure CLI is again a runtime dependency; this supersedes the earlier shell-free/client-secret design. No environment-credential fallback remains.
- Restore create-only tenant discovery and tenant-pinned token acquisition; list/expose use the active CLI context. Keep tokens opaque, bound authentication, and replace SDK errors with safe diagnostics rather than expose raw credential output.
- Retain `reqwest` for Graph HTTP with fixed Graph endpoint and disabled redirects/retries. Update help, README, repository instructions, and offline fixtures for Azure CLI authentication.
- Expand README's quick start with explicit Rust and C# commands, both version invocations, existing `az login` reuse, tenant inspection, and a warning that running both creation examples creates two registrations. No runtime behavior changed. Rechecked the documented locked Rust build and both implementations' help/version commands successfully; no live Azure operations were run.
- Require passing rustfmt, strict all-target Clippy, the locked build, Rust tests, and shared process checks in `AGENTS.md`; document the exact commands in README. Move CLI tests after production items and name the HTTP fixture response type. Keep the one-shot parsed invocation stack-allocated with a narrowly justified `large_enum_variant` expectation rather than adding a heap allocation.
- Replace Rust's manual option loop with Clap 4's derive parser (`clap` 4.6.7 pinned in `Cargo.lock`). Retain the C# command/help/version pre-pass, space-separated-only syntax, repeated scalar/flag/redirect behavior, single-hyphen values, detailed help, and deferred semantic validation. C# remains unchanged.
- Use ordinal hash-set lookup for redirect deduplication instead of repeated linear scans.
- Retain only displayed fields while collecting Graph list results; avoid cloning response trees and creating an additional sorted list.
- Reuse the scope collection already cloned with API settings instead of cloning every existing scope twice.

### Added

- Check Azure CLI account availability before token acquisition for every authenticated command. Provide actionable, secret-safe guidance for unavailable login, missing CLI executable, and token acquisition failure after a successful account check. Preserve create-only tenant pinning, the overall ten-second deadline, JSON stdout isolation, and login-free help/version. Verification: all 50 tests and Rust quality/build gates passed on macOS; actual-binary fake-CLI smoke covered signed-out, missing CLI, token failure, and help/version. No live Azure session was changed.

- List now displays the authenticated tenant's name and ID from Graph `/organization`, including empty lists. JSON mode sends tenant details to stderr and preserves the stdout array contract. Organization lookup failures stop listing.
  Verification: all 49 existing tests, rustfmt, strict all-target Clippy, cargo check, debug/release builds, and actual-binary list help passed on macOS. Offline production-workflow smoke covered text/JSON tenant output, empty results, organization 403, and malformed organization responses. No live Azure calls were made.

- Add `delete --appid <object-or-client-id>` with an escaped target preview and mandatory terminal confirmation: only exact `yes` plus Enter authorizes deletion. Enter, refusal, or EOF cancels; pipes, redirected prompts, missing/blank/sentinel IDs, and unrelated options are rejected. No force bypass, retry, or report. Reuse 404-only lookup fallback and delete the resolved object ID; preserve Graph failure bodies.

- Add read-only Rust CI on branch pushes, pull requests, and manual dispatch for Linux/macOS/Windows: rustfmt, strict Clippy, cargo check, locked build, unit tests, and real-process smoke/regressions. Actionlint and all commands passed locally on macOS, including 43 Rust tests and 21 CLI tests. Hosted execution and branch-protection configuration remain unverified; release publishing stays separate.

- Tag-triggered GitHub Actions release workflow for Rust binaries on Linux x64/ARM64, macOS Intel/Apple Silicon, and Windows x64. Native jobs gate publication on formatting, strict Clippy, unit/process tests, version/help smoke, and locked release builds. Publish archives with README/license and SHA-256 checksums using a least-privilege release job and commit-pinned actions. Require tags to match the package and binary version; mark prerelease versions accordingly.

- A complete Rust CLI alongside the retained C# implementation, under `rust/`, with a pinned Cargo lockfile and the same create/expose/list option, output, exit-code, and recovery contracts.
- Shared Python CLI checks selectable with `ENTRA_APPREG_BINARY`, plus Rust helper and isolated HTTP/subprocess tests.
- Offline CLI regression checks in `tests/test_cli.py`, using standard-library Python `unittest`, a controlled Azure CLI fixture, and a loopback-only proxy to prevent Graph traffic.
- Repository guidelines in `AGENTS.md` and `.gitignore` rules for secret-bearing reports, build output, IDE state, and test caches.
- Step-by-step README instructions for installation checks, tenant sign-in, read-only listing, creation, and local verification.
- Mandatory documentation closeout after every review or change: update README guidance/status and this changelog before reporting completion; the rule is recorded in `AGENTS.md`.

### Verification

- Confirmed deletion: rustfmt, strict all-target Clippy, locked cargo check, debug/release builds, and all 49 Rust tests passed on macOS. Real-binary smoke covered authentication-free help/validation and rejection of piped input/redirected stderr. A temporary production-workflow executable with a loopback Graph fixture passed five terminal scenarios: confirmed deletion after client-ID fallback, refusal, Enter, EOF, and HTTP 403 propagation. Tests additionally cover incomplete confirmation, malformed/missing lookup results, confirmation I/O failures, escaping, and no retry on delete errors. README/help/repository guidance updated; no live tenant operation occurred. Linux/Windows terminal behavior and live authorization remain unverified.

- Root Cargo layout verification: root-level formatting, strict Clippy, compilation checking, debug/ARM64 release builds, all 43 Rust tests, and actionlint passed. Executed both migrated workflow smoke scripts and the root-manifest tag guard. Tested migrated packaging paths, archive contents/checksums, Unix executable permissions, and extracted binary version; ZIP used a fixture, not Windows execution. Confirmed root target output is ignored and lock/source/workflows remain trackable. No active old package paths remain in source, workflows, README, AGENTS.md, or ignore rules. Hosted publication/platform and live-tenant limits are unchanged.
- Rust-only documentation/workflow verification: four parallel read-only research agents supplied source, tests, configuration, and documentation findings. Confirmed root AGENTS.md has the requested title/eight sections and current guidance/workflows contain no removed implementation/test commands. On macOS, actionlint for both workflows, rustfmt, strict Clippy, cargo check, debug and ARM64 release builds, and all 43 Rust tests passed. Executed both workflows' actual binary-smoke scripts; invalid top and blank app ID returned exit 2. Hosted matrix/publication and live Graph behavior remain unverified. Subsequent bullets retain historical verification, including the now-removed Python suite.

- Release workflow verification: actionlint 1.7.12 passed; macOS ARM64 locked release build, rustfmt, strict Clippy, 43 Rust tests, and 21 release-binary process checks passed. Executed the workflow's version guard, smoke, and archive steps locally; verified mismatched-tag rejection, archive contents/checksums, preserved Unix executable permissions, and extracted binary version. ZIP smoke used a fixture, not a Windows build. No tag was pushed or GitHub release created; hosted matrix and publication remain unverified.
- Current Azure CLI credential cutover: locked build, all 43 Rust unit/workflow tests, and all 21 process tests against each implementation passed with no skips. Formatting and strict all-target Clippy passed. Integration caught and corrected the SDK executor import to its public root re-export.
- Actual Rust binary smoke with fake `az` verified successful SDK token acquisition for Graph `.default`, normalized create tenant pinning, subsequent local validation, authentication-free version, and empty JSON stdout on login failure. No live Azure operation was performed.
- Rust requires Azure CLI 2.54.0+ for the SDK's numeric `expires_on` field. Authentication has a ten-second deadline and kills the directly spawned process on cancellation; termination of all shell descendants is not guaranteed. Windows/Linux and live-tenant verification remain outstanding.
- Historical, superseded client-secret implementation: 42 Rust unit/workflow tests and 21 Rust process tests passed; C# passed 17 applicable checks with four skips. Its one-off native SDK/reqwest loopback smoke also passed. Those results do not verify the current Azure CLI credential implementation.
- Final Rust formatting and lint gates passed: `cargo fmt --manifest-path rust/Cargo.toml --check` and `cargo clippy --manifest-path rust/Cargo.toml --locked --all-targets -- -D warnings`.
- Local CLI regressions and isolated Graph-response replays passed on macOS with .NET SDK 10.0.401.
- Before native authentication, the Clap migration passed 17 shared methods against both C# and Rust on macOS. The initial source contained seven baseline methods (not the eight recorded during planning); their non-authentication behavior remains covered.
- Follow-up scope checks confirmed leading-dot rejection while preserving internal dots, underscores, hyphens, and the 120-character boundary; the real CLI rejected an empty `--appid` with exit 2 before authentication.
- Before the temporary client-secret cutover, Rust 1.98.1 passed the locked build and 45 tests, including handwritten subprocess authentication checks. Current token acquisition is owned by the official Azure CLI credential SDK.
- Actual Rust process smoke passed version, general/create/expose/list help, invalid top/blank app ID, and JSON authentication failure with no stdout pollution.
- Clap migration smoke exercised the real binary's version, all four help topics, version-over-help precedence, missing/inline values, repeated JSON flags, and single-hyphen values under fake Azure CLI. Exit codes, authentication bypass, and clean JSON stdout were preserved; no live Graph operation was run.
- Historical initial-port evidence: a temporary `offline_smoke` Cargo example passed 30 loopback/fake-Azure scenarios covering Graph workflows and report recovery. The example was removed; these results predate the native authentication cutover.
- Independent read-only parity and security/reliability reviews of the initial Rust port found no remaining actionable defects. The later Clap migration was checked with the expanded unit/process suites and direct CLI smoke. Source-derived C# pagination-cycle and surrogate-truncation findings remain in C# by the approved coexistence decision; the Rust fixes are verified offline.
- Live-tenant operations and Windows/Linux execution remain unverified; macOS tests do not establish Windows ACL or live Graph behavior. No live authentication-state changes or tenant mutations were performed.

## 1.0.0

First release under the name **entra-appreg**. Earlier development builds were called `auth_init` and then `entra-appreg`, with versions 1.0.0 through 1.3.0; those development version numbers are retired.

### Added

- `create`: SPA app registrations with configurable audiences and redirect URIs, optional delegated API scopes and client secrets, and a local identifier/secret report.
- `expose-api`: add one scope to an existing registration while preserving existing scopes, API settings, and Application ID URIs.
- `list`: numbered or JSON output, name-prefix filtering, and result limits.
- General and command-specific help, plus `--version`.
- Authentication through the current Azure CLI login using `AzureCliCredential`.
- Existing-application lookup by either object ID or client ID.
- Actionable authentication errors and normal error handling for malformed Graph responses.
- Collision-safe report filenames and owner-only report permissions on Unix; inherited directory permissions on Windows.
