#!/usr/bin/env -S dotnet --
#:package Azure.Identity@*
#:property PublishAot=false

// =============================================================================
// entra-appreg.cs
// Create, expose an API on, and list Microsoft Entra ID app registrations for
// single-page applications.
// =============================================================================
//
// VERSION
//   1.0.0 (also in the Version constant below and root CHANGELOG.md;
//   show it with --version)
//
// PURPOSE
//   Manages a Microsoft Entra ID (Azure AD) App Registration for a single-page
//   application. It has three commands (see COMMANDS below):
//
//   create (the default) makes a new App Registration configured as:
//     - Supported account types: single tenant (this directory only) by
//       default, configurable with --audience
//     - Platform: Single-page application (SPA)
//     - Redirect URIs: path case preserved, exact duplicates removed
//
//   Optionally (only when you pass --scope-name) it also "exposes an API": it
//   sets the Application ID URI to api://<client id> and adds one delegated
//   permission scope. The scope mirrors the portal's "Add a scope" form: only
//   admins can consent by default (or admins and users with --scope-consent
//   users), and it is enabled by default.
//
//   Optionally (only when you pass --create-secret) it also adds a client
//   secret with a description derived from the app name and a configurable
//   lifetime (default 180 days).
//
//   Finally it writes the identifiers (and the API scope and secret, if they
//   were created) to a text file in the folder you ran the command from.
//
//   If you pass an existing app registration with --appid (its object ID or
//   client ID), create only verifies that it still exists and exits without
//   creating anything.
//
//   expose-api adds the same kind of scope (same options, same defaults) to an
//   app that ALREADY exists, so an app can be created first and have its API
//   exposed later.
//
//   list queries the tenant for app registrations (optionally filtered by name)
//   and prints them, so you can find the ID to pass to --appid. It only reads.
//
//   (During development this script was called auth_init.cs; it replaces the
//   original auth_init.py script.)
//
//   See root README.md for run instructions and the smoke test checklist, and
//   CHANGELOG.md for release history. Header examples assume the src directory.
//
// HOW THE SCRIPT IS ORGANISED
//   Help (-h, --help, help) is handled first and exits before any sign-in.
//   create:      1 parse arguments -> 2 sign in -> 3 check --appid ->
//                4 validate everything -> 5 create the app -> 6 expose an API
//                (optional) -> 7 add a secret (optional) -> 8 write the file.
//   expose-api:  parse and validate arguments -> sign in -> look the app up ->
//                merge the new scope into its existing ones -> update the app.
//   list:        parse and validate arguments -> sign in -> page through
//                GET /applications -> sort by name -> print a list (or JSON).
//   All commands share one sign-in and one HTTP client. create and expose-api
//   also share the scope helpers (ResolveScope and BuildScope) at the bottom of
//   the file.
//
// REQUIREMENTS
//   1. .NET 10 SDK or later (file-based apps are not available before .NET 10).
//        Check with:  dotnet --version
//   2. The Azure CLI (az), signed in to the tenant where the app registration
//      should be created. The script uses the standard Azure login: whatever
//      account "az login" signed in (through the AzureCliCredential class in
//      the Azure.Identity package).
//        Sign in with:  az login
//        Pick a tenant: az login --tenant <tenant id>
//        Check who you are signed in as:  az account show
//   3. Your account needs permission to create app registrations in the tenant
//      (for example the "Application Developer" role, or the tenant setting
//      that lets users register applications).
//
// COMMANDS
//   The first argument may be a command. Without one, the script runs "create".
//
//   create       (default) Create an app registration. The PURPOSE text above and
//                the ARGUMENTS below describe this command. Writing the word is
//                optional: "create --name X" and "--name X" are the same.
//
//   expose-api   Add one API scope to an EXISTING app, for when the app was
//                made earlier (by this script without --scope-name, or by
//                hand). Needs:
//                  --appid <id>       the app to change. Either identifier
//                                     works: the object ID or the client
//                                     (application) ID.
//                  --scope-name <v>   the scope to add.
//                Optional: the other --scope-* options (display name,
//                description, consent, user consent text, state).
//                Behaviour:
//                  - Scopes the app already has are kept; a scope with the
//                    same name is refused rather than duplicated.
//                  - If the app already has an Application ID URI it is kept
//                    and the scope is added under it. Otherwise the URI is set
//                    to api://<client id>.
//                  - Nothing is created and no file is written; the result is
//                    printed to the console.
//                  - Options that belong to the other commands (--name,
//                    --redirect-urls, --audience, --create-secret, the
//                    --secret-* options, --top and --json) are rejected.
//
//   list         List (query) the app registrations in the tenant. Read-only.
//                  --name <text>    only apps whose name STARTS WITH the text
//                  --top <n|all>    how many to show (default 50)
//                  --json           print a JSON array instead of a list
//                Prints a numbered list, sorted by name, showing each app's name
//                with its client ID and object ID. Use the IDs with --appid.
//                Nothing is created or changed and no file is written. Options
//                for the other commands are rejected.
//                If there are more matches than --top, the first ones Graph
//                returns are shown (sorted by name) with a note that more exist.
//                Your account needs permission to read app registrations.
//
//   help         Show help. "help" alone shows the general help; "help create",
//                "help expose-api" and "help list" show one command's help. Same
//                as --help.
//
// GETTING HELP
//   -h or --help shows help and exits with code 0, before anything else is
//   checked, so it works even next to a mistake or a missing value.
//   --version shows the version and exits with code 0 in the same way.
//     --help                    general help: every command and which options
//                               belong to which
//     create --help             all create options, with defaults and examples
//     expose-api --help         all expose-api options and behaviour
//     list --help               the list options
//     help [create|expose-api|list]  the same, written as a command
//   Running the script with no arguments at all prints the general help and
//   exits with code 2. Remember the "--" after the script name: without it
//   dotnet itself reads --help and shows its own help instead of this script's.
//   The help text is produced by ShowHelp near the bottom of the file, from the
//   same lists of allowed values and defaults the script validates against.
//
// ARGUMENTS
//   These apply to the create command unless noted. --appid and all the
//   --scope-* options also apply to expose-api. --name, --top and --json also
//   apply to list.
//
//   --name <text>          App registration display name. Required when a new
//                          app is created. Also used to name the output file
//                          and to build the secret's description.
//                          list: instead a filter, showing only apps whose
//                          name starts with this text.
//   --redirect-urls <list> One or more full redirect URIs for your SPA,
//                          separated by commas, for example
//                            "http://localhost:5173/auth/callback,https://myapp.azurewebsites.net/auth/callback"
//                          Paths and trailing slashes are preserved. Surrounding
//                          whitespace and exact duplicates are removed; paths
//                          that differ by case remain distinct. You can repeat
//                          the option instead of using commas.
//                          Quote the whole
//                          value if it contains spaces or shell-special
//                          characters.
//                          "--redirect-url" (singular) is accepted as an alias.
//                          Required when a new app is created.
//   --audience <value>     Optional. Supported account types (Graph's
//                          "signInAudience"). Case-insensitive. Default:
//                          AzureADMyOrg. One of:
//                            AzureADMyOrg
//                                single tenant: this directory only
//                            AzureADMultipleOrgs
//                                any Entra directory
//                            AzureADandPersonalMicrosoftAccount
//                                any Entra directory plus personal Microsoft
//                                accounts
//                            PersonalMicrosoftAccount
//                                personal Microsoft accounts only
//                          The two options that include personal accounts also
//                          set the app's access token version to 2 (those
//                          audiences need v2.0 tokens; without it Graph can
//                          reject the app).
//   --scope-name <value>   Optional. "Expose an API": add a delegated
//                          permission scope with this name (Graph's scope
//                          "value", for example access_as_user). Turns the
//                          feature on: without it the API is not exposed.
//                          Letters, digits, ".", "_" and "-" only, up to 120
//                          characters; must not start with ".". Sets the Application ID
//                          URI to api://<client id>, so the full scope is
//                          api://<client id>/<value>.
//                          The other --scope-* options below match the fields
//                          of the portal's "Add a scope" form and are only
//                          allowed together with --scope-name.
//   --scope-consent <admins|users>
//                          "Who can consent?". admins = "Admins only"
//                          (default). users = "Admins and users".
//   --scope-display-name <text>
//                          "Admin consent display name". Default:
//                          "Access <app name>".
//   --scope-description <text>
//                          "Admin consent description". Default: "Allow the
//                          application to access <app name> on behalf of the
//                          signed-in user."
//   --scope-user-display-name <text>
//                          "User consent display name". Only with
//                          --scope-consent users. Default: "Access <app name>".
//   --scope-user-description <text>
//                          "User consent description". Only with
//                          --scope-consent users. Default: "Allow the
//                          application to access <app name> on your behalf."
//   --scope-state <enabled|disabled>
//                          "State". Default: enabled. A disabled scope exists
//                          but client apps cannot request it.
//   --create-secret        Optional flag (takes no value). Also create a client
//                          secret for the new registration. Without this flag
//                          NO secret is created. A SPA is a public client and
//                          does not use a client secret, so leave this off
//                          unless something else (for example a backend that
//                          shares the registration) needs one.
//                          The secret's description is derived from the app
//                          registration name: "<name> secret".
//   --secret-expiry <value>
//                          How long the secret is valid. Only allowed together
//                          with --create-secret. One of:
//                            90, 180, 365, 545, 730   days from now
//                            custom                   use --secret-start and
//                                                     --secret-end
//                          Default: 180. Your tenant's app management policy
//                          can cap the maximum lifetime; if it does, Graph
//                          rejects the request with an explanatory error.
//   --secret-start <DD/MM/YYYY>
//                          Custom only. First day the secret is valid. Optional;
//                          defaults to now. Cannot be in the past.
//   --secret-end <DD/MM/YYYY>
//                          Custom only. Last day the secret is valid (it
//                          expires at the end of that day, 23:59:59 UTC).
//                          Required for a custom lifetime, and must be after
//                          the start date.
//                          Giving --secret-start or --secret-end without
//                          --secret-expiry implies "custom".
//   --top <n|all>          list only. Maximum number of app registrations to
//                          show. Default: 50. "all" pages through every result.
//   --json                 list only. Flag (no value). Print a JSON array
//                          instead of a list, and nothing else on stdout.
//   --appid <id>           create: optional. An existing registration, by object
//                          ID or client ID. If it exists, nothing is created. If
//                          it does not exist, a new registration is created.
//                          An explicitly blank value is rejected before sign-in.
//                          expose-api: required. The app to change, by object ID
//                          or client ID.
//   -h, --help             Show help and exit. Works with or without a command
//                          (see GETTING HELP above).
//
// USAGE
//   Create a new registration for local development:
//       dotnet run entra-appreg.cs -- --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
//     -> registers the redirect URI http://localhost:5173/auth/callback
//     -> writes ./My SPA.txt (in the folder you ran the command from)
//     -> does NOT create a client secret
//
//   Allow sign-in from any Entra directory instead of just your own:
//       dotnet run entra-appreg.cs -- --name "My SPA" --audience AzureADMultipleOrgs \
//           --redirect-urls http://localhost:5173/auth/callback
//
//   Expose an API with an admins-only scope (default display name and
//   description):
//       dotnet run entra-appreg.cs -- --name "My SPA" --scope-name access_as_user \
//           --redirect-urls http://localhost:5173/auth/callback
//     -> Application ID URI: api://<client id>
//     -> scope: api://<client id>/access_as_user
//
//   Same, with your own consent display name and description:
//       dotnet run entra-appreg.cs -- --name "My SPA" --scope-name access_as_user \
//           --scope-display-name "Access My SPA API" \
//           --scope-description "Allows the app to call the My SPA API on behalf of the signed-in user." \
//           --redirect-urls http://localhost:5173/auth/callback
//
//   Let users as well as admins consent, with your own user consent text, and
//   create the scope disabled:
//       dotnet run entra-appreg.cs -- --name "My SPA" --scope-name access_as_user \
//           --scope-consent users --scope-user-display-name "Access My SPA" \
//           --scope-user-description "Allows the app to access My SPA on your behalf." \
//           --scope-state disabled \
//           --redirect-urls http://localhost:5173/auth/callback
//
//   Add a scope to an app that already exists (the expose-api command). The
//   --appid value can be the object ID or the client ID:
//       dotnet run entra-appreg.cs -- expose-api --appid 00000000-0000-0000-0000-000000000000 \
//           --scope-name access_as_user
//
//   Same, with your own consent display name and description:
//       dotnet run entra-appreg.cs -- expose-api --appid 00000000-0000-0000-0000-000000000000 \
//           --scope-name access_as_user --scope-display-name "Access My SPA API" \
//           --scope-description "Allows the app to call the My SPA API on behalf of the signed-in user."
//
//   List the app registrations in the tenant (to find an ID for --appid):
//       dotnet run entra-appreg.cs -- list
//       dotnet run entra-appreg.cs -- list --name "My SPA"
//       dotnet run entra-appreg.cs -- list --top all
//       dotnet run entra-appreg.cs -- list --json
//
//   Also create a client secret (its value is written to the output file):
//       dotnet run entra-appreg.cs -- --name "My SPA" --create-secret \
//           --redirect-urls http://localhost:5173/auth/callback
//     -> secret described as "My SPA secret", valid for the default 180 days
//
//   Choose a different lifetime (90, 180, 365, 545 or 730 days):
//       dotnet run entra-appreg.cs -- --name "My SPA" --create-secret --secret-expiry 365 \
//           --redirect-urls http://localhost:5173/auth/callback
//
//   Custom start and end dates (DD/MM/YYYY):
//       dotnet run entra-appreg.cs -- --name "My SPA" --create-secret \
//           --secret-expiry custom --secret-start 01/10/2026 --secret-end 30/06/2027 \
//           --redirect-urls http://localhost:5173/auth/callback
//
//   Register several redirect URIs at once (local and deployed):
//       dotnet run entra-appreg.cs -- --name "My SPA" \
//           --redirect-urls "http://localhost:5173/auth/callback,https://myapp.example.com/auth/callback"
//
//   Only create if the registration from a previous run is missing. The value
//   is the application's object id (Graph's "id") or its client id ("appId"):
//       dotnet run entra-appreg.cs -- --appid 00000000-0000-0000-0000-000000000000 \
//           --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
//
//   Note the "--" separator. Everything after it is passed to this script
//   rather than being interpreted by the dotnet CLI itself.
//
//   On Linux/macOS you can also run it directly, because of the shebang line
//   on line 1 (it must use LF line endings and have no BOM):
//       chmod +x entra-appreg.cs
//       ./entra-appreg.cs --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
//
//   Show help (general, then one command):
//       dotnet run entra-appreg.cs -- --help
//       dotnet run entra-appreg.cs -- expose-api --help
//       dotnet run entra-appreg.cs -- help create
//       dotnet run entra-appreg.cs -- list --help
//
// WHAT HAPPENS WHEN --appid IS NOT PASSED (create)
//   The script does not look for existing apps by name. Every run without
//   --appid creates a NEW registration (and a new secret if --create-secret is
//   set), so running it twice gives you two registrations. Save the AUTH_APP_ID
//   (or the output file) and pass the ID back with --appid on later runs
//   to make the script safe to repeat.
//
// OUTPUT FILE (create only; expose-api writes no file)
//   After the app (and secret, if requested) are created, a file is written to
//   the CURRENT WORKING DIRECTORY (where you ran the command, not where this
//   script lives). It is named after the app registration, for example
//   "My SPA.txt". Any characters that are not allowed in file names are
//   replaced with "_". Existing paths are never overwritten: "-1", "-2", etc.
//   is added instead. Names are claimed atomically, including when a directory,
//   symlink, or concurrent writer occupies the candidate path.
//
//   Contents:
//       Application name
//       Application (client) ID
//       Object ID
//       Directory (tenant) ID
//       Supported account types
//       Application ID URI         } only with --scope-name
//       Scope (full name)          }
//       Scope ID                   }
//       Scope display name         }
//       Scope description          }
//       Scope consent              }
//       Scope state                }
//       Scope user display name    } only with --scope-consent users
//       Scope user description     }
//       Secret description         } only with --create-secret
//       Secret ID                  }
//       Secret value               }
//       Secret starts              }
//       Secret expires             }
//       Redirect URIs (one per line)
//
//   On Linux/macOS the file is created readable and writable by you only. On
//   Windows it gets the normal permissions of the folder.
//
//   WARNING: with --create-secret the file contains a live secret in plain
//   text, and Microsoft Graph only returns the secret value once. Do not commit
//   the file to source control (add it to .gitignore), and move it to a secret
//   store when you can.
//
// STDOUT / EXIT CODES
//   create: stdout gets progress messages plus AUTH_APP_ID, AUTH_CLIENT_ID and
//   the path of the output file. A secret value is NOT printed unless the file
//   could not be written, in which case it is printed as a last resort so it
//   isn't lost.
//   expose-api: stdout gets AUTH_APP_ID, AUTH_CLIENT_ID, the Application ID
//   URI, the full scope name, the scope ID and the consent type (there is no
//   secret and no file).
//   list: stdout gets a numbered list, or with --json only a JSON array.
//   Errors go to stderr, and so does the "more exist" note when --json is used.
//     0  Success (created a new app, added the scope, or for create the given
//        --appid already exists)
//     1  Runtime failure (sign-in failed because the Azure CLI is missing or you
//        are not signed in; Graph error, network timeout, unexpected response;
//        for expose-api also: app not found, or the scope already exists;
//        for list also: no permission to read app registrations)
//     2  Bad or missing command-line arguments (including no arguments at all,
//        which prints the general help)
//
// HOW THE DIRECTIVES AT THE TOP WORK
//   Lines starting with "#:" are read by the .NET SDK, which generates a
//   hidden project file for this script behind the scenes.
//     #:package Azure.Identity@*
//         Adds a NuGet reference. "@*" means "latest version"; pin a specific
//         version (for example Azure.Identity@1.13.2) for reproducible builds.
//     #:property PublishAot=false
//         File-based apps default to native AOT publishing. Azure.Identity may
//         not be AOT-friendly, so this turns AOT off. The docs describe it as
//         a publishing setting; "dotnet run" runs the script as ordinary
//         managed code either way.
//
// SECURITY
//   - With --create-secret the output file holds a live secret in plain text
//     (see the warning under OUTPUT FILE). The secret is printed to the console
//     only if that file cannot be written.
//   - The script signs in through the Azure CLI login ("az login") and keeps
//     the access token in memory only; it never writes it anywhere.
//   - The name filter used by list is escaped, and next-page links from Graph
//     are only followed if they point at graph.microsoft.com.
//
// KNOWN LIMITATIONS (version 1.0)
//   - No retries: a throttled request (429), a timeout or a transient 5xx fails
//     the run. The IDs are printed before the optional steps, so nothing is
//     lost if one of them fails.
//   - create never looks for an existing app by display name; only --appid
//     prevents a duplicate.
//   - One scope per run. Not covered: pre-authorised client applications, a
//     custom Application ID URI, editing or deleting scopes, adding a secret to
//     an existing app, deleting apps.
//   - list matches names that START WITH the text and sorts only the results it
//     collected.
//   - --help and --version are recognised anywhere in the arguments, including
//     as the value of another option.
//
// STATUS
//   Version 1.0.0 is the first release. Local CLI regressions and isolated Graph
//   replays pass on macOS with .NET SDK 10.0.401. Windows and live-tenant
//   operations remain unverified. Follow root README.md's smoke checklist in
//   a test tenant before relying on live operations.
//
// TIPS
//   - Keep this file OUTSIDE any folder that contains a .csproj, and be aware
//     that Directory.Build.props / Directory.Packages.props / nuget.config in
//     parent folders apply to it.
//   - The first run restores packages and compiles, so it is slower. Later
//     runs use a cache. If a change seems to be ignored, run:
//         dotnet clean entra-appreg.cs
//   - To turn this into a regular project later:
//         dotnet project convert entra-appreg.cs
// =============================================================================

