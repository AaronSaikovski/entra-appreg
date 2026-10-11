package cli

// HelpText returns the help text for a topic. version is the short build
// version shown in the general help heading.
func HelpText(t HelpTopic, version string) string {
	switch t {
	case TopicCreate:
		return createHelp
	case TopicExposeAPI:
		return exposeHelp
	case TopicList:
		return listHelp
	case TopicDelete:
		return deleteHelp
	default:
		return "entra-appreg " + version + generalHelp
	}
}

// generalHelp follows the "entra-appreg <version>" heading added by HelpText.
const generalHelp = ` - manage Microsoft Entra ID app registrations for single-page apps

USAGE
  entra-appreg [command] [options]

COMMANDS
  create           (default) Create an app registration. The word can be omitted.
  expose-api       Add an API scope to an app that already exists.
  list             List the app registrations in the tenant (read-only).
  delete           Delete an existing app registration after confirmation.
  help [command]   Show help, for example: help expose-api

OPTIONS BY COMMAND
  create       --name  --redirect-urls  --audience  --appid
               --scope-name  --scope-display-name  --scope-description
               --scope-consent  --scope-user-display-name  --scope-user-description
               --scope-state
               --create-secret  --secret-expiry  --secret-start  --secret-end
               --keyvault-uri  --keyvault-secret-name
  expose-api   --appid  --scope-name  --scope-display-name  --scope-description
               --scope-consent  --scope-user-display-name  --scope-user-description
               --scope-state
  list         --name  --top  --json
  delete       --appid
  every        --params-file <file.json>

PARAMETERS FILE
  --params-file <file.json> reads options from one JSON object whose keys are
  the option names without "--", for example {"name": "My SPA",
  "redirect-urls": ["http://localhost:5173/auth/callback"]}. Options on the
  command line override the file. Unknown keys are rejected.

HELP
  -h, --help       Show help. Add it after a command to see that command's options:
                     entra-appreg create --help
                     entra-appreg expose-api --help
                     entra-appreg list --help
                     entra-appreg delete --help
  --version        Show the version and exit.

AUTHENTICATION
  Requires Azure CLI (az) and an existing sign-in: az login.
  Uses the official Azure SDK for Go (azidentity) AzureCLICredential with your current
  Azure CLI login: one az call per command, in the active az context.
  The tenant shown or recorded is read from Microsoft Graph with that token.
  Your account needs permission for the requested operation. No
  AZURE_* client-secret settings are required. Help/version need no login.

EXAMPLES
  entra-appreg --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
  entra-appreg expose-api --appid <id> --scope-name access_as_user
  entra-appreg list --name "My SPA"
  entra-appreg create --params-file examples/spa.params.json`

