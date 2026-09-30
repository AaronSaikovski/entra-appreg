# Changelog

Notable changes to entra-appreg are recorded here. Unreleased changes have not been assigned a new version.

## Unreleased

### Fixed

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

- Use ordinal hash-set lookup for redirect deduplication instead of repeated linear scans.
- Retain only displayed fields while collecting Graph list results; avoid cloning response trees and creating an additional sorted list.
- Reuse the scope collection already cloned with API settings instead of cloning every existing scope twice.

### Added

- Offline CLI regression checks in `tests/test_cli.py`, using standard-library Python `unittest`, a controlled Azure CLI fixture, and a loopback-only proxy to prevent Graph traffic.
- Repository guidelines in `AGENTS.md` and `.gitignore` rules for secret-bearing reports, build output, IDE state, and test caches.
- Step-by-step README instructions for installation checks, tenant sign-in, read-only listing, creation, and local verification.
- Mandatory documentation closeout after every review or change: update README guidance/status and this changelog before reporting completion; the rule is recorded in `AGENTS.md`.

### Verification

- Local CLI regressions and isolated Graph-response replays passed on macOS with .NET SDK 10.0.401.
- Seven CLI test methods pass; one-off report-helper checks also cover 16 simultaneous writers, occupied paths, existing-secret preservation, Unix permissions, and genuine open-error propagation.
- Follow-up scope checks confirmed leading-dot rejection while preserving internal dots, underscores, hyphens, and the 120-character boundary; the real CLI rejected an empty `--appid` with exit 2 before authentication.
- Windows execution and live-tenant operations remain unverified; use the README smoke checklist in a test tenant.

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