using System.Diagnostics;
using System.Globalization;
using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text;
using System.Text.Json.Nodes;
using Azure.Core;
using Azure.Identity;

// The version of this script. Shown by --version and at the top of the general
// help. Change it here, in the VERSION section of the header, and in the
// root CHANGELOG.md when you make a release.
const string Version = "1.0.0";

// -----------------------------------------------------------------------------
// 1. Parse the command and command-line arguments
// -----------------------------------------------------------------------------
// In a file-based app (top-level statements), the implicit "args" variable
// holds everything after the "--" separator. A small manual loop is simpler
// than pulling in a parsing library for a handful of options. The optional
// command (create, expose-api, list or help) is read first, then help requests are
// handled, then the options.

string? appIdArg = null;
const string NoAppId = "no-id"; // placeholder meaning "no app ID", treated like an omitted --appid
string? displayName = null;
var redirectUriArgs = new List<string>(); // raw --redirect-urls values, validated later
var redirectUrisSupplied = false;

// Supported account types (Graph's "signInAudience"). The value is kept as raw
// text here and validated later against the list of values Graph accepts.
string? audienceArg = null;
string[] allowedAudiences =
[
    "AzureADMyOrg",                        // single tenant
    "AzureADMultipleOrgs",                 // any Entra directory
    "AzureADandPersonalMicrosoftAccount",  // any Entra directory + personal accounts
    "PersonalMicrosoftAccount",            // personal Microsoft accounts only
];
const string DefaultAudience = "AzureADMyOrg";

