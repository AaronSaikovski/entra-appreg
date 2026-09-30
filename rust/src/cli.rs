use clap::Parser;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Command {
    Create,
    ExposeApi,
    List,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum HelpTopic {
    General,
    Create,
    ExposeApi,
    List,
}

#[expect(
    clippy::large_enum_variant,
    reason = "One invocation is parsed and immediately consumed; boxing adds an unnecessary heap allocation."
)]
#[derive(Debug)]
pub(crate) enum Invocation {
    Help(HelpTopic),
    Version,
    NoArguments,
    Run(Options),
}

#[derive(Clone, Debug, Parser)]
#[command(
    name = "entra-appreg",
    disable_help_flag = true,
    disable_version_flag = true,
    args_override_self = true
)]
pub(crate) struct Options {
    #[arg(skip = Command::Create)]
    pub(crate) command: Command,
    #[arg(long = "appid", allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) app_id: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) name: Option<String>,
    #[arg(long = "redirect-urls", alias = "redirect-url", allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) redirect_uris: Vec<String>,
    #[arg(skip)]
    pub(crate) redirects_supplied: bool,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) audience: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) scope_name: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) scope_display_name: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) scope_description: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) scope_consent: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) scope_user_display_name: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) scope_user_description: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) scope_state: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) top: Option<String>,
    #[arg(long)]
    pub(crate) json: bool,
    #[arg(long)]
    pub(crate) create_secret: bool,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) secret_expiry: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) secret_start: Option<String>,
    #[arg(long, allow_hyphen_values = true, value_parser = option_value)]
    pub(crate) secret_end: Option<String>,
    #[arg(skip = 50_usize)]
    pub(crate) list_limit: usize,
}

fn option_value(value: &str) -> Result<String, String> {
    // C# accepts single-hyphen values, but a long option never supplies a value.
    if value.starts_with("--") {
        Err("Expected a value, not a following long option.".into())
    } else {
        Ok(value.to_owned())
    }
}

