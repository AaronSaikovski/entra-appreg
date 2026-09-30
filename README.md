# entra-appreg

Create, expose an API on, and list Microsoft Entra ID **app registrations** for single-page applications, from one .NET file (`src/entra-appreg.cs`).

**Version 1.0.0** · show it with `dotnet run src/entra-appreg.cs -- --version` · see [CHANGELOG.md](CHANGELOG.md) for release history and unreleased fixes

A single-file .NET script for Microsoft Entra ID (Azure AD) **app registrations** for a single-page application (SPA). It has three commands:

- **`create`** (the default) makes a new app registration, optionally exposes an API and adds a **client secret**, and saves the resulting IDs to a text file.
- **`expose-api`** adds an API scope to an app that **already exists**.
- **`list`** queries the tenant for app registrations, so you can find the ID to use with `--appid`.

It is a .NET [file-based app](https://learn.microsoft.com/en-us/dotnet/core/sdk/file-based-apps): there is no `.csproj`, you just run the `.cs` file. It replaces the original `auth_init.py` script, and was called `auth_init.cs` during development.

## Contents

- [What `create` does](#what-create-does) · [Requirements](#requirements) · [Quick start](#quick-start)
- [Commands](#commands): [`expose-api`](#expose-api), [`list`](#list)
- [Getting help](#getting-help) · [Options](#options) · [Examples](#examples)
- [Scope options](#scope-options) · [Supported account types](#supported-account-types)
- [Output](#output) · [Behaviour worth knowing](#behaviour-worth-knowing) · [Exit codes](#exit-codes)
- [Security](#security) · [Troubleshooting](#troubleshooting) · [Notes on the code](#notes-on-the-code)
- [Known limitations](#known-limitations) · [Local verification](#local-verification) · [Smoke test checklist](#smoke-test-checklist) · [Changelog](#changelog) · [Status](#status)

## What `create` does

1. Signs in with the standard Azure login: the account you signed in with using `az login` (no app IDs or secrets needed).
2. If you pass `--appid` (an object ID or a client ID) and that app exists, stops there (nothing is created).
3. Otherwise creates an app registration with:
   - **Platform:** Single-page application (SPA)
   - **Supported account types:** single tenant by default (configurable)
   - **Redirect URIs:** surrounding whitespace and exact duplicates removed; path case and trailing slashes preserved
4. If you pass `--scope-name`, **exposes an API**: sets the Application ID URI to `api://<client id>` and adds one delegated permission scope. The scope mirrors the portal's **Add a scope** form: admins-only consent and enabled by default, with options for everything else (see [Scope options](#scope-options)).
5. If you pass `--create-secret`, adds a client secret described as `<app name> secret`, with a configurable lifetime (default 180 days).
6. Writes the IDs (and the API scope and secret, if created) to `<app name>.txt` in the folder you ran the command from.

## Requirements

| Requirement | Notes |
|---|---|
| [.NET 10 SDK](https://dotnet.microsoft.com/download) or later | File-based apps are not available earlier. Check with `dotnet --version`. |
| [Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli) (`az`) | Required for Graph operations, not help/version. Sign in to the intended tenant; the script reuses that login through `AzureCliCredential`. |
| Tenant permissions | Create requires permission to register apps, such as the *Application Developer* role or the tenant's user-registration setting. `list` requires read access; `expose-api` requires permission to update the target registration. |

**For local tests only:** Python 3.9+ and the .NET 10 SDK. Python is not needed to run the application, and the regression suite does not require a real Azure login.

## Quick start

Run these commands from the **repository root**, the folder containing `README.md` and `src/`. Install the .NET 10 **SDK** (not just the runtime) and Azure CLI using the links above; both must be on `PATH`.

### 1. Check the installation without signing in

```sh
dotnet --version
dotnet run src/entra-appreg.cs -- --version
dotnet run src/entra-appreg.cs -- --help
```

The first run restores the NuGet dependency and compiles; it may need internet access. Subsequent runs use the SDK's build cache. There is no `.csproj` or separate setup/build command. Help and version do not call Azure CLI or Graph.

### 2. Sign in to the intended tenant

Replace `YOUR_TENANT_ID` with your directory's tenant ID:

```sh
az login --tenant "YOUR_TENANT_ID"
az account show --query "{tenantId:tenantId,user:user.name}" --output json
```

For a directory without an Azure subscription, add `--allow-no-subscriptions` to `az login`. Check the reported tenant and account before running any write command.

### 3. Check read access, then create an app

```sh
# Read-only; requires permission to read registrations.
dotnet run src/entra-appreg.cs -- list --top 5

# Creates a real registration and writes ./My SPA.txt.
dotnet run src/entra-appreg.cs -- create --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
```

The create command configures a single-tenant SPA without a scope or secret. Save its object/client IDs. Repeating it without an existing `--appid` creates another registration; use a test tenant when experimenting.

The `--` separates SDK arguments from application arguments. Quote names and comma-separated URI lists. The one-line commands work in Bash, Zsh, and PowerShell; later examples using `\` line continuation are Bash/Zsh syntax (join their lines for PowerShell).

On Linux/macOS, direct execution is optional:

```sh
chmod +x src/entra-appreg.cs
./src/entra-appreg.cs --help
```

Output files are written to the current working directory, not `src/`. Reports can contain plaintext secrets when `--create-secret` is used; see [Security](#security).

### 4. Work with an existing registration

Find the registration and copy its object ID or client ID:

```sh
dotnet run src/entra-appreg.cs -- list --name "My SPA" --json
```

Replace `YOUR_APP_ID` below with that ID:

```sh
# Verify an existing registration without changing it.
dotnet run src/entra-appreg.cs -- create --appid "YOUR_APP_ID"

# Mutates that registration: adds one delegated API scope.
dotnet run src/entra-appreg.cs -- expose-api --appid "YOUR_APP_ID" --scope-name access_as_user
```

The existing-app check is not an update or repair operation. If the ID is missing, creation requires `--name` and `--redirect-urls`; supplying those options permits a new registration to be created. `expose-api` instead fails if the target does not exist.

## Commands

The first argument can be a command. With none, the script runs `create`.

| Command | What it does |
|---|---|
| `create` (default) | Creates an app registration, as described above. Writing the word is optional: `create --name X` and `--name X` are the same. |
| `expose-api` | Adds one API scope to an **existing** app. |
| `list` | Lists (queries) the app registrations in the tenant. Read-only. |
| `help` | Shows help. `help` alone is the general help; `help create`, `help expose-api` and `help list` show one command's help. Same as `--help`. |

### `expose-api`

Use it when the app already exists, whether it came from this script (run without `--scope-name`) or was made by hand.

```bash
dotnet run src/entra-appreg.cs -- expose-api --appid <id> --scope-name access_as_user
```

- **`--appid`** is required. Either identifier works: the **object ID** or the **client (application) ID**. The script tries the object ID first, then the client ID.
- **`--scope-name`** is required. The other `--scope-*` options are optional and work exactly as they do for `create` (see [Scope options](#scope-options)); the default consent text is derived from the app's current name.
- **Existing scopes are kept.** Graph replaces the whole scope list on update, so the script sends the app's current scopes back along with the new one. A scope with the same name (ignoring case) is refused, not duplicated.
- **The existing Application ID URI is kept**, and the scope is added under it. Only an app with no URI gets the default `api://<client id>`.
- **Other API settings are preserved.** The update starts from a copy of the app's current `api` settings, so things like the token version and pre-authorised clients are not reset.
- **Nothing is created and no file is written.** The result is printed to the console, since no secret is involved.
- **Options for the other commands are rejected** with exit code 2: `--name`, `--redirect-urls`, `--audience`, `--create-secret`, the `--secret-*` options, `--top` and `--json`.

### `list`

Use it to see what app registrations exist, for example to find the ID to pass to `--appid`.

```bash
dotnet run src/entra-appreg.cs -- list
dotnet run src/entra-appreg.cs -- list --name "My SPA"
dotnet run src/entra-appreg.cs -- list --top all
dotnet run src/entra-appreg.cs -- list --json
```

- **`--name <text>`** shows only apps whose display name **starts with** the text. (It is a "starts with" match, not "contains".)
- **`--top <n|all>`** sets how many to show. The default is 50. `all` pages through every result, which can be slow in a large tenant.
- **`--json`** prints a JSON array and nothing else on stdout, so you can pipe it to a tool such as `jq`.
- **Sorted by name.** If there are more matches than `--top`, you see the first ones Graph returns, sorted by name, plus a note that more exist. Narrow with `--name` or use `--top all`.
- **Read-only.** Nothing is created or changed, and no file is written.
- **Needs read permission.** Your account must be allowed to read app registrations in the tenant. If not, Graph answers 403 and the script exits with code 1.
- **Options for the other commands are rejected** with exit code 2: `--appid`, `--redirect-urls`, `--audience`, any `--scope-*` option, `--create-secret` and the `--secret-*` options.

Each app is shown as a numbered entry: its name, then its client ID and object ID. Output looks like this:

```
1. My SPA
   Client ID: <client id>
   Object ID: <object id>
2. My SPA (prod)
   Client ID: <client id>
   Object ID: <object id>

2 app registration(s).
```

With `--json`, each entry has the fields `displayName`, `appId` (the client ID) and `id` (the object ID):

```json
[
  {
    "displayName": "My SPA",
    "appId": "<client id>",
    "id": "<object id>"
  }
]
```

## Getting help

`-h` or `--help` shows help and exits with code 0, even beside a missing option value. `--version` is handled similarly. An unknown command or help topic is rejected first.

| Command | Shows |
|---|---|
| `--help` | General help: every command, and which options belong to which |
| `create --help` | All `create` options, with defaults, allowed values and examples |
| `expose-api --help` | All `expose-api` options and how it behaves |
| `list --help` | The `list` options |
| `help`, `help create`, `help expose-api`, `help list` | The same, written as a command |
| `--version` | The version, for example `entra-appreg 1.0.0` |

```bash
dotnet run src/entra-appreg.cs -- --help
dotnet run src/entra-appreg.cs -- expose-api --help
dotnet run src/entra-appreg.cs -- help create
```

- **Keep the `--` after the script name.** Without it, `dotnet` reads `--help` itself and shows its own help instead of the script's.
- **No arguments at all** prints the general help and exits with code 2.
- **The help text can't drift from the code.** The allowed audiences, secret lifetimes and defaults shown in the help are read from the same lists the script validates against.

## Options

The options below apply to `create` unless noted. `--appid` and the `--scope-*` options also apply to `expose-api`; `--name`, `--top` and `--json` also apply to `list`.

| Option | Required | Description |
|---|---|---|
| `--name <text>` | When creating | App registration display name. Also names the output file and the secret description. For `list` it is instead a filter: only apps whose name starts with the text. |
| `--redirect-urls <list>` | When creating | One or more **full** redirect URIs, comma-separated. Surrounding whitespace and exact duplicates are removed; case-distinct paths and trailing slashes are preserved. Can be repeated. `--redirect-url` (singular) is accepted as an alias. |
| `--audience <value>` | No | Supported account types. Default `AzureADMyOrg`. See [Supported account types](#supported-account-types). |
| `--scope-name <value>` | No | **Expose an API.** Adds a delegated permission scope with this name (for example `access_as_user`) and sets the Application ID URI to `api://<client id>`. Letters, digits, `.`, `_` and `-` only, up to 120 characters; must not start with `.`. Without it, no API is exposed. The other `--scope-*` options need it. |
| `--scope-consent <admins\|users>` | No | Who can consent. `admins` = "Admins only" (default), `users` = "Admins and users". |
| `--scope-display-name <text>` | No | Admin consent display name. Default: `Access <app name>`. |
| `--scope-description <text>` | No | Admin consent description. Default: `Allow the application to access <app name> on behalf of the signed-in user.` |
| `--scope-user-display-name <text>` | No | User consent display name. Only with `--scope-consent users`. Default: `Access <app name>`. |
| `--scope-user-description <text>` | No | User consent description. Only with `--scope-consent users`. Default: `Allow the application to access <app name> on your behalf.` |
| `--scope-state <enabled\|disabled>` | No | Default `enabled`. A disabled scope exists but client apps can't request it. |
| `--create-secret` | No | Flag (no value). Also creates a client secret. **Without it, no secret is created.** |
| `--secret-expiry <value>` | No | `90`, `180`, `365`, `545`, `730` (days from now) or `custom`. Default `180`. Requires `--create-secret`. |
| `--secret-start <DD/MM/YYYY>` | No | Custom lifetime only. Defaults to now. Cannot be in the past. |
| `--secret-end <DD/MM/YYYY>` | With `custom` | Custom lifetime only. Inclusive: the secret works until 23:59:59 UTC on that day. Must be after the start. |
| `--appid <id>` | No | An existing registration, by object ID or client ID. If it exists, `create` creates nothing. Explicitly empty or whitespace-only IDs are rejected before authentication; omit the option for a new registration. For `expose-api` it is required. |
| `--top <n\|all>` | No | `list` only. Maximum number of app registrations to show. Default 50. |
| `--json` | No | `list` only. Flag (no value). Print a JSON array instead of a list. |
| `-h`, `--help` | No | Show help and exit. Works with or without a command. See [Getting help](#getting-help). |
| `--version` | No | Show the version and exit. |

Options that take a value reject a following `--option` as a missing value; values starting with `--` are not supported. Replace angle-bracket placeholders such as `<id>` before running examples.

"When creating" means the option is needed only if a new app will be created. If `--appid` points at an app that already exists, the script exits before checking the other options.

## Examples

**Local development, no secret:**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" \
  --redirect-urls http://localhost:5173/auth/callback
```

**Local and deployed redirect URIs:**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" \
  --redirect-urls "http://localhost:5173/auth/callback,https://myapp.example.com/auth/callback"
```

**Allow sign-in from any Entra directory:**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" --audience AzureADMultipleOrgs \
  --redirect-urls http://localhost:5173/auth/callback
```

**Expose an API with an admins-only scope:**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" --scope-name access_as_user \
  --redirect-urls http://localhost:5173/auth/callback
```

This sets the Application ID URI to `api://<client id>` and adds the scope `api://<client id>/access_as_user`.

**Same, with your own consent text:**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" --scope-name access_as_user \
  --scope-display-name "Access My SPA API" \
  --scope-description "Allows the app to call the My SPA API on behalf of the signed-in user." \
  --redirect-urls http://localhost:5173/auth/callback
```

**Let users as well as admins consent, with your own user text, and create the scope disabled:**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" --scope-name access_as_user \
  --scope-consent users \
  --scope-user-display-name "Access My SPA" \
  --scope-user-description "Allows the app to access My SPA on your behalf." \
  --scope-state disabled \
  --redirect-urls http://localhost:5173/auth/callback
```

**Add a scope to an app that already exists** (object ID or client ID):

```bash
dotnet run src/entra-appreg.cs -- expose-api --appid 00000000-0000-0000-0000-000000000000 \
  --scope-name access_as_user
```

**Find an existing app registration** (then use its ID with `--appid`):

```bash
dotnet run src/entra-appreg.cs -- list --name "My SPA"
```

**With a client secret (180 days by default):**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" --create-secret \
  --redirect-urls http://localhost:5173/auth/callback
```

**With a 365-day secret:**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" --create-secret --secret-expiry 365 \
  --redirect-urls http://localhost:5173/auth/callback
```

**With a custom secret lifetime:**

```bash
dotnet run src/entra-appreg.cs -- --name "My SPA" --create-secret \
  --secret-expiry custom --secret-start 01/10/2026 --secret-end 30/06/2027 \
  --redirect-urls http://localhost:5173/auth/callback
```

**Only create the app if a previous one is missing:**

```bash
dotnet run src/entra-appreg.cs -- --appid 00000000-0000-0000-0000-000000000000 \
  --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
```

## Scope options

These options describe the scope added by `--scope-name` (with `create`) or by `expose-api`. They match the fields of the portal's **Add a scope** panel, and they are only allowed together with `--scope-name`.

| Portal field | Option | Default |
|---|---|---|
| Scope name | `--scope-name` | *(none: no API is exposed)* |
| Who can consent? | `--scope-consent admins\|users` | `admins` ("Admins only") |
| Admin consent display name | `--scope-display-name` | `Access <app name>` |
| Admin consent description | `--scope-description` | `Allow the application to access <app name> on behalf of the signed-in user.` |
| User consent display name | `--scope-user-display-name` | `Access <app name>` (only with `--scope-consent users`) |
| User consent description | `--scope-user-description` | `Allow the application to access <app name> on your behalf.` (only with `--scope-consent users`) |
| State | `--scope-state enabled\|disabled` | `enabled` |

- The user consent options are refused with `--scope-consent admins`, because they would be ignored.
- With `--scope-consent users`, the user consent text is sent to Graph along with the admin text.
- Blank display names or descriptions are refused.

## Supported account types

`--audience` sets Graph's `signInAudience`. It is case-insensitive; the script sends Graph the exact spelling.

| Value | Who can sign in |
|---|---|
| `AzureADMyOrg` (default) | Accounts in your own directory only (single tenant) |
| `AzureADMultipleOrgs` | Accounts in any Entra directory |
| `AzureADandPersonalMicrosoftAccount` | Any Entra directory plus personal Microsoft accounts |
| `PersonalMicrosoftAccount` | Personal Microsoft accounts only |

For the two options that include personal accounts, the script also sets the app's access token version to 2, because those audiences need v2.0 tokens. This is the least tested part of the script, so check the result the first time you use either value.

## Output

### Console

**`create`** prints progress messages, plus these lines once the app exists:

```
AUTH_APP_ID=<object id>
AUTH_CLIENT_ID=<application (client) id>
Wrote details to /path/to/My SPA.txt
```

The secret value is **not** printed unless the file could not be written (see [Exit codes](#exit-codes)).

**`expose-api`** writes no file. It prints the result:

```
AUTH_APP_ID=<object id>
AUTH_CLIENT_ID=<application (client) id>
Application ID URI: api://<client id>
Scope (full name):  api://<client id>/access_as_user
Scope ID:           <scope guid>
Scope consent:      Admins only
Scope state:        Enabled
```

### Output file (`create` only)

Written to the folder you ran the command from (not the script's folder), named after the app registration, for example `My SPA.txt`:

```
Application name:        My SPA
Application (client) ID: <client id>
Object ID:               <object id>
Directory (tenant) ID:   <tenant id>
Supported account types: AzureADMyOrg
Application ID URI:      api://<client id>
Scope (full name):       api://<client id>/access_as_user
Scope ID:                <scope guid>
Scope display name:      Access My SPA
Scope description:       Allow the application to access My SPA on behalf of the signed-in user.
Scope consent:           Admins only
Scope state:             Enabled
Secret description:      My SPA secret
Secret ID:               <key id>
Secret value:            <secret>
Secret starts:           <timestamp>
Secret expires:          <timestamp>
Redirect URIs (SPA):     http://localhost:5173/auth/callback
                         https://myapp.example.com/auth/callback
```

The seven lines from `Application ID URI` to `Scope state` appear only with `--scope-name`. With `--scope-consent users` two more lines follow, `Scope user display name` and `Scope user description`. The `Secret ...` lines appear only with `--create-secret`.

- Characters not allowed in file names are replaced with `_`.
- Existing paths are **never overwritten**. The writer atomically claims a filename, trying `-1`, `-2`, and so on when a file, directory, symlink, or concurrent writer occupies it. Genuine write failures still use the recovery behavior described under [Security](#security).
- On Linux/macOS the file is readable and writable by you only. On Windows it gets the folder's normal permissions.

## Behaviour worth knowing

- **`create` is not idempotent without `--appid`.** The script does not look for apps by name. Every run without `--appid` creates a new registration (and a new secret with `--create-secret`), so running it twice gives you two apps. Save the `AUTH_APP_ID` and pass it back with `--appid` to make repeat runs safe.
- **`--appid` accepts either ID.** The script looks the app up by object ID (Graph's `id`) and, if that isn't found, by client ID (`appId`). This matters for `create`: the two IDs are easy to mix up, and an ID that was only tried one way would look "not found" and create a duplicate app.
- **Only a 404 on both lookups means "doesn't exist".** Any other response to the existence check (401, 403, 429, 5xx) stops the script rather than risking a duplicate app.
- **`list` shows the first matches, not necessarily all.** With more results than `--top`, you get the first ones Graph returns, sorted by name. Use `--name` or `--top all` to see others.
- **Everything is validated before anything is created.** A bad redirect URI, audience or date exits with code 2 and creates nothing.
- **The IDs are printed before the API scope and secret steps**, so if either fails you still have a record of the app.
- **Exposing an API is a second request.** The default Application ID URI, `api://<client id>`, contains the client ID, which Graph only assigns when the app is created. So the script creates the app first, then updates it with the URI and the scope. If that update fails, the app already exists (its IDs are printed) but has no scope.
- **The scope is admins-only and enabled unless you say otherwise.** By default its type is `Admin`, so users can't consent to it themselves; `--scope-consent users` makes it `User` ("Admins and users"). Either way it gets a newly generated ID.
- **Scope names are checked conservatively.** Letters, digits, `.`, `_` and `-` only, up to 120 characters, so typical names like `access_as_user` and `Files.Read` pass. Graph applies its own rules too.
- **Tenant lifetime limits.** Your tenant's app management policy may cap secret lifetimes. If it does, Graph rejects the request and the script shows the error. The script does not enforce a maximum itself.
- **A SPA is a public client** and does not use a client secret. That is why secrets are opt-in. Only add one if something else, such as a backend that shares the registration, needs it.
- **Dates are UTC and day-first.** `03/04/2027` is always 3 April, regardless of your machine's regional settings.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success, including help/version, listing, creating an app, adding a scope, or finding the existing `--appid` |
| `1` | Runtime failure: sign-in failed (the Azure CLI is missing or you are not signed in), Graph error, timeout, unexpected response, or the output file could not be written (in which case the values are printed to the console so a secret isn't lost). For `expose-api` also: the app was not found, or it already has a scope with that name. For `list` also: no permission to read app registrations |
| `2` | Bad or missing command-line arguments, including no arguments at all (which prints the general help) |

## Security

- With `--create-secret`, the output file contains a **live secret in plain text**, and Graph only returns the secret value once.
- Do not commit the file. The repository's `.gitignore` excludes `*.txt` reports; ignore rules do not protect files already tracked by Git.
- Move the secret to a proper store, such as Key Vault, as soon as you can.
- The secret is not printed to the console unless the output file can't be written; then it is printed once so it isn't lost. Don't run the script where stdout is logged publicly.
- On Linux/macOS the output file is created readable and writable by you only. On Windows it gets the folder's normal permissions, so check who can read that folder.
- The script signs in through your Azure CLI login (`az login`) and holds the access token in memory only. It never writes the token anywhere, and `list` and `expose-api` handle no secrets.
- Redirect URIs, names and IDs are sent to Graph as given after the checks described above. Values in the OData name filter are escaped, and the next-page links Graph returns are only followed if they point at `graph.microsoft.com`.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `dotnet run src/entra-appreg.cs` fails to run the file | Check `dotnet --version` (SDK 10+) and run from the repository root. |
| `dotnet run src/entra-appreg.cs` runs a different project | There is a `.csproj` in the current folder. Use `dotnet run --file src/entra-appreg.cs -- --help` to select the file explicitly. |
| "Could not get an access token to call Microsoft Graph" (exit code 1) | The Azure CLI isn't installed, or you aren't signed in. Install it, then run `az login`. The `Details:` line shows the CLI's own explanation. |
| Signed in to the wrong account or tenant | The script acts as whatever `az account show` reports. Sign in again with `az login --tenant <tenant id>` (or `az login` with the right account). |
| `403` from Graph (creating an app, `expose-api`, or `list`) | Your account, or the permissions the sign-in carries, doesn't allow it in this tenant. Ask a tenant admin, or try an account with the *Application Developer* role. |
| `Application not found` (`expose-api`) | The ID matches neither an object ID nor a client ID in the tenant you are signed in to. Check with `list`, and check you are signed in to the right tenant (for the Azure CLI: `az login --tenant <tenant id>`). |
| `already has a scope named ...` (`expose-api`) | The app has that scope name already. Nothing was changed. Choose another name. |
| `Unknown or incomplete argument` (exit code 2) | A typo, or an option with no value after it. Run with `--help`. |
| `dotnet` shows its own help for `--help` | You left out the `--` after the script name. |
| "Welcome to .NET" banner and an HTTPS development certificate message on the first run | Normal first-run output after installing a new .NET SDK. It has nothing to do with the script and doesn't appear again. |
| A change to the script seems to be ignored | Stale build cache. Run `dotnet clean src/entra-appreg.cs`. |
| Secret creation rejected | Tenant policy may cap the lifetime. Try a shorter `--secret-expiry`. |
| Unexpected behaviour from build settings | The script is inside a `.csproj` folder, or a parent folder has `Directory.Build.props`, `Directory.Packages.props` or `nuget.config`. Keep the script in its own folder. |

## Notes on the code

- The `#:package Azure.Identity@*` line at the top uses the latest version. Pin a specific version for reproducible builds.
- `#:property PublishAot=false` turns off the native AOT publishing that file-based apps enable by default, because Azure.Identity may not be AOT-friendly. The docs describe it as a publishing setting; `dotnet run` runs the script as ordinary managed code either way.
- Create reads the tenant ID from `az account show --query tenantId --output tsv` and pins `AzureCliCredential` to that tenant. Graph access tokens are treated as opaque; no JWT decoding or extra Graph permission is needed.
- Valid help/version requests exit before authentication. The SDK may still need network access for a cold NuGet restore.
- The command is read from the first argument, and only if it doesn't start with `-`, so every command line that begins with an option still runs `create`.
- All three commands share one sign-in and one HTTP client. `create` and `expose-api` also share two scope helpers (`ResolveScope` for checking every scope option and filling in defaults, `BuildScope` for the scope object), so both apply the same rules.
- Sign-in failures (the Azure CLI missing, or not signed in) print a short instruction and exit with code 1 instead of a stack trace.
- The owner-only permission on the output file uses a Unix-only API, so it is set only when the script isn't running on Windows (`OperatingSystem.IsWindows()`); this keeps the build free of the CA1416 platform warning.
- To turn the script into a regular project later: `dotnet project convert src/entra-appreg.cs`.

### How the code is organised

The file reads top to bottom. The header comment is a full reference; the code has numbered steps:

| Part | What it does |
|---|---|
| Header comment | Version, purpose, commands, every option, usage examples, output, exit codes |
| `1` Parse | Reads the command and options; handles `--version`, `--help` and `help` first; validates `expose-api` and `list` options |
| `2` Authenticate | For create, reads and validates the CLI tenant ID; gets a Graph token through `AzureCliCredential`; creates one HTTP client (10 second timeout) |
| `3`–`8` (`create`) | Check `--appid` → validate everything → create the app → expose an API → add a secret → write the file |
| Helpers (bottom) | Graph calls (`PostAsync`, `PatchAsync`, `GetApplicationAsync`), the commands (`ListAsync`, `ExposeApiAsync`), scope logic (`ResolveScope`, `BuildScope`), option checks (`Supplied`, `SecretOptionsSupplied`, `ScopeDetailOptionsSupplied`), `ShowHelp` and utilities (`Require`, `TryParseDate`, `GetTenantIdAsync`, `WriteResultFile`) |
| `ScopeSpec` (last) | The record holding one resolved scope; it is at the end because type declarations must follow all top-level statements |

### After every review or change

Before marking work complete, update both this README and [CHANGELOG.md](CHANGELOG.md):

- Keep affected usage instructions, behavior, limitations, and verification status current in the README.
- Record fixes, changes, review outcomes, and verification evidence under **Unreleased** in the changelog. If a review finds no actionable issues, say so and record its scope.
- Distinguish checked-in tests from one-off checks, and local verification from live-tenant verification. Record untested platforms and remaining risks explicitly.
- Keep unreleased work under the existing version until a release is intentionally made.

### Making a new release

1. Change the `Version` constant in `src/entra-appreg.cs`.
2. Change the `VERSION` section of the header comment.
3. Change the version line at the top of this README.
4. Add an entry to [CHANGELOG.md](CHANGELOG.md) and update the [smoke test checklist](#smoke-test-checklist) if behaviour changed.
5. If you add an option, update all of: the parsing code, the validation code, `ShowHelp`, the header comment's `ARGUMENTS`, and the [Options](#options) table. If it is a secret or scope option, also add it to `SecretOptionsSupplied` or `ScopeDetailOptionsSupplied`, which is what makes the other commands reject it.

## Known limitations

These are deliberate omissions in 1.0, not bugs:

- **No retries.** A throttled request (429), a timeout or a transient 5xx fails the run. Graph can also be briefly inconsistent right after an app is created, so the follow-up request (adding the scope or the secret) could in principle fail with a 404. The IDs are printed before those steps, so nothing is lost: re-run `expose-api` for the scope, or add the secret in the portal.
- **`create` is not idempotent by name.** It never looks for an existing app by display name; only `--appid` prevents duplicates.
- **One scope per run.** To add several scopes, run `expose-api` once per scope.
- **`list` matches names that start with the text**, not names that contain it, and sorts only the results it collected.
- **Not covered:** authorized client applications (pre-authorisation), custom Application ID URIs, editing or deleting scopes, adding secrets to an existing app, and deleting apps.
- **`--help` and `--version` are recognised anywhere in the arguments**, including as the value of another option.
- **Secrets are written to a plain-text file** (see [Security](#security)).

## Local verification

With .NET 10+ and Python 3.9+ installed, run the authentication-free CLI regressions from the repository root:

```sh
python3 -m unittest discover -s tests -v
```

The suite in `tests/test_cli.py` uses Python's standard-library `unittest` and `tests/fake_az.py` behind a temporary `az` executable. Its seven test methods exercise missing values, forbidden options, blank app IDs, invalid leading-dot scopes, help precedence, exit codes, and single-hyphen names. A loopback-only proxy blocks Graph traffic even when the fixture supplies a test token; your Azure login is never used. No Python package installation is needed.

There is no configured lint task, test project for `dotnet test`, or coverage threshold. Use the checklist below for live-tenant behavior; local tests do not establish Graph integration correctness.

## Smoke test checklist

The CLI regressions and isolated Graph-response replays passed during review on macOS with .NET SDK 10.0.401. The replays used temporary fake CLI/HTTP responses to verify lookup safety, malformed responses, pagination truncation, scope preservation, case-sensitive redirects, and opaque access tokens. Those one-off replays are **not** part of the checked-in test suite and do not verify a live tenant. Run the following checklist against a **test tenant** before relying on live operations. Abbreviated commands below are arguments to `dotnet run src/entra-appreg.cs --`.

A separate one-off report-helper check verified directory/file/dangling-symlink collisions, 16 simultaneous writers, preservation of existing secrets, owner-only Unix permissions, and propagation of genuine open failures. It is not part of the checked-in CLI suite.

1. `dotnet run src/entra-appreg.cs -- --version` prints `entra-appreg 1.0.0`.
2. `dotnet run src/entra-appreg.cs -- --help`, `create --help`, `expose-api --help` and `list --help` print help without needing to sign in.
3. `dotnet run src/entra-appreg.cs -- list` shows your apps (checks sign-in and the Graph connection).
4. `create --name "Smoke test" --redirect-urls http://localhost:5173/auth/callback` creates an app; check it in the portal (SPA platform, single tenant, redirect URI) and check the output file.
5. Run the same `create` command again with `--appid <client id>`: it should say the app already exists and create nothing.
6. `expose-api --appid <id> --scope-name access_as_user`: check **Expose an API** in the portal (Application ID URI, one scope, admins only, enabled).
7. Run step 6 again: it should refuse the duplicate scope name and change nothing.
8. `expose-api` with `--scope-consent users --scope-name other`: check the user consent text and that the first scope is still there.
9. `create` with `--create-secret --secret-expiry 90`: check the secret's description and expiry in the portal, and that the output file holds the secret.
10. `create` with a bad option (for example `--audience nope`): exits with code 2 and creates nothing.
    Also check `--scope-name .read`: it must exit 2 before any app is created.
11. Sign out with `az logout` and run `list`: it should print the "Could not get an access token" message and exit with code 1 (no stack trace). Sign back in afterwards.
12. Run with no arguments: it prints the general help and exits with code 2.
13. `list --json` prints a JSON array and nothing else on stdout.
14. Create with both `https://example.com/Callback` and `https://example.com/callback`: verify both case-distinct paths in the portal. Repeat an exact URI and verify it appears only once.
15. Delete the smoke test apps.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for unreleased changes and the 1.0.0 release history.

## Status

| Verification | Status |
|---|---|
| Restore/build through `dotnet run`, version, and command help | Passed on macOS with .NET SDK 10.0.401 |
| Checked-in offline CLI regression suite | All seven test methods passed |
| Graph behavior with isolated fake responses | Passed one-off review checks; not a live integration test |
| Tenant lookup timeout and child-process cleanup | Passed locally |
| Report naming collisions and concurrent writers | Passed one-off local helper checks |
| Windows execution and live-tenant operations | Not verified |

Run the [local checks](#local-verification) after changes, then use the [smoke test checklist](#smoke-test-checklist) in a test tenant before production use.