// "Expose an API" options. A scope is only added when --scope-name is given.
// The display name and description are optional and get defaults derived from
// the app name. They are kept as raw text here and validated later.
string? scopeNameArg = null;
string? scopeDisplayNameArg = null;      // admin consent display name
string? scopeDescriptionArg = null;      // admin consent description
string? scopeConsentArg = null;          // "admins" (default) or "users"
string? scopeUserDisplayNameArg = null;  // user consent display name (users only)
string? scopeUserDescriptionArg = null;  // user consent description (users only)
string? scopeStateArg = null;            // "enabled" (default) or "disabled"

// Client secret options. Secrets are opt-in: nothing is created unless the
// --create-secret flag is present.
var createSecret = false;

// "list" command options. --top limits how many app registrations are shown
// ("all" removes the limit) and --json switches the output from a list to JSON.
string? topArg = null;
var jsonOutput = false;

// Secret lifetime options. They are kept as raw text here and validated later,
// once we know whether a secret is actually being created.
string? secretExpiryArg = null; // "90" | "180" | "365" | "545" | "730" | "custom"
string? secretStartArg = null;  // DD/MM/YYYY, custom only
string? secretEndArg = null;    // DD/MM/YYYY, custom only

// The lifetimes offered by the Entra portal, in days, and the default.
int[] allowedLifetimeDays = [90, 180, 365, 545, 730];
const int DefaultLifetimeDays = 180;

// The first argument may be a subcommand:
//   create      (default) create an app registration, see the header for details
//   expose-api  add an API scope to an EXISTING app
//   list        list (query) the app registrations in the tenant
//   help        show help (a pseudo-command, handled straight away below)
// It is recognised by NOT starting with "-", so every existing command line
// (which starts with an option such as --name) still means "create".
// "args" is an ordinary parameter of the generated Main, so the subcommand can
// be removed from it and the option loop below sees only options.
var command = "create";
var commandGiven = false; // true when the user actually typed a command word
if (args.Length > 0 && !args[0].StartsWith('-'))
{
    command = args[0].ToLowerInvariant();
    commandGiven = true;
    args = args[1..];

    // "help" is a pseudo-command: "help" shows the general help and
    // "help expose-api" shows the help for one command. It is the same as
    // "--help" / "-h" but reads naturally to people who expect a help word.
    if (command == "help")
    {
        var topic = args.Length > 0 ? args[0].ToLowerInvariant() : "general";
        if (topic is not ("general" or "create" or "expose-api" or "list"))
        {
            Console.Error.WriteLine($"No help for '{topic}'. Try: help, help create, help expose-api, help list.");
            return 2;
        }

        ShowHelp(topic);
        return 0;
    }

    if (command is not ("create" or "expose-api" or "list"))
    {
        Console.Error.WriteLine($"Unknown command: {command}. Use 'create', 'expose-api', 'list' or 'help' (see --help).");
        return 2;
    }
}

// --version shows the version and stops, like --help, before anything else.
if (args.Any(a => a == "--version"))
{
    Console.WriteLine($"entra-appreg {Version}");
    return 0;
}

// -h or --help anywhere in the arguments shows help and stops, before any other
// argument is looked at, so it works even next to a mistake or a missing value.
// With a command ("expose-api --help") it shows that command's help; with none
// it shows the general help, which lists every command and option.
if (args.Any(a => a is "-h" or "--help"))
{
    ShowHelp(commandGiven ? command : "general");
    return 0;
}

// Nothing at all typed: show how to use the script instead of a bare
// "--name is required" error. Exit code 2 because no work was done.
if (args.Length == 0 && !commandGiven)
{
    Console.Error.WriteLine("No arguments given. Here is how to use this script:");
    Console.Error.WriteLine();
    ShowHelp("general");
    return 2;
}

for (var i = 0; i < args.Length; i++)
{
    var hasValue = i + 1 < args.Length && !args[i + 1].StartsWith("--", StringComparison.Ordinal);
    switch (args[i])
    {
        // Each "--option <value>" case consumes the next argument as its value.
        // The guard rejects both missing values and a following long option.
        //
        // --appid names an existing app, by either its object ID or its client
        // ID (GetApplicationAsync tries both). For create it is an app that may
        // already exist, so nothing is created; for expose-api it is the app to
        // change and is required.
        case "--appid" when hasValue:
            appIdArg = args[++i];
            break;

        case "--name" when hasValue:
            displayName = args[++i];
            break;

        // Accepts a comma-separated list of full redirect URIs ("uri1,uri2").
        // Each piece is trimmed and empty pieces are dropped. The option may
        // also be repeated; every occurrence adds to the same list.
        // "--redirect-url" is kept as an alias.
        case "--redirect-urls" or "--redirect-url" when hasValue:
            redirectUrisSupplied = true;
            redirectUriArgs.AddRange(
                args[++i].Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries));
            break;

        // Supported account types. Stored as typed; matched against the allowed
        // values (case-insensitively) once we know an app is being created.
        case "--audience" when hasValue:
            audienceArg = args[++i];
            break;

        // "Expose an API" options. --scope-name switches the feature on; the
        // other --scope-* options only customise the scope.
        case "--scope-name" when hasValue:
            scopeNameArg = args[++i];
            break;

        case "--scope-display-name" when hasValue:
            scopeDisplayNameArg = args[++i];
            break;

        case "--scope-description" when hasValue:
            scopeDescriptionArg = args[++i];
            break;

        // The rest of the portal's "Add a scope" form: who can consent, the
        // text ordinary users see (only when users may consent) and the state.
        case "--scope-consent" when hasValue:
            scopeConsentArg = args[++i];
            break;

        case "--scope-user-display-name" when hasValue:
            scopeUserDisplayNameArg = args[++i];
            break;

        case "--scope-user-description" when hasValue:
            scopeUserDescriptionArg = args[++i];
            break;

        case "--scope-state" when hasValue:
            scopeStateArg = args[++i];
            break;

        // Options for the list command. --top takes a value (a number or "all");
        // --json is a flag with no value, so it has no "when" guard and no ++i.
        case "--top" when hasValue:
            topArg = args[++i];
            break;

        case "--json":
            jsonOutput = true;
            break;

        // A boolean flag: it takes no value, so there is no "when" guard and
        // no ++i. Its presence alone turns secret creation on.
        case "--create-secret":
            createSecret = true;
            break;

        // The three secret lifetime options are stored as typed and only
        // validated later, and only if --create-secret is present.
        case "--secret-expiry" when hasValue:
            secretExpiryArg = args[++i];
            break;

        case "--secret-start" when hasValue:
            secretStartArg = args[++i];
            break;

        case "--secret-end" when hasValue:
            secretEndArg = args[++i];
            break;

        default:
            Console.Error.WriteLine($"Unknown or incomplete argument: {args[i]}. Run with --help for usage.");
            return 2;
    }
}

if (appIdArg is not null && string.IsNullOrWhiteSpace(appIdArg))
{
    Console.Error.WriteLine("--appid cannot be blank; omit it when creating a new application.");
    return 2;
}

// The expose-api subcommand works on an existing app, so it needs an app
// reference and a scope name, and it rejects the options that only make sense
// when creating an app. This is checked before signing in so a mistake fails
// immediately.
if (command == "expose-api")
{
    if (string.IsNullOrWhiteSpace(appIdArg) || appIdArg == NoAppId)
    {
        Console.Error.WriteLine("expose-api requires --appid <application object id or client id>.");
        return 2;
    }

    if (string.IsNullOrWhiteSpace(scopeNameArg))
    {
        Console.Error.WriteLine("expose-api requires --scope-name.");
        return 2;
    }

    // Collect every option that belongs to another command so the error names
    // them all at once.
    var notAllowed = Supplied(
            ("--name", displayName is not null),
            ("--redirect-urls", redirectUrisSupplied),
            ("--audience", audienceArg is not null))
        .Concat(SecretOptionsSupplied())
        .Concat(Supplied(("--top", topArg is not null), ("--json", jsonOutput)))
        .ToList();

    if (notAllowed.Count > 0)
    {
        Console.Error.WriteLine($"{string.Join(", ", notAllowed)} can't be used with the expose-api command.");
        return 2;
    }
}

// The list command only reads, so it accepts just --name (as a filter), --top and
// --json, and rejects everything that only makes sense when creating or changing
// an app. --top and --json are in turn rejected for the other commands. As with
// expose-api, this is checked before signing in so a mistake fails immediately.
var listLimit = 50; // how many app registrations to show by default
if (command == "list")
{
    var notAllowedForList = Supplied(
            ("--appid", appIdArg is not null),
            ("--redirect-urls", redirectUrisSupplied),
            ("--audience", audienceArg is not null),
            ("--scope-name", scopeNameArg is not null))
        .Concat(ScopeDetailOptionsSupplied())
        .Concat(SecretOptionsSupplied())
        .ToList();

    if (notAllowedForList.Count > 0)
    {
        Console.Error.WriteLine($"{string.Join(", ", notAllowedForList)} can't be used with the list command.");
        return 2;
    }

    // For list, --name is a filter ("starts with"), so an empty one is a mistake.
    if (displayName is not null && string.IsNullOrWhiteSpace(displayName))
    {
        Console.Error.WriteLine("--name cannot be blank.");
        return 2;
    }

    // --top is a positive whole number, or "all" to page through every result.
    if (topArg is not null)
    {
        if (topArg.Trim().Equals("all", StringComparison.OrdinalIgnoreCase))
            listLimit = int.MaxValue;
        else if (int.TryParse(topArg, out var topCount) && topCount > 0)
            listLimit = topCount;
        else
        {
            Console.Error.WriteLine($"--top must be a positive whole number or 'all', got: {topArg}");
            return 2;
        }
    }
}
else if (topArg is not null || jsonOutput)
{
    var listOnly = Supplied(("--top", topArg is not null), ("--json", jsonOutput));
    Console.Error.WriteLine($"{string.Join(", ", listOnly)} can only be used with the list command.");
    return 2;
}