pub(crate) fn parse(args: &[String]) -> Result<Invocation, String> {
    // Keep C#'s early exits outside Clap: unknown commands fail first, then
    // version beats help anywhere (even in value positions), before validation.
    let command_given = args.first().is_some_and(|arg| !arg.starts_with('-'));
    let mut command = Command::Create;
    let args = if command_given {
        let word = args[0].to_lowercase();
        command = match word.as_str() {
            "help" => {
                return match args
                    .get(1)
                    .map(|s| s.to_lowercase())
                    .as_deref()
                    .unwrap_or("general")
                {
                    "general" => Ok(Invocation::Help(HelpTopic::General)),
                    "create" => Ok(Invocation::Help(HelpTopic::Create)),
                    "expose-api" => Ok(Invocation::Help(HelpTopic::ExposeApi)),
                    "list" => Ok(Invocation::Help(HelpTopic::List)),
                    topic => Err(format!(
                        "No help for '{topic}'. Try: help, help create, help expose-api, help list."
                    )),
                };
            }
            "create" => Command::Create,
            "expose-api" => Command::ExposeApi,
            "list" => Command::List,
            _ => {
                return Err(format!(
                    "Unknown command: {word}. Use 'create', 'expose-api', 'list' or 'help' (see --help)."
                ));
            }
        };
        &args[1..]
    } else {
        args
    };
    if args.iter().any(|s| s == "--version") {
        return Ok(Invocation::Version);
    }
    if args.iter().any(|s| s == "-h" || s == "--help") {
        return Ok(Invocation::Help(if !command_given {
            HelpTopic::General
        } else {
            match command {
                Command::Create => HelpTopic::Create,
                Command::ExposeApi => HelpTopic::ExposeApi,
                Command::List => HelpTopic::List,
            }
        }));
    }
    if args.is_empty() && !command_given {
        return Ok(Invocation::NoArguments);
    }
    // Clap normally accepts --option=value and the -- terminator; C# does not.
    if let Some(argument) = args
        .iter()
        .find(|arg| arg.as_str() == "--" || (arg.starts_with("--") && arg.contains('=')))
    {
        return Err(format!(
            "Unknown or incomplete argument: {argument}. Run with --help for usage."
        ));
    }
    let mut options = Options::try_parse_from(
        std::iter::once("entra-appreg").chain(args.iter().map(String::as_str)),
    )
    .map_err(|error| error.to_string())?;
    options.command = command;
    options.redirects_supplied = !options.redirect_uris.is_empty();
    let raw_redirects = std::mem::take(&mut options.redirect_uris);
    options.redirect_uris = raw_redirects
        .iter()
        .flat_map(|value| value.split(','))
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .collect();
    if options
        .app_id
        .as_deref()
        .is_some_and(|s| s.trim().is_empty())
    {
        return Err("--appid cannot be blank; omit it when creating a new application.".into());
    }
    let secret_options = [
        ("--create-secret", options.create_secret),
        ("--secret-expiry", options.secret_expiry.is_some()),
        ("--secret-start", options.secret_start.is_some()),
        ("--secret-end", options.secret_end.is_some()),
    ];
    let list_options = [("--top", options.top.is_some()), ("--json", options.json)];
    if command == Command::ExposeApi {
        if options.app_id.as_deref().is_none_or(|s| s == "no-id") {
            return Err("expose-api requires --appid <application object id or client id>.".into());
        }
        if options
            .scope_name
            .as_deref()
            .is_none_or(|s| s.trim().is_empty())
        {
            return Err("expose-api requires --scope-name.".into());
        }
        let forbidden: Vec<_> = [
            ("--name", options.name.is_some()),
            ("--redirect-urls", options.redirects_supplied),
            ("--audience", options.audience.is_some()),
        ]
        .into_iter()
        .chain(secret_options)
        .chain(list_options)
        .filter_map(|(name, supplied)| supplied.then_some(name))
        .collect();
        if !forbidden.is_empty() {
            return Err(format!(
                "{} can't be used with the expose-api command.",
                forbidden.join(", ")
            ));
        }
    }
    if command == Command::List {
        let forbidden: Vec<_> = [
            ("--appid", options.app_id.is_some()),
            ("--redirect-urls", options.redirects_supplied),
            ("--audience", options.audience.is_some()),
            ("--scope-name", options.scope_name.is_some()),
            ("--scope-display-name", options.scope_display_name.is_some()),
            ("--scope-description", options.scope_description.is_some()),
            ("--scope-consent", options.scope_consent.is_some()),
            (
                "--scope-user-display-name",
                options.scope_user_display_name.is_some(),
            ),
            (
                "--scope-user-description",
                options.scope_user_description.is_some(),
            ),
            ("--scope-state", options.scope_state.is_some()),
        ]
        .into_iter()
        .chain(secret_options)
        .filter_map(|(name, supplied)| supplied.then_some(name))
        .collect();
        if !forbidden.is_empty() {
            return Err(format!(
                "{} can't be used with the list command.",
                forbidden.join(", ")
            ));
        }
        if options.name.as_deref().is_some_and(|s| s.trim().is_empty()) {
            return Err("--name cannot be blank.".into());
        }
        if let Some(top) = &options.top {
            options.list_limit = if top.trim().eq_ignore_ascii_case("all") {
                i32::MAX as usize
            } else {
                top.trim()
                    .parse::<i32>()
                    .ok()
                    .filter(|&n| n > 0)
                    .ok_or_else(|| {
                        format!("--top must be a positive whole number or 'all', got: {top}")
                    })? as usize
            };
        }
    } else if options.top.is_some() || options.json {
        let names: Vec<_> = list_options
            .into_iter()
            .filter_map(|(name, supplied)| supplied.then_some(name))
            .collect();
        return Err(format!(
            "{} can only be used with the list command.",
            names.join(", ")
        ));
    }
    // Creation fields and scope/secret semantics are deliberately checked only
    // after authentication and lookup, so an existing ID remains a no-op.
    Ok(Invocation::Run(options))
}