const createHelp = `create - create a new app registration (the default command)

USAGE
  entra-appreg [create] --name <text> [--redirect-urls <list>] [options]

AUTHENTICATION
  Requires Azure CLI (az) and an existing sign-in: az login.
  AzureCLICredential reuses that login in the active az context; the
  report's tenant ID is read from Graph. Your account needs permission to manage
  app registrations. No AZURE_* client-secret settings are required.

REQUIRED (when a new app is created)
  --name <text>
      Display name of the app registration. Also names the output file and
      the secret description.

OPTIONS
  --redirect-urls <list>
      Full redirect URIs for the SPA, comma-separated. Surrounding whitespace
      and exact repeats are removed; path case and trailing slashes are
      preserved. Can be repeated. Alias: --redirect-url. For example:
      "http://localhost:5173/auth/callback,https://myapp.example.com/auth/callback"
      Optional, but without one the app cannot sign anyone in; a warning is
      printed to stderr.

  --audience <value>
      Supported account types. Case-insensitive. Default: AzureADMyOrg
      One of: AzureADMyOrg, AzureADMultipleOrgs, AzureADandPersonalMicrosoftAccount, PersonalMicrosoftAccount
  --appid <id>
      An existing app registration, by object ID or client ID. If it exists,
      nothing is created. An explicitly blank ID is rejected.
      New registrations are rejected if Graph finds the same display name.
      This check requires Graph read access and cannot prevent concurrent creates.
  --params-file <file.json>
      Read any of these options from a JSON object keyed by option name
      without "--". Command-line options win. See examples/spa.params.json.

EXPOSE AN API (optional)
  --scope-name <value>
      Add a scope with this name and set the Application ID URI to
      api://<client id>. Letters, digits, '.', '_' and '-' only, up to 120
      characters, not starting with '.'. Without this option no API is
      exposed. The options below match the portal's "Add a scope" form.
  --scope-consent <admins|users>
      Who can consent. admins = "Admins only" (default), users = "Admins and
      users".
  --scope-display-name <text>
      Admin consent display name. Default: "Access <app name>".
  --scope-description <text>
      Admin consent description. Default: "Allow the application to access
      <app name> on behalf of the signed-in user."
  --scope-user-display-name <text>
      User consent display name. Only with --scope-consent users.
      Default: "Access <app name>".
  --scope-user-description <text>
      User consent description. Only with --scope-consent users.
      Default: "Allow the application to access <app name> on your behalf."
  --scope-state <enabled|disabled>
      Default: enabled. A disabled scope exists but can't be requested.

CLIENT SECRET (optional)
  --create-secret
      Also create a client secret, described as "<app name> secret". Without
      this flag no secret is created. Takes no value.
  --secret-expiry <value>
      Secret lifetime in days: 90, 180, 365, 545, 730, or custom. Default: 180.
      Requires --create-secret.
  --secret-start <DD/MM/YYYY>
      Custom lifetime only. First day the secret is valid. Default: now.
      Cannot be in the past.
  --secret-end <DD/MM/YYYY>
      Custom lifetime only. Last day the secret is valid (until 23:59:59 UTC).
      Required for a custom lifetime. Must be after the start.
  --keyvault-uri <https://<vault-name>.vault.azure.net>
  --keyvault-secret-name <name>
      Also store the new secret value in this Azure Key Vault under this
      name (1-127 letters, digits or hyphens), with the secret's expiry and
      the client ID as a tag. Both are required together, with
      --create-secret. Needs 'Key Vault Secrets Officer' (or a 'Set' secret
      access policy) on the vault. An existing secret gets a new version.
      A Key Vault token is requested before anything is created. If storing
      fails, the secret is still in the report and the exit code is 1.

HELP
  -h, --help
      Show this help.

SINGLE-PAGE APP SETUP
  Every new registration also gets, as the Entra admin center does:
  - a service principal (enterprise application) in this tenant, so admins
    can grant consent and the app's own API scope can be requested;
  - the delegated Microsoft Graph permission User.Read;
  - with --scope-name, its own scope as a delegated permission, so admin
    consent covers it too.
  Consent itself is not granted.

OUTPUT
  Writes <name>.txt (IDs, permissions, and the API scope and secret if
  created) to the current directory. An existing file is never overwritten.
  Prints AUTH_APP_ID, AUTH_CLIENT_ID and AUTH_SP_ID.
  Exit codes: 0 success, 1 failure, 2 bad arguments.

EXAMPLES
  entra-appreg --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
  entra-appreg --name "My SPA" --scope-name access_as_user --redirect-urls http://localhost:5173/auth/callback
  entra-appreg --name "My SPA" --create-secret --secret-expiry 365 --redirect-urls http://localhost:5173/auth/callback
  entra-appreg --name "My SPA" --create-secret --secret-expiry custom --secret-end 30/06/2027 --redirect-urls http://localhost:5173/auth/callback
  entra-appreg create --params-file examples/spa.params.json --name "My SPA (test)"`