// -----------------------------------------------------------------------------
// 2. Authenticate (all commands)
// -----------------------------------------------------------------------------
// The Microsoft Graph base URL, including API version. Every request below is
// built from this.
const string GraphHost = "https://graph.microsoft.com";
const string GraphBase = GraphHost + "/v1.0";
const string GraphScope = GraphHost + "/.default";

// AzureCliCredential is the standard Azure login: it reuses whatever account
// you signed in with via "az login", by asking the Azure CLI for a token. It
// uses the CLI's current tenant (see "az login --tenant <id>" and
// "az account show"). No secrets or app IDs are needed in this script.

// Ask for a Graph token. The ".default" scope means "whatever Graph
// permissions this signed-in user has already been granted".
AccessToken token;
string? tenantId = null;
try
{
    // Graph access tokens are opaque. Read tenant metadata from the CLI and pin
    // create's credential to that tenant so the report and token cannot disagree.
    if (command == "create")
        tenantId = await GetTenantIdAsync();
    var credential = new AzureCliCredential(new AzureCliCredentialOptions { TenantId = tenantId });
    token = await credential.GetTokenAsync(
        new TokenRequestContext([GraphScope]));
}
catch (Exception ex) when (ex is AuthenticationFailedException or System.ComponentModel.Win32Exception
    or InvalidOperationException)
{
    // Includes CredentialUnavailableException, which is what you get when the
    // Azure CLI isn't installed or you aren't signed in. Without this the
    // script would crash with a stack trace instead of telling you what to do.
    Console.Error.WriteLine("Could not get an access token to call Microsoft Graph.");
    Console.Error.WriteLine("Check that the Azure CLI is installed, then sign in with: az login");
    Console.Error.WriteLine($"Details: {ex.Message}");
    return 1;
}

// One HttpClient for the whole script. The 10 second timeout applies to every
// request: the lookups, the create, the updates and the secret.
using var http = new HttpClient { Timeout = TimeSpan.FromSeconds(10) };

// Attach the bearer token to every request made through this client.
http.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", token.Token);