pub(crate) fn show_help(topic: HelpTopic) {
    println!(
        "{}",
        match topic {
            HelpTopic::General => GENERAL_HELP,
            HelpTopic::Create => CREATE_HELP,
            HelpTopic::ExposeApi => EXPOSE_HELP,
            HelpTopic::List => LIST_HELP,
        }
    );
}

const GENERAL_HELP: &str = r#"entra-appreg 1.0.0 - manage Microsoft Entra ID app registrations for single-page apps

USAGE
  entra-appreg [command] [options]
  cargo run --manifest-path rust/Cargo.toml -- [command] [options]

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
                     cargo run --manifest-path rust/Cargo.toml -- create --help
                     cargo run --manifest-path rust/Cargo.toml -- expose-api --help
                     cargo run --manifest-path rust/Cargo.toml -- list --help
  --version        Show the version and exit.

AUTHENTICATION
  Requires Azure CLI (az) and an existing sign-in: az login.
  Uses the official Azure Identity AzureCliCredential with your current
  Azure CLI login. Create discovers and pins the current tenant; list
  and expose-api use the active Azure CLI context.
  Your account needs permission for the requested operation. No
  AZURE_* client-secret settings are required. Help/version need no login.

NOTE
  Keep the "--" after Cargo options. Without it, Cargo itself reads
  --help and shows its own help instead of this one.
  Examples assume the repository root as the current directory.

EXAMPLES
  cargo run --manifest-path rust/Cargo.toml -- --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
  cargo run --manifest-path rust/Cargo.toml -- expose-api --appid <id> --scope-name access_as_user
  cargo run --manifest-path rust/Cargo.toml -- list --name "My SPA""#;

const CREATE_HELP: &str = r#"create - create a new app registration (the default command)

USAGE
  cargo run --manifest-path rust/Cargo.toml -- [create] --name <text> --redirect-urls <list> [options]

AUTHENTICATION
  Requires Azure CLI (az) and an existing sign-in: az login.
  AzureCliCredential reuses that login, discovers the current tenant,
  and pins authentication to it. Your account needs permission to manage
  app registrations. No AZURE_* client-secret settings are required.

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
      Supported account types. Case-insensitive. Default: AzureADMyOrg
      One of: AzureADMyOrg, AzureADMultipleOrgs, AzureADandPersonalMicrosoftAccount, PersonalMicrosoftAccount
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
      Secret lifetime in days: 90, 180, 365, 545, 730, or custom. Default: 180.
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
  cargo run --manifest-path rust/Cargo.toml -- --name "My SPA" --redirect-urls http://localhost:5173/auth/callback
  cargo run --manifest-path rust/Cargo.toml -- --name "My SPA" --scope-name access_as_user --redirect-urls http://localhost:5173/auth/callback
  cargo run --manifest-path rust/Cargo.toml -- --name "My SPA" --create-secret --secret-expiry 365 --redirect-urls http://localhost:5173/auth/callback
  cargo run --manifest-path rust/Cargo.toml -- --name "My SPA" --create-secret --secret-expiry custom --secret-end 30/06/2027 --redirect-urls http://localhost:5173/auth/callback"#;

const EXPOSE_HELP: &str = r#"expose-api - add an API scope to an existing app

USAGE
  cargo run --manifest-path rust/Cargo.toml -- expose-api --appid <id> --scope-name <value> [options]

AUTHENTICATION
  Requires Azure CLI (az) and an existing sign-in: az login.
  AzureCliCredential uses the active Azure CLI context (create instead
  discovers and pins its tenant). Your account needs permission to modify
  the target app. No AZURE_* client-secret settings are required.

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
  cargo run --manifest-path rust/Cargo.toml -- expose-api --appid 00000000-0000-0000-0000-000000000000 --scope-name access_as_user
  cargo run --manifest-path rust/Cargo.toml -- expose-api --appid <id> --scope-name access_as_user --scope-display-name "Access My SPA API""#;

const LIST_HELP: &str = r#"list - list the app registrations in the tenant (read-only)

USAGE
  cargo run --manifest-path rust/Cargo.toml -- list [options]