const exposeHelp = `expose-api - add an API scope to an existing app

USAGE
  entra-appreg expose-api --appid <id> --scope-name <value> [options]

AUTHENTICATION
  Requires Azure CLI (az) and an existing sign-in: az login.
  AzureCLICredential uses the active Azure CLI context. Your account needs
  permission to modify the target app. No AZURE_* client-secret settings
  are required.

REQUIRED
  --appid <id>
      The app to change. The object ID or the client (application) ID both work.
  --scope-name <value>
      Name of the scope to add. Letters, digits, '.', '_' and '-' only, up to
      120 characters, not starting with '.', for example access_as_user.

OPTIONS (they match the portal's "Add a scope" form)
  --scope-consent <admins|users>
      Who can consent. admins = "Admins only" (default), users = "Admins and
      users".
  --scope-display-name <text>
      Admin consent display name. Default: "Access <app name>".
  --scope-description <text>
      Admin consent description. Default: "Allow the application to access
      <app name> on behalf of the signed-in user."
  --scope-user-display-name <text>
      User consent display name. Only with --scope-consent users.
      Default: "Access <app name>".
  --scope-user-description <text>
      User consent description. Only with --scope-consent users.
      Default: "Allow the application to access <app name> on your behalf."
  --scope-state <enabled|disabled>
      Default: enabled. A disabled scope exists but can't be requested.
  --params-file <file.json>
      Read any of these options from a JSON object keyed by option name
      without "--". Command-line options win.

HELP
  -h, --help
      Show this help.

BEHAVIOUR
  - Scopes the app already has are kept. A scope with the same name is
    refused rather than duplicated.
  - An existing Application ID URI is kept and the scope is added under it.
    An app with none gets api://<client id>.
  - The scope is admins-only and enabled unless you say otherwise.
  - Nothing is created and no file is written; the result is printed.
  - Options that only apply to create or list (--name, --redirect-urls,
    --audience, --create-secret, the --secret-* and --keyvault-* options,
    --top and --json)
    are rejected.
  Exit codes: 0 success, 1 failure (including app not found or scope already
  exists), 2 bad arguments.

EXAMPLES
  entra-appreg expose-api --appid 00000000-0000-0000-0000-000000000000 --scope-name access_as_user
  entra-appreg expose-api --appid <id> --scope-name access_as_user --scope-display-name "Access My SPA API"`

const listHelp = `list - list the app registrations in the tenant (read-only)

USAGE
  entra-appreg list [options]

AUTHENTICATION
  Requires Azure CLI (az) and an existing sign-in: az login.
  AzureCLICredential uses the active Azure CLI context. Your account needs
  permission to read app registrations. No AZURE_* client-secret settings
  are required.

OPTIONS
  --name <text>
      Only show apps whose display name starts with this text.
  --top <n|all>
      Maximum number of app registrations to show. Default: 50. "all" pages
      through every result, which can be slow in a large tenant.
  --json
      Print a JSON array instead of a list, and nothing else on stdout,
      so the output can be piped to other tools.
      Tenant name and ID are printed to stderr, preserving the JSON array.
      Without --json, tenant details appear above the registrations.
      Tenant details are read from Graph /organization using the same login.
      A tenant lookup failure stops the command.
  --params-file <file.json>
      Read --name, --top or --json from a JSON object keyed by option name
      without "--". Command-line options win.

HELP
  -h, --help
      Show this help.

OUTPUT
  A numbered list sorted by name. Each app shows its name, then its
  Application (client) ID and Object ID. Either ID works with "--appid" for both
  "expose-api" and "create".
  Exit codes: 0 success, 1 failure (for example no permission to read
  app registrations), 2 bad arguments.

BEHAVIOUR
  - Nothing is created or changed, and no file is written.
  - If there are more matches than --top, you see the first ones Graph
    returns, sorted by name, and a note that more exist. Narrow with --name
    or use --top all.
  - Your signed-in account needs permission to read app registrations in
    the active Azure CLI tenant.

EXAMPLES
  entra-appreg list
  entra-appreg list --name "My SPA"
  entra-appreg list --top all
  entra-appreg list --json`

const deleteHelp = `delete - delete an existing app registration after confirmation

USAGE
  entra-appreg delete --appid <id>

OPTIONS
  --appid <id>     Required application object ID or client ID; not a name.
  --params-file <file.json>
                   Read --appid from a JSON object instead.
  -h, --help      Show this help without authentication.
  --version       Show the version and exit.

CONFIRMATION
  Requires terminal input and terminal stderr; pipes and redirected prompts
  are rejected before authentication. There is no --yes or force option.
  Shows the resolved display name, object ID, and client ID, then asks you
  to type exactly 'yes'. Enter, any other answer, or EOF cancels without
  deleting anything (exit 0).

BEHAVIOR
  Uses the active az login context. Check the selected tenant first.
  Only a lookup 404 permits fallback from object ID to client ID.
  A missing app or Graph failure exits 1; invalid arguments exit 2.
  Deletes only the resolved application object, with no retry or report.
  Deletion can interrupt applications using this registration.
  Graph soft-deletes applications; restoration is not provided by this CLI.

EXAMPLE
  entra-appreg delete --appid <id>`