// Wrap the actual work in try/catch so Graph or network errors print a clean
// message and return exit code 1, rather than dumping a stack trace.
try
{
    // The list command only reads, so hand over to it here and skip everything
    // below. Like expose-api it is a separate, self-contained flow.
    if (command == "list")
        return await ListAsync(displayName, listLimit, jsonOutput);

    // The expose-api command is a separate, self-contained flow: it never
    // creates anything, so hand over to it here and skip the create steps.
    if (command == "expose-api")
        return await ExposeApiAsync(appIdArg!, scopeNameArg!, scopeDisplayNameArg, scopeDescriptionArg,
            scopeConsentArg, scopeUserDisplayNameArg, scopeUserDescriptionArg, scopeStateArg);

    // -------------------------------------------------------------------------
    // 3. If an app ID was supplied, check whether it already exists
    // -------------------------------------------------------------------------
    // "no-id" is a placeholder value the original Python script (auth_init.py)
    // accepted, for example from a calling script when no app ID has been saved
    // yet. It is treated the same as not passing --appid at all.
    if (!string.IsNullOrEmpty(appIdArg) && appIdArg != NoAppId)
    {
        Console.WriteLine($"Checking if application {appIdArg} exists");

        // GetApplicationAsync (see Helpers) looks the app up by object ID and,
        // failing that, by client ID, so passing either identifier finds it.
        // That matters: the two are easy to mix up, and a client ID that was
        // only tried as an object ID would look "not found" and create a
        // duplicate app. Only a 404 on both lookups means "does not exist";
        // any other failure (401 expired token, 403 missing permission, 429
        // throttled, 5xx outage) throws, because then we DON'T KNOW and
        // creating a new app could produce a duplicate.
        if (await GetApplicationAsync(appIdArg) is not null)
        {
            // The app is there, so there is nothing to do. This makes the
            // script safe to run repeatedly.
            Console.WriteLine("Application already exists, not creating new one.");
            return 0;
        }

        // Not found by either ID: the id was given but no such app exists, so
        // fall through and        // create a new one.
        Console.WriteLine("Application not found");
    }

    // -------------------------------------------------------------------------
    // 4. Validate the inputs needed to create an app (and its optional parts)
    // -------------------------------------------------------------------------
    // Covers the app name, the supported account types, the redirect URIs and,
    // when they are requested, the API scope (--scope-name and its consent
    // text) and the secret's lifetime (--create-secret). These are only
    // checked here, after the existence check, because a run that finds the
    // app already there doesn't need them. Validating now, before any Graph
    // write, means a typo can't leave a half-configured app behind.
    if (string.IsNullOrWhiteSpace(displayName))
    {
        Console.Error.WriteLine("--name is required when creating an application.");
        return 2;
    }

    // Supported account types. Match case-insensitively but always send Graph
    // the exact canonical spelling from the allowed list. No --audience means
    // the default (single tenant).
    var audience = allowedAudiences.FirstOrDefault(a =>
        a.Equals(audienceArg?.Trim() ?? DefaultAudience, StringComparison.OrdinalIgnoreCase));
    if (audience is null)
    {
        Console.Error.WriteLine(
            $"--audience must be one of {string.Join(", ", allowedAudiences)}, got: {audienceArg}");
        return 2;
    }

    if (redirectUriArgs.Count == 0)
    {
        Console.Error.WriteLine("--redirect-urls is required when creating an application.");
        return 2;
    }

    // Validate every redirect URI. Nothing is sent to Graph until ALL of them
    // pass, so one typo in the list can't leave a half-configured app behind.
    // Parsing trims surrounding whitespace; preserve URI spelling and path case.
    // A trailing slash or a path can be significant to Entra's redirect matching.
    var redirectUris = new List<string>();
    var seenRedirectUris = new HashSet<string>(StringComparer.Ordinal);
    foreach (var uri in redirectUriArgs)
    {
        // Must be an absolute http(s) URI. Entra itself only accepts http for
        // localhost; any other host must use https, and Graph will reject it
        // with a clear error if not.
        if (!Uri.TryCreate(uri, UriKind.Absolute, out var parsed)
            || (parsed.Scheme != Uri.UriSchemeHttp && parsed.Scheme != Uri.UriSchemeHttps))
        {
            Console.Error.WriteLine($"--redirect-urls must contain absolute http(s) URIs, got: {uri}");
            return 2;
        }

        // Preserve input order and case-distinct paths, removing only exact repeats.
        if (seenRedirectUris.Add(uri))
            redirectUris.Add(uri);
    }

    // "Expose an API" options. The feature is on only when --scope-name is
    // given; everything else about the scope then has a default (see
    // ResolveScope). After this block, scopeSpec is non-null exactly when a
    // scope should be added.
    ScopeSpec? scopeSpec = null;

    if (scopeNameArg is null)
    {
        // Scope settings with no scope would be silently ignored, which is
        // almost certainly a mistake, so refuse instead and name them all.
        var orphaned = ScopeDetailOptionsSupplied();

        if (orphaned.Count > 0)
        {
            Console.Error.WriteLine($"{string.Join(", ", orphaned)} require --scope-name.");
            return 2;
        }
    }
    else
    {
        // ResolveScope (see Helpers) checks the name and every scope option and
        // fills in the defaults. It is shared with the expose-api command so
        // both behave identically.
        var resolved = ResolveScope(scopeNameArg, scopeDisplayNameArg, scopeDescriptionArg,
            scopeConsentArg, scopeUserDisplayNameArg, scopeUserDescriptionArg, scopeStateArg, displayName);
        if (resolved.Error is not null)
        {
            Console.Error.WriteLine(resolved.Error);
            return 2;
        }

        scopeSpec = resolved.Spec;
    }

    // Work out the secret's lifetime. Like the redirect URIs, this is validated
    // BEFORE anything is created so a bad date can't leave an app without the
    // secret you asked for.
    //   secretStart == null means "start now" (Graph's default), so the request
    //   omits startDateTime. secretEnd is the absolute expiry.
    DateTimeOffset? secretStart = null;
    DateTimeOffset secretEnd = default;

    var hasLifetimeArgs = secretExpiryArg is not null || secretStartArg is not null || secretEndArg is not null;

    if (!createSecret)
    {
        // Lifetime options with no secret would be silently ignored, which is
        // almost certainly a mistake, so refuse instead.
        if (hasLifetimeArgs)
        {
            Console.Error.WriteLine("--secret-expiry, --secret-start and --secret-end require --create-secret.");
            return 2;
        }
    }
    else
    {
        // No --secret-expiry: use "custom" if a start/end date was given,
        // otherwise the default number of days.
        var mode = secretExpiryArg?.Trim().ToLowerInvariant()
            ?? (secretStartArg is not null || secretEndArg is not null ? "custom" : DefaultLifetimeDays.ToString());

        if (mode == "custom")
        {
            if (secretEndArg is null)
            {
                Console.Error.WriteLine("--secret-end (DD/MM/YYYY) is required for a custom secret lifetime.");
                return 2;
            }

            if (!TryParseDate(secretEndArg, out var endDate))
            {
                Console.Error.WriteLine($"--secret-end must be a date in DD/MM/YYYY format, got: {secretEndArg}");
                return 2;
            }

            // Start is optional and defaults to today.
            var today = DateOnly.FromDateTime(DateTime.UtcNow);
            var startDate = today;
            if (secretStartArg is not null && !TryParseDate(secretStartArg, out startDate))
            {
                Console.Error.WriteLine($"--secret-start must be a date in DD/MM/YYYY format, got: {secretStartArg}");
                return 2;
            }

            if (startDate < today)
            {
                Console.Error.WriteLine($"--secret-start cannot be in the past (today is {today:dd/MM/yyyy} UTC).");
                return 2;
            }

            if (endDate <= startDate)
            {
                Console.Error.WriteLine("--secret-end must be after --secret-start.");
                return 2;
            }

            // A start of "today" is sent as "now" (by leaving startDateTime out)
            // because midnight UTC is already in the past and could be rejected.
            // A future start is sent as midnight UTC on that date.
            if (startDate > today)
                secretStart = new DateTimeOffset(startDate.ToDateTime(TimeOnly.MinValue), TimeSpan.Zero);

            // The end date is inclusive: the secret works until the last second
            // of that day (UTC).
            secretEnd = new DateTimeOffset(endDate.ToDateTime(new TimeOnly(23, 59, 59)), TimeSpan.Zero);
        }
        else if (int.TryParse(mode, out var days) && allowedLifetimeDays.Contains(days))
        {
            // A preset lifetime can't be combined with explicit dates.
            if (secretStartArg is not null || secretEndArg is not null)
            {
                Console.Error.WriteLine("--secret-start and --secret-end can only be used with --secret-expiry custom.");
                return 2;
            }

            secretEnd = DateTimeOffset.UtcNow.AddDays(days);
        }
        else
        {
            Console.Error.WriteLine(
                $"--secret-expiry must be one of {string.Join(", ", allowedLifetimeDays)} or custom, got: {secretExpiryArg}");
            return 2;
        }
    }

    // -------------------------------------------------------------------------
    // 5. Create the app registration
    // -------------------------------------------------------------------------
    Console.WriteLine($"Creating application registration '{displayName}'");
    Console.WriteLine($"  Supported account types: {audience}");
    foreach (var uri in redirectUris)
        Console.WriteLine($"  Redirect URI (SPA): {uri}");

    // The request body is built with JsonObject, which avoids defining C#
    // classes for a one-off payload.
    var appBody = new JsonObject
    {
        // Name shown in the Entra portal.
        ["displayName"] = displayName,

        // Supported account types, from --audience (default AzureADMyOrg,
        // single tenant: only accounts in your own directory can sign in).
        // The other values are AzureADMultipleOrgs (any Entra directory),
        // AzureADandPersonalMicrosoftAccount (any Entra directory plus
        // personal Microsoft accounts) and PersonalMicrosoftAccount (personal
        // accounts only).
        ["signInAudience"] = audience,

        // "spa" registers the redirect URIs under the Single-page application
        // platform. That makes Entra use the authorization code flow with PKCE
        // and allows cross-origin token redemption from browser JavaScript
        // (for example MSAL.js). This is different from the "web" platform,
        // which is for server-side apps, and it needs no implicit grant
        // setting.
        ["spa"] = new JsonObject
        {
            ["redirectUris"] = new JsonArray(
                redirectUris.Select(u => (JsonNode?)JsonValue.Create(u)).ToArray()),
        },
    };

    // Audiences that include personal Microsoft accounts need v2.0 access
    // tokens, so the app's requested token version is set to 2 explicitly;
    // without it Graph can reject the app. The tenant-only audiences don't need
    // this, so it is added only for the two personal-account audiences.
    var needsV2Tokens = audience is "AzureADandPersonalMicrosoftAccount" or "PersonalMicrosoftAccount";
    if (needsV2Tokens)
        appBody["api"] = new JsonObject { ["requestedAccessTokenVersion"] = 2 };

    // POST /applications creates the app registration.
    var app = await PostAsync("/applications", appBody);

    // Graph returns two different identifiers, which are easy to confuse:
    //   "id"    = the object id of the registration. Used in Graph URLs such as
    //             /applications/{id}. --appid accepts this or the client id.
    //   "appId" = the application (client) id. This is what your SPA is
    //             configured with as its client id.
    var appId = Require(app, "id");
    var clientId = Require(app, "appId");

    // Print the IDs NOW, before the optional API scope and secret steps. If
    // either of those fails, you still have a record of the app that was
    // created and can either delete it or re-run with --appid, instead of
    // leaving an orphan behind.
    Console.WriteLine($"AUTH_APP_ID={appId}");
    Console.WriteLine($"AUTH_CLIENT_ID={clientId}");

    // -------------------------------------------------------------------------
    // 6. Expose an API (only if --scope-name was passed)
    // -------------------------------------------------------------------------
    // This is the "Expose an API" page in the portal: an Application ID URI
    // plus one delegated permission scope. Both are set with a PATCH AFTER the
    // app exists, because the default Application ID URI, api://<client id>,
    // contains the client ID, which Graph only assigns at creation. The IDs are
    // already printed above, so if this step fails the app is not lost.
    // These stay null when no scope is requested, and the file-writing step
    // below leaves the API lines out in that case.
    string? appIdUri = null;
    string? scopeFullName = null;
    string? scopeId = null;

    if (scopeSpec is not null)
    {
        appIdUri = $"api://{clientId}";
        scopeFullName = $"{appIdUri}/{scopeSpec.Name}";
        scopeId = Guid.NewGuid().ToString();

        Console.WriteLine($"Exposing API: adding scope '{scopeFullName}' ({scopeSpec.ConsentLabel}, {scopeSpec.StateLabel})");

        // A delegated permission scope, as it appears under "Expose an API".
        // BuildScope (see Helpers) is shared with the expose-api command.
        var scope = BuildScope(scopeId, scopeSpec);

        // The "api" property also holds the token version, so repeat that
        // setting when the audience needs it rather than depend on how Graph
        // merges partial updates.
        var apiBody = new JsonObject { ["oauth2PermissionScopes"] = new JsonArray(scope) };
        if (needsV2Tokens)
            apiBody["requestedAccessTokenVersion"] = 2;

        await PatchAsync($"/applications/{appId}", new JsonObject
        {
            // The Application ID URI. api://<client id> is the standard
            // default and is always accepted for the app's own client ID.
            ["identifierUris"] = new JsonArray(appIdUri),
            ["api"] = apiBody,
        });
    }
    else
    {
        Console.WriteLine("Skipping API scope (pass --scope-name to expose an API)");
    }

    // -------------------------------------------------------------------------
    // 7. Add a client secret (only if --create-secret was passed)
    // -------------------------------------------------------------------------
    // The secret's description comes from the app name and its lifetime from
    // the options validated in step 4. These variables stay null when no secret
    // is requested, and the file-writing step below leaves the secret lines out
    // in that case.
    string? secretDescription = null;
    string? secretId = null;
    string? clientSecret = null;
    string? secretStartText = null;
    string? secretEndText = null;

    if (createSecret)
    {
        // The description ("displayName" in Graph, "Description" in the portal)
        // is derived from the app registration name, e.g. "My SPA secret".
        // It is capped at 128 characters, a conservative limit, so a very long
        // app name can't make the request fail.
        secretDescription = $"{displayName} secret";
        if (secretDescription.Length > 128)
            secretDescription = secretDescription[..128];

        Console.WriteLine($"Adding client secret '{secretDescription}' to {appId}");

        // Build the credential. "o" formats timestamps as ISO 8601, which Graph
        // requires.
        var credentialBody = new JsonObject
        {
            // Label shown next to the secret in the portal.
            ["displayName"] = secretDescription,

            // When the secret stops working, chosen by --secret-expiry (or the
            // default lifetime, DefaultLifetimeDays) and validated earlier.
            ["endDateTime"] = secretEnd.ToString("o"),
        };

        // Only send a start when it is in the future (custom lifetime). Left
        // out, Graph starts the secret immediately.
        if (secretStart is { } start)
            credentialBody["startDateTime"] = start.ToString("o");

        // POST /applications/{id}/addPassword creates a new secret on the app.
        var secret = await PostAsync($"/applications/{appId}/addPassword",
            new JsonObject { ["passwordCredential"] = credentialBody });

        // "keyId" is the secret's ID (the "Secret ID" column in the portal). It
        // identifies the secret but cannot be used to sign in.
        secretId = Require(secret, "keyId");

        // "secretText" is the actual secret value. Graph returns it only in this
        // response and can never show it again, so it must be captured now.
        clientSecret = Require(secret, "secretText");

        // Record the start and end Graph actually applied (the response echoes
        // them), which is more accurate than our own calculation when the start
        // was left to default to "now".
        secretStartText = secret["startDateTime"]?.GetValue<string>() ?? "now";
        secretEndText = secret["endDateTime"]?.GetValue<string>() ?? secretEnd.ToString("o");
    }
    else
    {
        Console.WriteLine("Skipping client secret (pass --create-secret to create one)");
    }

    // -------------------------------------------------------------------------
    // 8. Write everything to a file in the current working directory
    // -------------------------------------------------------------------------
    // Build the file contents. One "label: value" per line, easy to read and
    // easy to copy from. The API scope lines are included only when a scope was
    // added, and the secret lines only when a secret was created.
    var sb = new StringBuilder()
        .AppendLine($"Application name:        {displayName}")
        .AppendLine($"Application (client) ID: {clientId}")
        .AppendLine($"Object ID:               {appId}")
        .AppendLine($"Directory (tenant) ID:   {tenantId}")
        .AppendLine($"Supported account types: {audience}");

    if (scopeSpec is not null)
    {
        sb.AppendLine($"Application ID URI:      {appIdUri}")
          .AppendLine($"Scope (full name):       {scopeFullName}")
          .AppendLine($"Scope ID:                {scopeId}")
          .AppendLine($"Scope display name:      {scopeSpec.DisplayName}")
          .AppendLine($"Scope description:       {scopeSpec.Description}")
          .AppendLine($"Scope consent:           {scopeSpec.ConsentLabel}")
          .AppendLine($"Scope state:             {scopeSpec.StateLabel}");

        // The text ordinary users see exists only when users can consent.
        if (scopeSpec.Type == "User")
        {
            sb.AppendLine($"Scope user display name: {scopeSpec.UserDisplayName}")
              .AppendLine($"Scope user description:  {scopeSpec.UserDescription}");
        }
    }

    if (createSecret)
    {
        sb.AppendLine($"Secret description:      {secretDescription}")
          .AppendLine($"Secret ID:               {secretId}")
          .AppendLine($"Secret value:            {clientSecret}")
          .AppendLine($"Secret starts:           {secretStartText}")
          .AppendLine($"Secret expires:          {secretEndText}");
    }

    sb.AppendLine($"Redirect URIs (SPA):     {string.Join(Environment.NewLine + new string(' ', 25), redirectUris)}");
    var contents = sb.ToString();

    try
    {
        var path = WriteResultFile(displayName, contents);
        Console.WriteLine($"Wrote details to {path}");
        if (createSecret)
            Console.WriteLine("This file contains the client secret in plain text. Keep it out of source control.");
    }
    catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
    {
        // The app (and secret, if one was created) already exist and Graph will
        // never show a secret again, so if the file can't be written print
        // everything as a last resort rather than losing it.
        Console.Error.WriteLine($"Could not write the output file: {ex.Message}");
        Console.Error.WriteLine("Printing the values here instead so they are not lost:");
        Console.WriteLine(contents);
        return 1;
    }

    return 0;
}
// Only catch the failures we expect and can explain. HttpRequestException covers
// HTTP errors and connection problems, TaskCanceledException is what HttpClient
// throws when the 10 second timeout is hit, InvalidOperationException is used
// above for malformed responses, and JsonException / FormatException cover a
// response or access token that isn't valid JSON / base64. Anything else is a real bug and should
// surface with a full stack trace.
catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or InvalidOperationException
    or System.Text.Json.JsonException or FormatException)
{
    Console.Error.WriteLine($"Error: {ex.Message}");
    return 1;
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------
// In a file with top-level statements, local functions must come after the
// statements. They can still use the "http" variable declared above.
//
// Graph calls:   PostAsync, PatchAsync, GetApplicationAsync
// Scope logic:   ResolveScope, BuildScope (shared by create and expose-api)
// The expose-api command itself: ExposeApiAsync
// The list command itself: ListAsync
// Help text:     ShowHelp (general, create and expose-api topics)
// Option checks: Supplied, SecretOptionsSupplied, ScopeDetailOptionsSupplied
// Small utilities: Require, TryParseDate, GetTenantIdAsync, WriteResultFile

// The list command: query the app registrations in the tenant. It only reads.
//
// Graph returns app registrations in pages, each with an "@odata.nextLink" for
// the next one, so this follows the links until it has "limit" results or runs
// out. Only the fields we display are requested ($select) to keep the responses
// small. The sort is done here rather than by Graph ($orderby) because Graph can
// refuse to combine $orderby with $filter on directory objects.
// Note that the sort only covers the results collected: if there are more
// registrations than "limit", you see the first "limit" that Graph returned,
// sorted by name. Use --name to narrow the search, or --top all to see them all.
async Task<int> ListAsync(string? nameFilter, int limit, bool asJson)
{
    // One page is at most 999 results; a smaller limit just asks for fewer.
    var pageSize = Math.Min(limit, 999);
    var url = $"{GraphBase}/applications?$select=id,appId,displayName&$top={pageSize}";

    // --name: names that START WITH the text. In an OData string a single quote
    // is written twice, and the whole filter is URL-encoded.
    if (!string.IsNullOrWhiteSpace(nameFilter))
    {
        var text = nameFilter.Trim().Replace("'", "''");
        url += "&$filter=" + Uri.EscapeDataString($"startswith(displayName,'{text}')");
    }

    // Retain only the fields we display, not cloned Graph response trees.
    var apps = new List<(string Name, string ClientId, string ObjectId)>();
    var moreAvailable = false;
    string? next = url;

    while (next is not null && apps.Count < limit)
    {
        using var resp = await http.GetAsync(next);
        var body = await resp.Content.ReadAsStringAsync();
        if (!resp.IsSuccessStatusCode)
            throw new HttpRequestException($"GET applications failed with {(int)resp.StatusCode}: {body}");

        var page = JsonNode.Parse(body) as JsonObject
            ?? throw new InvalidOperationException("Graph returned an invalid application list.");
        var items = page["value"] as JsonArray
            ?? throw new InvalidOperationException("Graph application list is missing a 'value' array.");
        moreAvailable = items.Count > limit - apps.Count;
        foreach (var item in items)
        {
            if (apps.Count == limit)
                break;
            if (item is not JsonObject app)
                throw new InvalidOperationException("Graph returned an invalid application list entry.");
            apps.Add((app["displayName"]?.GetValue<string>() ?? "", Require(app, "appId"), Require(app, "id")));
        }

        next = page?["@odata.nextLink"]?.GetValue<string>();

        // The next-page link is followed with our access token attached, so only
        // ever follow it if it really points at Microsoft Graph.
        if (next is not null && !next.StartsWith(GraphHost + "/", StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException("Graph returned an unexpected next-page link; stopping.");
    }

    // The limit can be reached within the last page, even without a next link.
    moreAvailable |= next is not null && apps.Count >= limit;
    var sorted = apps.OrderBy(a => a.Name, StringComparer.OrdinalIgnoreCase);

    if (asJson)
    {
        // A JSON array and nothing else on stdout, so it can be piped to a tool
        // such as jq. The fields are the ones shown in the list, under Graph's
        // own names.
        var array = new JsonArray();
        foreach (var a in sorted)
        {
            array.Add(new JsonObject
            {
                ["displayName"] = a.Name,
                ["appId"] = a.ClientId,
                ["id"] = a.ObjectId,
            });
        }

        Console.WriteLine(array.ToJsonString(new System.Text.Json.JsonSerializerOptions { WriteIndented = true }));
        if (moreAvailable)
            Console.Error.WriteLine($"Showing the first {limit}; more exist. Use --top or --name.");
        return 0;
    }

    if (apps.Count == 0)
    {
        Console.WriteLine(string.IsNullOrWhiteSpace(nameFilter)
            ? "No app registrations found."
            : $"No app registrations found with a name starting with '{nameFilter.Trim()}'.");
        return 0;
    }

    // A numbered list: the name on one line, then its two IDs indented beneath
    // it, so each ID is easy to select and copy. The numbers are padded to the
    // width of the largest one so the names stay lined up.
    var numberWidth = apps.Count.ToString().Length;
    var indent = new string(' ', numberWidth + 2);

    var number = 0;
    foreach (var app in sorted)
    {
        Console.WriteLine($"{(++number).ToString().PadLeft(numberWidth)}. {app.Name}");
        Console.WriteLine($"{indent}Client ID: {app.ClientId}");
        Console.WriteLine($"{indent}Object ID: {app.ObjectId}");
    }

    Console.WriteLine();
    Console.WriteLine(moreAvailable
        ? $"Showing the first {apps.Count} app registrations; more exist. Use --top <n|all> or --name <text> to see others."
        : $"{apps.Count} app registration(s).");
    return 0;
}

// Prints help text to stdout. topic is "general", "create", "expose-api" or "list".
// The allowed values and defaults shown come from the same variables the
// parsing and validation code uses (allowedAudiences, allowedLifetimeDays,
// DefaultAudience, DefaultLifetimeDays), so the help can't drift out of step
// with what the script actually accepts. Each block is a raw string literal:
// the whitespace in front of the closing quotes is removed from every line.
void ShowHelp(string topic)
{
    var audiences = string.Join(", ", allowedAudiences);
    var lifetimes = string.Join(", ", allowedLifetimeDays);

    // Shown for --help / -h with no command, and for "help". A map of the whole
    // script: what commands exist and which options belong to which.
    var general = $"""
        entra-appreg {Version} - manage Microsoft Entra ID app registrations for single-page apps

        USAGE
          dotnet run src/entra-appreg.cs -- [command] [options]

        COMMANDS
          create           (default) Create an app registration. The word can be omitted.
          expose-api       Add an API scope to an app that already exists.
          list             List the app registrations in the tenant (read-only).
          help [command]   Show help, for example: help expose-api

        OPTIONS BY COMMAND
          create       --name  --redirect-urls  --audience  --appid
                       --scope-name  --scope-display-name  --scope-description
                       --scope-consent  --scope-user-display-name  --scope-user-description
                       --scope-state
                       --create-secret  --secret-expiry  --secret-start  --secret-end
          expose-api   --appid  --scope-name  --scope-display-name  --scope-description
                       --scope-consent  --scope-user-display-name  --scope-user-description
                       --scope-state
          list         --name  --top  --json

        HELP
          -h, --help       Show help. Add it after a command to see that command's options:
                             dotnet run src/entra-appreg.cs -- create --help
                             dotnet run src/entra-appreg.cs -- expose-api --help
                             dotnet run src/entra-appreg.cs -- list --help
          --version        Show the version and exit.

        NOTE
          Keep the "--" after the script name. Without it, dotnet itself reads
          --help and shows its own help instead of this one.
          Examples assume the repository root as the current directory.

        EXAMPLES
          dotnet run src/entra-appreg.cs -- --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
          dotnet run src/entra-appreg.cs -- expose-api --appid <id> --scope-name access_as_user
          dotnet run src/entra-appreg.cs -- list --name "My SPA"
        """;

    // Shown for "create --help" and "help create".
    var create = $"""
        create - create a new app registration (the default command)

        USAGE
          dotnet run src/entra-appreg.cs -- [create] --name <text> --redirect-urls <list> [options]

        REQUIRED (when a new app is created)
          --name <text>
              Display name of the app registration. Also names the output file and
              the secret description.
          --redirect-urls <list>
              Full redirect URIs for the SPA, comma-separated. Surrounding whitespace
              and exact repeats are removed; path case and trailing slashes are
              preserved. Can be repeated. Alias: --redirect-url. For example:
              "http://localhost:5173/auth/callback,https://myapp.example.com/auth/callback"

        OPTIONS
          --audience <value>
              Supported account types. Case-insensitive. Default: {DefaultAudience}
              One of: {audiences}
          --appid <id>
              An existing app registration, by object ID or client ID. If it exists,
              nothing is created. An explicitly blank ID is rejected.

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
              Secret lifetime in days: {lifetimes}, or custom. Default: {DefaultLifetimeDays}.
              Requires --create-secret.
          --secret-start <DD/MM/YYYY>
              Custom lifetime only. First day the secret is valid. Default: now.
              Cannot be in the past.
          --secret-end <DD/MM/YYYY>
              Custom lifetime only. Last day the secret is valid (until 23:59:59 UTC).
              Required for a custom lifetime. Must be after the start.

        HELP
          -h, --help
              Show this help.

        OUTPUT
          Writes <name>.txt (IDs, and the API scope and secret if created) to the
          current directory. An existing file is never overwritten. Prints
          AUTH_APP_ID and AUTH_CLIENT_ID. Exit codes: 0 success, 1 failure, 2 bad arguments.

        EXAMPLES
          dotnet run src/entra-appreg.cs -- --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
          dotnet run src/entra-appreg.cs -- --name "My SPA" --scope-name access_as_user --redirect-urls http://localhost:5173/auth/callback
          dotnet run src/entra-appreg.cs -- --name "My SPA" --create-secret --secret-expiry 365 --redirect-urls http://localhost:5173/auth/callback
          dotnet run src/entra-appreg.cs -- --name "My SPA" --create-secret --secret-expiry custom --secret-end 30/06/2027 --redirect-urls http://localhost:5173/auth/callback
        """;

    // Shown for "expose-api --help" and "help expose-api".
    var exposeApi = $"""
        expose-api - add an API scope to an existing app

        USAGE
          dotnet run src/entra-appreg.cs -- expose-api --appid <id> --scope-name <value> [options]

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
            --audience, --create-secret, the --secret-* options, --top and --json)
            are rejected.
          Exit codes: 0 success, 1 failure (including app not found or scope already
          exists), 2 bad arguments.

        EXAMPLES
          dotnet run src/entra-appreg.cs -- expose-api --appid 00000000-0000-0000-0000-000000000000 --scope-name access_as_user
          dotnet run src/entra-appreg.cs -- expose-api --appid <id> --scope-name access_as_user --scope-display-name "Access My SPA API"
        """;


    // Shown for "list --help" and "help list".
    var listHelp = $"""
        list - list the app registrations in the tenant (read-only)

        USAGE
          dotnet run src/entra-appreg.cs -- list [options]

        OPTIONS
          --name <text>
              Only show apps whose display name starts with this text.
          --top <n|all>
              Maximum number of app registrations to show. Default: 50. "all" pages
              through every result, which can be slow in a large tenant.
          --json
              Print a JSON array instead of a list, and nothing else on stdout,
              so the output can be piped to other tools.

        HELP
          -h, --help
              Show this help.

        OUTPUT
          A numbered list sorted by name. Each app shows its name, then its
          Client ID and Object ID. Either ID works with "--appid" for both
          "expose-api" and "create".
          Exit codes: 0 success, 1 failure (for example no permission to read
          app registrations), 2 bad arguments.

        BEHAVIOUR
          - Nothing is created or changed, and no file is written.
          - If there are more matches than --top, you see the first ones Graph
            returns, sorted by name, and a note that more exist. Narrow with --name
            or use --top all.
          - Your account needs permission to read app registrations in the tenant.

        EXAMPLES
          dotnet run src/entra-appreg.cs -- list
          dotnet run src/entra-appreg.cs -- list --name "My SPA"
          dotnet run src/entra-appreg.cs -- list --top all
          dotnet run src/entra-appreg.cs -- list --json
        """;

    Console.WriteLine(topic switch
    {
        "create" => create,
        "expose-api" => exposeApi,
        "list" => listHelp,
        _ => general,
    });
}

// Returns the names of the options that were actually supplied, from
// (option name, was it supplied?) pairs. Used to reject options that belong to a
// different command and to name all of them in one error message.
static List<string> Supplied(params (string Name, bool WasSupplied)[] options) =>
    options.Where(o => o.WasSupplied).Select(o => o.Name).ToList();

// The option groups that several of those checks share. They read the option
// variables declared at the top of the file, so a new secret or scope option
// only has to be added here to be covered everywhere.
List<string> SecretOptionsSupplied() => Supplied(
    ("--create-secret", createSecret),
    ("--secret-expiry", secretExpiryArg is not null),
    ("--secret-start", secretStartArg is not null),
    ("--secret-end", secretEndArg is not null));

// Every --scope-* option except --scope-name itself (which switches the feature on).
List<string> ScopeDetailOptionsSupplied() => Supplied(
    ("--scope-display-name", scopeDisplayNameArg is not null),
    ("--scope-description", scopeDescriptionArg is not null),
    ("--scope-consent", scopeConsentArg is not null),
    ("--scope-user-display-name", scopeUserDisplayNameArg is not null),
    ("--scope-user-description", scopeUserDescriptionArg is not null),
    ("--scope-state", scopeStateArg is not null));

// Reads a field that a Graph response must contain, as text. Throws the same
// "Response missing '<field>'" error everywhere if it is absent, which the
// main try/catch turns into a normal error message and exit code 1.
static string Require(JsonNode? node, string field) =>
    node?[field]?.GetValue<string>()
        ?? throw new InvalidOperationException($"Response missing '{field}'");

// Sends a JSON POST to a Graph path (for example "/applications"), throws with
// Graph's own error text if the response is not successful, and returns the
// parsed JSON body.
async Task<JsonNode> PostAsync(string path, JsonNode body)
{
    using var resp = await http.PostAsJsonAsync($"{GraphBase}{path}", body);

    // Read the body first so the error message can include it. Graph's error
    // JSON usually says exactly what went wrong (for example, insufficient
    // privileges).
    var text = await resp.Content.ReadAsStringAsync();

    if (!resp.IsSuccessStatusCode)
        throw new HttpRequestException($"POST {path} failed with {(int)resp.StatusCode}: {text}");

    return JsonNode.Parse(text)
        ?? throw new InvalidOperationException($"Empty response from {path}");
}

// Sends a JSON PATCH to a Graph path (for example "/applications/{id}") to
// change an existing object. Graph answers a successful update with
// "204 No Content" and no body, so there is nothing to return; a failure throws
// with Graph's own error text, like PostAsync.
async Task PatchAsync(string path, JsonNode body)
{
    using var request = new HttpRequestMessage(HttpMethod.Patch, $"{GraphBase}{path}")
    {
        Content = JsonContent.Create(body),
    };
    using var resp = await http.SendAsync(request);

    if (!resp.IsSuccessStatusCode)
    {
        var text = await resp.Content.ReadAsStringAsync();
        throw new HttpRequestException($"PATCH {path} failed with {(int)resp.StatusCode}: {text}");
    }
}

// Finds an application by EITHER of its two identifiers and returns it, or null
// if there is no such app. People often mix these up (see step 5), so accepting
// both makes --appid forgiving. It is used by create (the existence check in
// step 3, where a mixed-up ID would otherwise cause a duplicate app) and by
// expose-api:
//   1. treat the value as the object ID:   GET /applications/{id}
//   2. otherwise as the client (app) ID:   GET /applications(appId='{id}')
// Only 404 moves on to the next lookup; any other failure (401, 403, 429, 5xx)
// is an error, because it doesn't tell us the app is missing.
async Task<JsonNode?> GetApplicationAsync(string idOrClientId)
{
    var escaped = Uri.EscapeDataString(idOrClientId);
    string[] urls =
    [
        $"{GraphBase}/applications/{escaped}",
        $"{GraphBase}/applications(appId='{escaped}')",
    ];

    foreach (var url in urls)
    {
        using var resp = await http.GetAsync(url);
        if (resp.StatusCode == HttpStatusCode.NotFound)
            continue;

        var text = await resp.Content.ReadAsStringAsync();
        if (!resp.IsSuccessStatusCode)
            throw new HttpRequestException($"GET {url} failed with {(int)resp.StatusCode}: {text}");

        var app = JsonNode.Parse(text) as JsonObject
            ?? throw new InvalidOperationException($"Invalid application response from {url}");
        _ = Require(app, "id");
        _ = Require(app, "appId");
        return app;
    }

    return null;
}

// Validates the scope options and fills in the defaults. Shared by the create
// command (step 4) and the expose-api command so both apply the same rules. It
// covers every field of the portal's "Add a scope" form:
//   scope name                   nameArg
//   who can consent              consentArg        admins (default) | users
//   admin consent display name   displayArg        default "Access <app name>"
//   admin consent description    descArg           default derived from the app name
//   user consent display name    userDisplayArg    users only, default derived
//   user consent description     userDescArg       users only, default derived
//   state                        stateArg          enabled (default) | disabled
// On success Spec holds the resolved settings; on failure Error holds the message.
static (ScopeSpec? Spec, string? Error) ResolveScope(
    string nameArg, string? displayArg, string? descArg,
    string? consentArg, string? userDisplayArg, string? userDescArg, string? stateArg,
    string appName)
{
    var name = nameArg.Trim();

    // A conservative check on the scope value: letters, digits, dot,
    // underscore and hyphen only (no spaces), up to 120 characters, with no
    // leading dot as required by Graph. Typical names include access_as_user
    // and Files.Read.
    if (name.Length is 0 or > 120 || name.StartsWith('.')
        || !System.Text.RegularExpressions.Regex.IsMatch(name, "^[A-Za-z0-9._-]+$"))
    {
        return (null,
            $"--scope-name must be 1-120 characters using only letters, digits, '.', '_' and '-', and cannot start with '.', got: {nameArg}");
    }

    // Who can consent ("Who can consent?" in the portal). Graph calls the two
    // choices "Admin" (Admins only) and "User" (Admins and users).
    string type;
    switch (consentArg?.Trim().ToLowerInvariant())
    {
        case null or "admins" or "admin":
            type = "Admin";
            break;
        case "users" or "user":
            type = "User";
            break;
        default:
            return (null, $"--scope-consent must be 'admins' or 'users', got: {consentArg}");
    }

    // State ("Enabled" / "Disabled" in the portal). A disabled scope exists but
    // cannot be requested by client apps.
    bool enabled;
    switch (stateArg?.Trim().ToLowerInvariant())
    {
        case null or "enabled":
            enabled = true;
            break;
        case "disabled":
            enabled = false;
            break;
        default:
            return (null, $"--scope-state must be 'enabled' or 'disabled', got: {stateArg}");
    }

    // The user consent text is only used when users can consent. With admins
    // only it would be silently ignored, so refuse it instead.
    if (type == "Admin" && (userDisplayArg is not null || userDescArg is not null))
    {
        return (null, "--scope-user-display-name and --scope-user-description require --scope-consent users.");
    }

    // An explicitly supplied display name or description can't be blank.
    if ((displayArg is not null && string.IsNullOrWhiteSpace(displayArg))
        || (descArg is not null && string.IsNullOrWhiteSpace(descArg))
        || (userDisplayArg is not null && string.IsNullOrWhiteSpace(userDisplayArg))
        || (userDescArg is not null && string.IsNullOrWhiteSpace(userDescArg)))
    {
        return (null, "The --scope-*display-name and --scope-*description options cannot be blank.");
    }

    // Anything not supplied falls back to text derived from the app name. The
    // user consent text is written for the person being asked, so it addresses
    // them directly.
    var spec = new ScopeSpec(
        Name: name,
        DisplayName: displayArg?.Trim() ?? $"Access {appName}",
        Description: descArg?.Trim() ?? $"Allow the application to access {appName} on behalf of the signed-in user.",
        Type: type,
        UserDisplayName: type == "User" ? userDisplayArg?.Trim() ?? $"Access {appName}" : null,
        UserDescription: type == "User" ? userDescArg?.Trim() ?? $"Allow the application to access {appName} on your behalf." : null,
        Enabled: enabled);

    return (spec, null);
}

// Builds one delegated permission scope in the shape Graph expects under
// api.oauth2PermissionScopes, from settings that ResolveScope has already
// checked. Shared by the create command (step 6) and the expose-api command.
static JsonObject BuildScope(string id, ScopeSpec spec)
{
    var scope = new JsonObject
    {
        // A new unique ID for this scope. Graph requires the caller to choose
        // it, and it stays fixed for the life of the scope.
        ["id"] = id,

        // The scope name, without the api://<client id>/ prefix. Tokens carry
        // this in their "scp" claim.
        ["value"] = spec.Name,

        // "Admin" means only an administrator can grant consent; "User" means
        // ordinary users can consent too (the portal's "Admins and users").
        ["type"] = spec.Type,

        // An enabled scope can be requested by client apps; a disabled one
        // exists but cannot be requested.
        ["isEnabled"] = spec.Enabled,

        // The text an admin sees on the consent screen. Always required.
        ["adminConsentDisplayName"] = spec.DisplayName,
        ["adminConsentDescription"] = spec.Description,
    };

    // The text an ordinary user sees. It only applies, and is only sent, when
    // users are allowed to consent.
    if (spec.Type == "User")
    {
        scope["userConsentDisplayName"] = spec.UserDisplayName;
        scope["userConsentDescription"] = spec.UserDescription;
    }

    return scope;
}

// The expose-api command: add one delegated permission scope to an EXISTING app.
// Returns the process exit code. Nothing is created; the app is updated with a
// single PATCH, and nothing is written to a file (no secret is involved, so the
// details are printed to the console).
async Task<int> ExposeApiAsync(string appRef, string nameArg, string? displayArg, string? descArg,
    string? consentArg, string? userDisplayArg, string? userDescArg, string? stateArg)
{
    // Look the app up by object ID or client ID. We need the whole object, not
    // just proof it exists, because the update below must keep what's there.
    Console.WriteLine($"Looking up application {appRef}");
    var app = await GetApplicationAsync(appRef);
    if (app is null)
    {
        Console.Error.WriteLine($"Application not found: {appRef}");
        return 1;
    }

    var objectId = Require(app, "id");
    var clientId = Require(app, "appId");
    var appName = app["displayName"]?.GetValue<string>() ?? appRef;

    // Defaults for the consent text come from the app's real name, which is
    // why this happens after the lookup.
    var resolved = ResolveScope(nameArg, displayArg, descArg,
        consentArg, userDisplayArg, userDescArg, stateArg, appName);
    if (resolved.Error is not null)
    {
        Console.Error.WriteLine(resolved.Error);
        return 2;
    }

    var spec = resolved.Spec!; // non-null whenever Error is null

    // Graph replaces the WHOLE scopes list on update, so an app that already
    // exposes scopes must have them sent back along with the new one, or they
    // would be deleted. Refuse a duplicate name instead of creating a second
    // scope with the same value.
    var existingScopes = app["api"]?["oauth2PermissionScopes"]?.AsArray();
    if (existingScopes is not null && existingScopes.Any(s =>
            string.Equals(s?["value"]?.GetValue<string>(), spec.Name, StringComparison.OrdinalIgnoreCase)))
    {
        Console.Error.WriteLine($"'{appName}' already has a scope named '{spec.Name}'. Nothing was changed.");
        return 1;
    }

    // Use the app's existing Application ID URI if it has one, so the new scope
    // lives under the URI other clients already use. Only an app with no URI
    // gets the default api://<client id>.
    var existingUris = app["identifierUris"] as JsonArray;
    var existingUri = existingUris?
        .Select(u => u?.GetValue<string>())
        .FirstOrDefault(u => !string.IsNullOrEmpty(u));
    var appIdUri = existingUri ?? $"api://{clientId}";
    var scopeFullName = $"{appIdUri.TrimEnd('/')}/{spec.Name}";
    var scopeId = Guid.NewGuid().ToString();

    Console.WriteLine($"Adding scope '{scopeFullName}' to '{appName}' ({spec.ConsentLabel}, {spec.StateLabel})");

    // Start from a copy of the app's current "api" settings (token version,
    // pre-authorised clients, and so on) so the update changes only the scopes
    // and can't reset anything else stored in that property.
    var apiBody = app["api"]?.DeepClone().AsObject() ?? new JsonObject();
    var scopes = apiBody["oauth2PermissionScopes"]?.AsArray();
    if (scopes is null)
    {
        scopes = new JsonArray();
        apiBody["oauth2PermissionScopes"] = scopes;
    }
    scopes.Add(BuildScope(scopeId, spec));

    var patch = new JsonObject { ["api"] = apiBody };

    // Only set the Application ID URI when the app doesn't have one yet;
    // otherwise leave it untouched.
    if (existingUri is null)
        patch["identifierUris"] = new JsonArray(appIdUri);

    await PatchAsync($"/applications/{objectId}", patch);

    Console.WriteLine($"AUTH_APP_ID={objectId}");
    Console.WriteLine($"AUTH_CLIENT_ID={clientId}");
    Console.WriteLine($"Application ID URI: {appIdUri}");
    Console.WriteLine($"Scope (full name):  {scopeFullName}");
    Console.WriteLine($"Scope ID:           {scopeId}");
    Console.WriteLine($"Scope consent:      {spec.ConsentLabel}");
    Console.WriteLine($"Scope state:        {spec.StateLabel}");
    return 0;
}

// Parses a day-first date such as 30/06/2027 (or 1/7/2027). InvariantCulture and
// explicit formats make this independent of the machine's regional settings,
// so 03/04/2027 is always 3 April, never March 4th. The date is validated too,
// so 31/02/2027 is rejected.
static bool TryParseDate(string text, out DateOnly date) =>
    DateOnly.TryParseExact(text.Trim(), ["dd/MM/yyyy", "d/M/yyyy"],
        CultureInfo.InvariantCulture, DateTimeStyles.None, out date);

// Read tenant metadata without interpreting Graph's opaque access token.
static async Task<string> GetTenantIdAsync()
{
    var start = new ProcessStartInfo
    {
        FileName = OperatingSystem.IsWindows() ? "cmd.exe" : "az",
        UseShellExecute = false,
        RedirectStandardOutput = true,
        RedirectStandardError = true,
        CreateNoWindow = true,
    };
    if (OperatingSystem.IsWindows())
    {
        start.ArgumentList.Add("/d");
        start.ArgumentList.Add("/c");
        start.ArgumentList.Add("az");
    }
    foreach (var argument in new[] { "account", "show", "--query", "tenantId", "--output", "tsv", "--only-show-errors" })
        start.ArgumentList.Add(argument);

    using var process = Process.Start(start)
        ?? throw new InvalidOperationException("Could not start Azure CLI.");
    var output = process.StandardOutput.ReadToEndAsync();
    var error = process.StandardError.ReadToEndAsync();
    using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(10));
    try
    {
        await process.WaitForExitAsync(timeout.Token);
    }
    catch (OperationCanceledException)
    {
        process.Kill(entireProcessTree: true);
        await process.WaitForExitAsync();
        throw new InvalidOperationException("Azure CLI tenant lookup timed out.");
    }

    var tenant = (await output).Trim();
    var details = (await error).Trim();
    if (process.ExitCode != 0)
        throw new InvalidOperationException($"Azure CLI tenant lookup failed: {details}");
    if (!Guid.TryParse(tenant, out var tenantId))
        throw new InvalidOperationException("Azure CLI did not return a valid tenant ID.");
    return tenantId.ToString();
}

// Writes the file into the current working directory and returns its full path.
// The file is named after the app registration.
static string WriteResultFile(string appName, string contents)
{
    // Replace characters that are illegal in file names (such as / \ : * ? " < > |)
    // with underscores, and trim spaces and dots from the ends, which Windows
    // doesn't allow.
    var invalid = Path.GetInvalidFileNameChars();
    var safeName = new string(appName.Select(c => invalid.Contains(c) ? '_' : c).ToArray()).Trim(' ', '.');
    if (safeName.Length == 0)
        safeName = "app-registration";

    // Directory.GetCurrentDirectory() is the folder the command was run from,
    // NOT the folder holding this script.
    var dir = Directory.GetCurrentDirectory();
    var options = new FileStreamOptions
    {
        Mode = FileMode.CreateNew,
        Access = FileAccess.Write,
    };

    // On Linux/macOS, make the file readable and writable by the current user
    // only. UnixCreateMode isn't supported on Windows (the compiler warns about
    // it as CA1416 if it is set unconditionally), so it is only set elsewhere;
    // on Windows the file gets the folder's normal permissions.
    if (!OperatingSystem.IsWindows())
        options.UnixCreateMode = UnixFileMode.UserRead | UnixFileMode.UserWrite;
    // Claim each name atomically. Retry only an occupied path, not write failures:
    // an existing directory, symlink, or concurrent writer must not expose a secret
    // through the caller's last-resort console fallback.
    for (var n = 0; ; n++)
    {
        var fileName = n == 0 ? safeName + ".txt" : $"{safeName}-{n}.txt";
        var path = Path.Combine(dir, fileName);
        FileStream stream;
        try
        {
            stream = new FileStream(path, options);
        }
        catch (Exception ex) when ((ex is IOException or UnauthorizedAccessException) && Path.Exists(path))
        {
            continue;
        }

        using var writer = new StreamWriter(stream, Encoding.UTF8);
        writer.Write(contents);
        return path;
    }
}

// The fully resolved settings of one scope: what ResolveScope produces after
// checking the options and filling in defaults, and what BuildScope turns into
// the JSON Graph expects. Type is Graph's word for who can consent: "Admin" or
// "User". The user consent text is null unless Type is "User". This declaration
// is at the end of the file because type declarations must come after all
// top-level statements (which include the local functions above).
record ScopeSpec(
    string Name,
    string DisplayName,
    string Description,
    string Type,
    string? UserDisplayName,
    string? UserDescription,
    bool Enabled)
{
    // The portal's wording for the two states of "Who can consent?" and "State".
    public string ConsentLabel => Type == "Admin" ? "Admins only" : "Admins and users";
    public string StateLabel => Enabled ? "Enabled" : "Disabled";
}