AUTHENTICATION
  Requires Azure CLI (az) and an existing sign-in: az login.
  AzureCliCredential uses the active Azure CLI context (create instead
  discovers and pins its tenant). Your account needs permission to read
  app registrations. No AZURE_* client-secret settings are required.

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
  - Your signed-in account needs permission to read app registrations in
    the active Azure CLI tenant.

EXAMPLES
  cargo run --manifest-path rust/Cargo.toml -- list
  cargo run --manifest-path rust/Cargo.toml -- list --name "My SPA"
  cargo run --manifest-path rust/Cargo.toml -- list --top all
  cargo run --manifest-path rust/Cargo.toml -- list --json"#;

#[cfg(test)]
mod tests {
    use super::*;

    fn parse_words(words: &[&str]) -> Result<Invocation, String> {
        parse(&words.iter().map(|s| (*s).to_owned()).collect::<Vec<_>>())
    }

    fn run(words: &[&str]) -> Options {
        match parse_words(words).unwrap() {
            Invocation::Run(options) => options,
            other => panic!("Expected a command, got {other:?}"),
        }
    }

    #[test]
    fn repeated_scalars_override_but_redirects_append_and_flags_stay_set() {
        let options = run(&[
            "create",
            "--appid",
            "old",
            "--appid",
            "new",
            "--name",
            "first",
            "--name",
            "",
            "--create-secret",
            "--create-secret",
            "--redirect-url",
            "https://a/First",
            "--redirect-urls",
            " , ",
            "--redirect-url",
            "https://a/Last",
        ]);
        assert_eq!(options.app_id.as_deref(), Some("new"));
        assert_eq!(options.name.as_deref(), Some(""));
        assert!(options.create_secret);
        assert!(options.redirects_supplied);
        assert_eq!(options.redirect_uris, ["https://a/First", "https://a/Last"]);
        let options = run(&["list", "--top", "0", "--top", "2", "--json", "--json"]);
        assert_eq!(options.list_limit, 2);
        assert!(options.json);
    }

    #[test]
    fn clap_does_not_broaden_the_accepted_argument_syntax() {
        for args in [
            &["--"][..],
            &["list", "--"],
            &["--name=value"],
            &["list", "--na", "prefix"],
            &["list", "-j"],
            &["list", "--json=true"],
            &["--create-secret=false"],
            &["list", "--name", "--unknown", "--name", "valid"],
            &["list", "--name", "valid", "extra"],
        ] {
            assert!(parse_words(args).is_err(), "{args:?}");
        }
        assert!(matches!(
            parse_words(&["--name=x", "--help"]),
            Ok(Invocation::Help(_))
        ));
        assert!(matches!(
            parse_words(&["--", "--version"]),
            Ok(Invocation::Version)
        ));
    }

    #[test]
    fn single_hyphens_and_equals_remain_literal_option_values() {
        for value in ["-", "-preview", "-x=y", "name=value", ""] {
            assert_eq!(run(&["--name", value]).name.as_deref(), Some(value));
        }
        assert!(parse_words(&["--name", "--not-a-value"]).is_err());
    }

    #[test]
    fn early_exit_precedence_ignores_invalid_options_and_values() {
        assert!(matches!(
            parse_words(&["--name", "--help", "--version"]),
            Ok(Invocation::Version)
        ));
        assert!(matches!(
            parse_words(&["--name", "-h"]),
            Ok(Invocation::Help(HelpTopic::General))
        ));
        assert!(matches!(
            parse_words(&["LiSt", "--unknown", "--help"]),
            Ok(Invocation::Help(HelpTopic::List))
        ));
        assert!(parse_words(&["unknown", "--version", "--help"]).is_err());
        assert!(parse_words(&["help", "unknown", "--version"]).is_err());
        assert!(matches!(
            parse_words(&["help", "CREATE", "--version"]),
            Ok(Invocation::Help(HelpTopic::Create))
        ));
        assert!(matches!(parse_words(&[]), Ok(Invocation::NoArguments)));
    }

