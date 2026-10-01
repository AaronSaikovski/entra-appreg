# Changelog

Notable changes to entra-appreg are recorded here. Unreleased changes have not been assigned a new version.

## Unreleased

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