    #[test]
    fn values_preserve_raw_spelling_and_redirects_accumulate() {
        let options = run(&[
            "--name",
            "first",
            "--name",
            " second ",
            "--appid",
            "no-id",
            "--redirect-url",
            " https://a/A, ,https://a/a ",
            "--redirect-urls",
            "https://a/A",
            "--scope-description",
            "-single",
            "--secret-end",
            "",
        ]);
        assert_eq!(options.name.as_deref(), Some(" second "));
        assert_eq!(options.app_id.as_deref(), Some("no-id"));
        assert_eq!(
            options.redirect_uris,
            ["https://a/A", "https://a/a", "https://a/A"]
        );
        assert!(options.redirects_supplied);
        assert_eq!(options.scope_description.as_deref(), Some("-single"));
        assert_eq!(options.secret_end.as_deref(), Some(""));
        for words in [
            &["--name"][..],
            &["--name", "--audience", "x"],
            &["--Name", "x"],
            &["--name=x"],
        ] {
            assert!(parse_words(words).is_err(), "{words:?}");
        }
    }

    #[test]
    fn creation_semantics_remain_deferred_but_blank_ids_do_not() {
        assert_eq!(run(&["create"]).command, Command::Create);
        assert_eq!(
            run(&["--appid", "existing"]).app_id.as_deref(),
            Some("existing")
        );
        assert!(
            parse_words(&[
                "--name",
                "",
                "--audience",
                "invalid",
                "--scope-state",
                "invalid",
                "--secret-expiry",
                "invalid"
            ])
            .is_ok()
        );
        assert!(parse_words(&["--appid", " \t"]).is_err());
        assert!(parse_words(&["expose-api", "--appid", "no-id", "--scope-name", "read"]).is_err());
        assert!(parse_words(&["expose-api", "--appid", "NO-ID", "--scope-name", "read"]).is_ok());
        assert!(parse_words(&["expose-api", "--appid", "id", "--scope-name", " "]).is_err());
    }

    #[test]
    fn list_top_obeys_signed_integer_boundaries() {
        for (raw, expected) in [
            (" all ", i32::MAX as usize),
            ("ALL", i32::MAX as usize),
            ("2147483647", i32::MAX as usize),
            (" +1 ", 1),
            ("999", 999),
        ] {
            assert_eq!(run(&["list", "--top", raw]).list_limit, expected);
        }
        assert_eq!(run(&["list"]).list_limit, 50);
        for raw in ["", "0", "-1", "2147483648", "1.0", "1_000", "allx"] {
            assert!(parse_words(&["list", "--top", raw]).is_err(), "{raw}");
        }
        assert!(parse_words(&["list", "--name", " "]).is_err());
    }

    #[test]
    fn forbidden_option_groups_reject_even_empty_values() {
        for option in [
            "--appid",
            "--redirect-url",
            "--audience",
            "--scope-name",
            "--scope-display-name",
            "--scope-description",
            "--scope-consent",
            "--scope-user-display-name",
            "--scope-user-description",
            "--scope-state",
            "--secret-expiry",
            "--secret-start",
            "--secret-end",
        ] {
            assert!(parse_words(&["list", option, ""]).is_err(), "{option}");
        }
        assert!(parse_words(&["list", "--create-secret"]).is_err());
        for option in [
            "--name",
            "--redirect-url",
            "--audience",
            "--top",
            "--secret-expiry",
            "--secret-start",
            "--secret-end",
        ] {
            assert!(
                parse_words(&[
                    "expose-api",
                    "--appid",
                    "id",
                    "--scope-name",
                    "read",
                    option,
                    ""
                ])
                .is_err(),
                "{option}"
            );
        }
        for option in ["--json", "--create-secret"] {
            assert!(
                parse_words(&[
                    "expose-api",
                    "--appid",
                    "id",
                    "--scope-name",
                    "read",
                    option
                ])
                .is_err()
            );
        }
        assert!(parse_words(&["create", "--json"]).is_err());
        assert!(parse_words(&["create", "--top", ""]).is_err());
    }
}
