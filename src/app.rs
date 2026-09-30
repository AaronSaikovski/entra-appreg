use std::{collections::HashSet, fmt, fmt::Write as _, io::Write as _};

use chrono::{DateTime, Duration, NaiveDate, Utc};
use serde_json::{Value, json};
use uuid::Uuid;

use crate::{
    cli::{Command, Options},
    graph::{GraphClient, encode, require_string},
    report,
};

#[derive(Debug)]
pub(crate) enum AppError {
    Usage(String),
    Runtime(anyhow::Error),
}

impl From<anyhow::Error> for AppError {
    fn from(error: anyhow::Error) -> Self {
        Self::Runtime(error)
    }
}
impl fmt::Display for AppError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Usage(message) => f.write_str(message),
            Self::Runtime(error) => write!(f, "{error:#}"),
        }
    }
}
impl std::error::Error for AppError {}

pub(crate) async fn run_command(
    options: Options,
    graph: &GraphClient,
    tenant_id: Option<&str>,
) -> Result<(), AppError> {
    match options.command {
        Command::Create => create(options, graph, tenant_id).await,
        Command::ExposeApi => expose_api(options, graph).await,
        Command::List => list(options, graph).await,
    }
}

pub(crate) struct ScopeSpec {
    pub(crate) name: String,
    pub(crate) display_name: String,
    pub(crate) description: String,
    pub(crate) scope_type: &'static str,
    pub(crate) user_display_name: Option<String>,
    pub(crate) user_description: Option<String>,
    pub(crate) enabled: bool,
}
impl ScopeSpec {
    pub(crate) fn consent_label(&self) -> &'static str {
        if self.scope_type == "Admin" {
            "Admins only"
        } else {
            "Admins and users"
        }
    }
    pub(crate) fn state_label(&self) -> &'static str {
        if self.enabled { "Enabled" } else { "Disabled" }
    }
}

pub(crate) fn resolve_scope(options: &Options, app_name: &str) -> Result<ScopeSpec, String> {
    let raw_name = options.scope_name.as_deref().unwrap_or("");
    let name = raw_name.trim();
    if name.is_empty()
        || name.len() > 120
        || name.starts_with('.')
        || !name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b'-'))
    {
        return Err(format!(
            "--scope-name must be 1-120 characters using only letters, digits, '.', '_' and '-', and cannot start with '.', got: {raw_name}"
        ));
    }
    let scope_type = match options
        .scope_consent
        .as_deref()
        .map(str::trim)
        .map(str::to_ascii_lowercase)
        .as_deref()
    {
        None | Some("admin" | "admins") => "Admin",
        Some("user" | "users") => "User",
        _ => {
            return Err(format!(
                "--scope-consent must be 'admins' or 'users', got: {}",
                options.scope_consent.as_deref().unwrap_or("")
            ));
        }
    };
    let enabled = match options
        .scope_state
        .as_deref()
        .map(str::trim)
        .map(str::to_ascii_lowercase)
        .as_deref()
    {
        None | Some("enabled") => true,
        Some("disabled") => false,
        _ => {
            return Err(format!(
                "--scope-state must be 'enabled' or 'disabled', got: {}",
                options.scope_state.as_deref().unwrap_or("")
            ));
        }
    };
    if scope_type == "Admin"
        && (options.scope_user_display_name.is_some() || options.scope_user_description.is_some())
    {
        return Err(
            "--scope-user-display-name and --scope-user-description require --scope-consent users."
                .into(),
        );
    }
    if [
        &options.scope_display_name,
        &options.scope_description,
        &options.scope_user_display_name,
        &options.scope_user_description,
    ]
    .iter()
    .any(|value| value.as_deref().is_some_and(|s| s.trim().is_empty()))
    {
        return Err(
            "The --scope-*display-name and --scope-*description options cannot be blank.".into(),
        );
    }
    Ok(ScopeSpec {
        name: name.into(),
        display_name: options
            .scope_display_name
            .as_deref()
            .map(|s| s.trim().to_owned())
            .unwrap_or_else(|| format!("Access {app_name}")),
        description: options
            .scope_description
            .as_deref()
            .map(|s| s.trim().to_owned())
            .unwrap_or_else(|| {
                format!(
                    "Allow the application to access {app_name} on behalf of the signed-in user."
                )
            }),
        scope_type,
        user_display_name: (scope_type == "User").then(|| {
            options
                .scope_user_display_name
                .as_deref()
                .map(|s| s.trim().to_owned())
                .unwrap_or_else(|| format!("Access {app_name}"))
        }),
        user_description: (scope_type == "User").then(|| {
            options
                .scope_user_description
                .as_deref()
                .map(|s| s.trim().to_owned())
                .unwrap_or_else(|| {
                    format!("Allow the application to access {app_name} on your behalf.")
                })
        }),
        enabled,
    })
}

pub(crate) fn build_scope(id: &str, spec: &ScopeSpec) -> Value {
    let mut scope = json!({"id": id, "value": spec.name, "type": spec.scope_type,
        "isEnabled": spec.enabled, "adminConsentDisplayName": spec.display_name,
        "adminConsentDescription": spec.description});
    if spec.scope_type == "User" {
        scope["userConsentDisplayName"] = json!(spec.user_display_name);
        scope["userConsentDescription"] = json!(spec.user_description);
    }
    scope
}

struct SecretLifetime {
    start: Option<DateTime<Utc>>,
    end: DateTime<Utc>,
}

fn parse_date(value: &str) -> Option<NaiveDate> {
    let mut parts = value.trim().split('/');
    let day = parts.next()?;
    let month = parts.next()?;
    let year = parts.next()?;
    if parts.next().is_some()
        || !(1..=2).contains(&day.len())
        || !(1..=2).contains(&month.len())
        || year.len() != 4
        || ![day, month, year]
            .iter()
            .all(|s| s.bytes().all(|b| b.is_ascii_digit()))
    {
        return None;
    }
    let year = year.parse().ok()?;
    if !(1..=9999).contains(&year) {
        return None;
    }
    NaiveDate::from_ymd_opt(year, month.parse().ok()?, day.parse().ok()?)
}

fn resolve_lifetime(
    options: &Options,
    now: DateTime<Utc>,
) -> Result<Option<SecretLifetime>, String> {
    let dates = options.secret_start.is_some() || options.secret_end.is_some();
    if !options.create_secret {
        return if dates || options.secret_expiry.is_some() {
            Err("--secret-expiry, --secret-start and --secret-end require --create-secret.".into())
        } else {
            Ok(None)
        };
    }
    let mode = options
        .secret_expiry
        .as_deref()
        .map(str::trim)
        .unwrap_or(if dates { "custom" } else { "180" });
    if mode.eq_ignore_ascii_case("custom") {
        let end_arg = options
            .secret_end
            .as_deref()
            .ok_or("--secret-end (DD/MM/YYYY) is required for a custom secret lifetime.")?;
        let end = parse_date(end_arg).ok_or_else(|| {
            format!("--secret-end must be a date in DD/MM/YYYY format, got: {end_arg}")
        })?;
        let today = now.date_naive();
        let start = match options.secret_start.as_deref() {
            Some(raw) => parse_date(raw).ok_or_else(|| {
                format!("--secret-start must be a date in DD/MM/YYYY format, got: {raw}")
            })?,
            None => today,
        };
        if start < today {
            return Err(format!(
                "--secret-start cannot be in the past (today is {} UTC).",
                today.format("%d/%m/%Y")
            ));
        }
        if end <= start {
            return Err("--secret-end must be after --secret-start.".into());
        }
        Ok(Some(SecretLifetime {
            start: (start > today).then(|| start.and_hms_opt(0, 0, 0).unwrap().and_utc()),
            end: end.and_hms_opt(23, 59, 59).unwrap().and_utc(),
        }))
    } else {
        let days = mode
            .parse::<i64>()
            .ok()
            .filter(|days| [90, 180, 365, 545, 730].contains(days))
            .ok_or_else(|| {
                format!(
                    "--secret-expiry must be one of 90, 180, 365, 545, 730 or custom, got: {}",
                    options.secret_expiry.as_deref().unwrap_or("")
                )
            })?;
        if dates {
            return Err(
                "--secret-start and --secret-end can only be used with --secret-expiry custom."
                    .into(),
            );
        }
        Ok(Some(SecretLifetime {
            start: None,
            end: now + Duration::days(days),
        }))
    }
}

fn secret_description(app_name: &str) -> String {
    let mut description = String::with_capacity(128);
    let mut units = 0;
    for ch in app_name.chars().chain(" secret".chars()) {
        units += ch.len_utf16();
        if units > 128 {
            break;
        }
        description.push(ch);
    }
    description
}

fn optional_string<'a>(value: &'a Value, key: &str, fallback: &'a str) -> anyhow::Result<&'a str> {
    match value.get(key) {
        None | Some(Value::Null) => Ok(fallback),
        Some(Value::String(text)) => Ok(text),
        _ => anyhow::bail!("Graph response field '{key}' must be a string."),
    }
}

fn valid_redirect(uri: &str) -> bool {
    // Url's WHATWG parser repairs "https:host" and extra leading slashes.
    // C# requires an explicit HTTP(S) authority; do not accept repaired input.
    uri.split_once("://").is_some_and(|(scheme, authority)| {
        (scheme.eq_ignore_ascii_case("http") || scheme.eq_ignore_ascii_case("https"))
            && !authority.starts_with(['/', '\\'])
            && url::Url::parse(uri).is_ok()
    })
}

pub(crate) async fn create(
    options: Options,
    graph: &GraphClient,
    tenant_id: Option<&str>,
) -> Result<(), AppError> {
    if let Some(reference) = options
        .app_id
        .as_deref()
        .filter(|id| !id.is_empty() && *id != "no-id")
    {
        println!("Checking if application {reference} exists");
        if graph.get_application(reference).await?.is_some() {
            println!("Application already exists, not creating new one.");
            return Ok(());
        }
        println!("Application not found");
    }
    let name = options
        .name
        .as_deref()
        .filter(|name| !name.trim().is_empty())
        .ok_or_else(|| {
            AppError::Usage("--name is required when creating an application.".into())
        })?;
    let audiences = [
        "AzureADMyOrg",
        "AzureADMultipleOrgs",
        "AzureADandPersonalMicrosoftAccount",
        "PersonalMicrosoftAccount",
    ];
    let raw_audience = options
        .audience
        .as_deref()
        .map(str::trim)
        .unwrap_or("AzureADMyOrg");
    let audience = audiences
        .into_iter()
        .find(|a| a.eq_ignore_ascii_case(raw_audience))
        .ok_or_else(|| {
            AppError::Usage(format!(
                "--audience must be one of {}, got: {raw_audience}",
                audiences.join(", ")
            ))
        })?;
    if options.redirect_uris.is_empty() {
        return Err(AppError::Usage(
            "--redirect-urls is required when creating an application.".into(),
        ));
    }
    let mut redirects = Vec::new();
    let mut seen = HashSet::new();
    for uri in &options.redirect_uris {
        let uri = uri.trim();
        if !valid_redirect(uri) {
            return Err(AppError::Usage(format!(
                "--redirect-urls must contain absolute http(s) URIs, got: {uri}"
            )));
        }
        if seen.insert(uri) {
            redirects.push(uri);
        }
    }
    let spec = if options.scope_name.is_some() {
        Some(resolve_scope(&options, name).map_err(AppError::Usage)?)
    } else {
        let orphaned: Vec<_> = [
            ("--scope-display-name", &options.scope_display_name),
            ("--scope-description", &options.scope_description),
            ("--scope-consent", &options.scope_consent),
            (
                "--scope-user-display-name",
                &options.scope_user_display_name,
            ),
            ("--scope-user-description", &options.scope_user_description),
            ("--scope-state", &options.scope_state),
        ]
        .into_iter()
        .filter_map(|(flag, value)| value.is_some().then_some(flag))
        .collect();
        if !orphaned.is_empty() {
            return Err(AppError::Usage(format!(
                "{} require --scope-name.",
                orphaned.join(", ")
            )));
        }
        None
    };
    let lifetime = resolve_lifetime(&options, Utc::now()).map_err(AppError::Usage)?;
    println!("Creating application registration '{name}'");
    println!("  Supported account types: {audience}");
    for uri in &redirects {
        println!("  Redirect URI (SPA): {uri}");
    }
    let needs_v2 = matches!(
        audience,
        "AzureADandPersonalMicrosoftAccount" | "PersonalMicrosoftAccount"
    );
    let mut body = json!({"displayName": name, "signInAudience": audience, "spa": {"redirectUris": redirects}});
    if needs_v2 {
        body["api"] = json!({"requestedAccessTokenVersion": 2});
    }
    let app = graph.post("/applications", &body).await?;
    let object_id = require_string(&app, "id")?;
    let client_id = require_string(&app, "appId")?;
    println!("AUTH_APP_ID={object_id}");
    println!("AUTH_CLIENT_ID={client_id}");
    std::io::stdout().flush().map_err(anyhow::Error::from)?;
    let object_path = format!("/applications/{}", encode(object_id));
    let mut contents = String::new();
    writeln!(contents, "Application name:        {name}\nApplication (client) ID: {client_id}\nObject ID:               {object_id}\nDirectory (tenant) ID:   {}\nSupported account types: {audience}", tenant_id.unwrap_or("")).unwrap();
    if let Some(spec) = spec {
        let identifier = format!("api://{client_id}");
        let full_name = format!("{identifier}/{}", spec.name);
        let scope_id = Uuid::new_v4().to_string();
        println!(
            "Exposing API: adding scope '{full_name}' ({}, {})",
            spec.consent_label(),
            spec.state_label()
        );
        let mut api = json!({"oauth2PermissionScopes": [build_scope(&scope_id, &spec)]});
        if needs_v2 {
            api["requestedAccessTokenVersion"] = json!(2);
        }
        graph
            .patch(
                &object_path,
                &json!({"identifierUris": [identifier], "api": api}),
            )
            .await?;
        writeln!(contents, "Application ID URI:      {identifier}\nScope (full name):       {full_name}\nScope ID:                {scope_id}\nScope display name:      {}\nScope description:       {}\nScope consent:           {}\nScope state:             {}", spec.display_name, spec.description, spec.consent_label(), spec.state_label()).unwrap();
        if spec.scope_type == "User" {
            writeln!(
                contents,
                "Scope user display name: {}\nScope user description:  {}",
                spec.user_display_name.as_deref().unwrap_or(""),
                spec.user_description.as_deref().unwrap_or("")
            )
            .unwrap();
        }
    } else {
        println!("Skipping API scope (pass --scope-name to expose an API)");
    }
    if let Some(lifetime) = lifetime {
        let description = secret_description(name);
        println!("Adding client secret '{description}' to {object_id}");
        let end = lifetime
            .end
            .to_rfc3339_opts(chrono::SecondsFormat::AutoSi, true);
        let mut credential = json!({"displayName": description, "endDateTime": end});
        if let Some(start) = lifetime.start {
            credential["startDateTime"] =
                json!(start.to_rfc3339_opts(chrono::SecondsFormat::AutoSi, true));
        }
        let secret = graph
            .post(
                &format!("{object_path}/addPassword"),
                &json!({"passwordCredential": credential}),
            )
            .await?;
        let key = require_string(&secret, "keyId")?;
        let text = require_string(&secret, "secretText")?;
        let start_text = optional_string(&secret, "startDateTime", "now")?;
        let end_text = optional_string(&secret, "endDateTime", &end)?;
        writeln!(contents, "Secret description:      {description}\nSecret ID:               {key}\nSecret value:            {text}\nSecret starts:           {start_text}\nSecret expires:          {end_text}").unwrap();
    } else {
        println!("Skipping client secret (pass --create-secret to create one)");
    }
    writeln!(
        contents,
        "Redirect URIs (SPA):     {}",
        redirects.join("\n                         ")
    )
    .unwrap();
    #[cfg(windows)]
    let contents = contents.replace('\n', "\r\n");
    match report::write_result_file(name, &contents) {
        Ok(path) => {
            println!("Wrote details to {}", path.display());
            if options.create_secret {
                println!(
                    "This file contains the client secret in plain text. Keep it out of source control."
                );
            }
            Ok(())
        }
        Err(error) => {
            eprintln!("Could not write the output file: {error}");
            eprintln!("Printing the values here instead so they are not lost:");
            println!("{contents}");
            Err(AppError::Runtime(error.into()))
        }
    }
}

async fn expose_api(options: Options, graph: &GraphClient) -> Result<(), AppError> {
    let reference = options
        .app_id
        .as_deref()
        .ok_or_else(|| AppError::Usage("expose-api requires --appid.".into()))?;
    println!("Looking up application {reference}");
    let mut app = graph
        .get_application(reference)
        .await?
        .ok_or_else(|| anyhow::anyhow!("Application not found: {reference}"))?;
    // Move the entire fetched API object, retaining unknown settings and scopes.
    let mut api = match app.get_mut("api").map(Value::take) {
        None | Some(Value::Null) => serde_json::Map::new(),
        Some(Value::Object(api)) => api,
        _ => return Err(anyhow::anyhow!("Graph application 'api' must be an object.").into()),
    };
    let object_id = require_string(&app, "id")?;
    let client_id = require_string(&app, "appId")?;
    let name = optional_string(&app, "displayName", reference)?;
    let spec = resolve_scope(&options, name).map_err(AppError::Usage)?;
    let scopes = api
        .entry("oauth2PermissionScopes")
        .or_insert_with(|| json!([]));
    if scopes.is_null() {
        *scopes = json!([]);
    }
    let scopes = scopes
        .as_array_mut()
        .ok_or_else(|| anyhow::anyhow!("Graph API scopes must be an array."))?;
    for scope in scopes.iter() {
        if scope.is_null() {
            continue;
        }
        if !scope.is_object() {
            return Err(anyhow::anyhow!("Graph returned an invalid API scope.").into());
        }
        if optional_string(scope, "value", "")?.eq_ignore_ascii_case(&spec.name) {
            return Err(anyhow::anyhow!(
                "'{name}' already has a scope named '{}'. Nothing was changed.",
                spec.name
            )
            .into());
        }
    }
    let mut existing_uri = None;
    if let Some(uris) = app.get("identifierUris").and_then(Value::as_array) {
        for uri in uris {
            let uri = match uri {
                Value::Null => continue,
                Value::String(uri) => uri,
                _ => return Err(anyhow::anyhow!("Graph identifier URIs must be strings.").into()),
            };
            if !uri.is_empty() {
                existing_uri = Some(uri.as_str());
                break;
            }
        }
    }
    let default_uri;
    let identifier = match existing_uri {
        Some(uri) => uri,
        None => {
            default_uri = format!("api://{client_id}");
            &default_uri
        }
    };
    let full_name = format!("{}/{}", identifier.trim_end_matches('/'), spec.name);
    let scope_id = Uuid::new_v4().to_string();
    println!(
        "Adding scope '{full_name}' to '{name}' ({}, {})",
        spec.consent_label(),
        spec.state_label()
    );
    scopes.push(build_scope(&scope_id, &spec));
    let mut patch = Value::Object(serde_json::Map::from_iter([(
        "api".into(),
        Value::Object(api),
    )]));
    if existing_uri.is_none() {
        patch["identifierUris"] = json!([identifier]);
    }
    graph
        .patch(&format!("/applications/{}", encode(object_id)), &patch)
        .await?;
    println!("AUTH_APP_ID={object_id}");
    println!("AUTH_CLIENT_ID={client_id}");
    println!("Application ID URI: {identifier}");
    println!("Scope (full name):  {full_name}");
    println!("Scope ID:           {scope_id}");
    println!("Scope consent:      {}", spec.consent_label());
    println!("Scope state:        {}", spec.state_label());
    Ok(())
}

#[derive(serde::Serialize)]
struct ListedApplication {
    #[serde(rename = "displayName")]
    name: String,
    #[serde(rename = "appId")]
    client_id: String,
    id: String,
    #[serde(skip)]
    sort_key: String,
}

fn take_required_string(
    object: &mut serde_json::Map<String, Value>,
    key: &str,
) -> anyhow::Result<String> {
    match object.remove(key) {
        Some(Value::String(value)) => Ok(value),
        _ => anyhow::bail!("Graph response requires an object with string '{key}'"),
    }
}

async fn collect_applications(
    options: &Options,
    graph: &GraphClient,
) -> anyhow::Result<(Vec<ListedApplication>, bool)> {
    let limit = options.list_limit;
    let mut path = format!(
        "/applications?$select=id,appId,displayName&$top={}",
        limit.min(999)
    );
    if let Some(name) = &options.name {
        let filter = format!(
            "startswith(displayName,'{}')",
            name.trim().replace('\'', "''")
        );
        path.push_str("&$filter=");
        path.push_str(&encode(&filter));
    }
    let mut next = Some(graph.url(&path)?);
    let mut requested = HashSet::new();
    let mut apps = Vec::new();
    let mut more = false;
    while let Some(url) = next.take() {
        if !requested.insert(url.clone()) {
            anyhow::bail!("Graph returned a repeated next-page link; stopping.");
        }
        let mut page = graph.get_url(&url).await?;
        if !page.is_object() {
            anyhow::bail!("Graph returned an invalid application list.");
        }
        let items = match page.get_mut("value").map(Value::take) {
            Some(Value::Array(items)) => items,
            _ => anyhow::bail!("Graph application list is missing a 'value' array."),
        };
        more = items.len() > limit - apps.len();
        for item in items.into_iter().take(limit - apps.len()) {
            let Value::Object(mut item) = item else {
                anyhow::bail!("Graph returned an invalid application list entry.");
            };
            let name = match item.remove("displayName") {
                None | Some(Value::Null) => String::new(),
                Some(Value::String(name)) => name,
                _ => anyhow::bail!("Graph displayName must be a string."),
            };
            let client_id = take_required_string(&mut item, "appId")?;
            let id = take_required_string(&mut item, "id")?;
            let sort_key = name.to_uppercase();
            apps.push(ListedApplication {
                name,
                client_id,
                id,
                sort_key,
            });
        }
        next = match page.get("@odata.nextLink") {
            None | Some(Value::Null) => None,
            Some(Value::String(link)) => Some(graph.validate_next_link(link)?),
            _ => anyhow::bail!("Graph returned an invalid next-page link."),
        };
        // Validate supplied links even when the requested count was reached.
        if next.as_ref().is_some_and(|url| requested.contains(url)) {
            anyhow::bail!("Graph returned a repeated next-page link; stopping.");
        }
        if apps.len() == limit {
            more |= next.is_some();
            break;
        }
    }
    apps.sort_by(|left, right| left.sort_key.cmp(&right.sort_key));
    Ok((apps, more))
}

async fn list(options: Options, graph: &GraphClient) -> Result<(), AppError> {
    let (apps, more) = collect_applications(&options, graph).await?;
    if options.json {
        let stdout = std::io::stdout();
        let mut output = stdout.lock();
        serde_json::to_writer_pretty(&mut output, &apps).map_err(anyhow::Error::from)?;
        writeln!(output).map_err(anyhow::Error::from)?;
        if more {
            eprintln!(
                "Showing the first {}; more exist. Use --top or --name.",
                options.list_limit
            );
        }
    } else if apps.is_empty() {
        match options.name {
            Some(name) => println!(
                "No app registrations found with a name starting with '{}'.",
                name.trim()
            ),
            None => println!("No app registrations found."),
        }
    } else {
        let width = apps.len().to_string().len();
        let indent = " ".repeat(width + 2);
        for (index, app) in apps.iter().enumerate() {
            println!("{:>width$}. {}", index + 1, app.name);
            println!("{indent}Client ID: {}", app.client_id);
            println!("{indent}Object ID: {}", app.id);
        }
        println!();
        if more {
            println!(
                "Showing the first {} app registrations; more exist. Use --top <n|all> or --name <text> to see others.",
                apps.len()
            );
        } else {
            println!("{} app registration(s).", apps.len());
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        cli::{self, Invocation},
        graph::tests::Fixture,
    };

    fn options(args: &[&str]) -> Options {
        match cli::parse(&args.iter().map(|s| (*s).to_owned()).collect::<Vec<_>>()).unwrap() {
            Invocation::Run(options) => options,
            _ => panic!("expected command"),
        }
    }

    #[test]
    fn redirects_require_explicit_http_authority_without_repair() {
        for uri in [
            "http:example.com",
            "https:/example.com",
            "https:\\example.com",
            "https:///example.com",
            "https://",
            "/callback",
            "ftp://example.com",
        ] {
            assert!(!valid_redirect(uri), "{uri}");
        }
        for uri in [
            "https://example.com/Callback",
            "https://example.com/callback/",
            "HTTP://localhost:5173",
            "https://example.com/#fragment",
        ] {
            assert!(valid_redirect(uri), "{uri}");
        }
    }
    #[test]
    fn scope_boundaries_and_consent_contradictions() {
        for name in ["", ".read", "a b", "é", &"a".repeat(121)] {
            assert!(resolve_scope(&options(&["--scope-name", name]), "App").is_err());
        }
        let spec = resolve_scope(&options(&["--scope-name", &"a".repeat(120)]), "App").unwrap();
        assert_eq!(spec.name.len(), 120);
        assert!(
            build_scope("id", &spec)
                .get("userConsentDescription")
                .is_none()
        );
        for args in [
            vec!["--scope-name", "read", "--scope-user-display-name", "user"],
            vec!["--scope-name", "read", "--scope-display-name", " "],
            vec!["--scope-name", "read", "--scope-consent", ""],
            vec!["--scope-name", "read", "--scope-state", ""],
            vec![
                "--scope-name",
                "read",
                "--scope-consent",
                "users",
                "--scope-user-description",
                "",
            ],
        ] {
            assert!(resolve_scope(&options(&args), "App").is_err());
        }
        let spec = resolve_scope(
            &options(&[
                "--scope-name",
                " read ",
                "--scope-consent",
                " USERS ",
                "--scope-state",
                "disabled",
            ]),
            "My App",
        )
        .unwrap();
        let scope = build_scope("id", &spec);
        assert_eq!(scope["value"], "read");
        assert_eq!(scope["type"], "User");
        assert_eq!(scope["isEnabled"], false);
        assert_eq!(
            scope["adminConsentDescription"],
            "Allow the application to access My App on behalf of the signed-in user."
        );
        assert_eq!(
            scope["userConsentDescription"],
            "Allow the application to access My App on your behalf."
        );
    }

    #[test]
    fn lifetime_uses_frozen_utc_clock_and_inclusive_custom_end() {
        let now = DateTime::parse_from_rfc3339("2028-02-28T16:30:45Z")
            .unwrap()
            .with_timezone(&Utc);
        for days in [90, 180, 365, 545, 730] {
            let options = options(&["--create-secret", "--secret-expiry", &days.to_string()]);
            let life = resolve_lifetime(&options, now).unwrap().unwrap();
            assert_eq!(life.end - now, Duration::days(days));
            assert!(life.start.is_none());
        }
        let life = resolve_lifetime(&options(&["--create-secret"]), now)
            .unwrap()
            .unwrap();
        assert_eq!(life.end - now, Duration::days(180));
        let life = resolve_lifetime(
            &options(&[
                "--create-secret",
                "--secret-start",
                "29/2/2028",
                "--secret-end",
                "1/3/2028",
            ]),
            now,
        )
        .unwrap()
        .unwrap();
        assert_eq!(
            life.start.unwrap().to_rfc3339(),
            "2028-02-29T00:00:00+00:00"
        );
        assert_eq!(life.end.to_rfc3339(), "2028-03-01T23:59:59+00:00");
        let life = resolve_lifetime(
            &options(&[
                "--create-secret",
                "--secret-start",
                "28/02/2028",
                "--secret-end",
                "29/02/2028",
            ]),
            now,
        )
        .unwrap()
        .unwrap();
        assert!(life.start.is_none());
        for args in [
            vec!["--secret-expiry", "90"],
            vec!["--create-secret", "--secret-expiry", "custom"],
            vec![
                "--create-secret",
                "--secret-expiry",
                "90",
                "--secret-end",
                "1/3/2028",
            ],
            vec![
                "--create-secret",
                "--secret-start",
                "27/2/2028",
                "--secret-end",
                "1/3/2028",
            ],
            vec!["--create-secret", "--secret-end", "28/2/2028"],
            vec![
                "--create-secret",
                "--secret-start",
                "1/3/2028",
                "--secret-end",
                "29/2/2028",
            ],
            vec!["--create-secret", "--secret-end", "29/2/2029"],
        ] {
            assert!(resolve_lifetime(&options(&args), now).is_err());
        }
        for date in [
            "1/1/0000",
            "1/1/10000",
            "001/1/2028",
            "+1/1/2028",
            "1/1/28",
            "31/4/2028",
            "1/1/2028/",
        ] {
            assert!(parse_date(date).is_none(), "{date}");
        }
        assert!(parse_date(" 1/1/0001 ").is_some());
        assert!(parse_date("31/12/9999").is_some());
    }

    #[test]
    fn description_never_splits_astral_scalar() {
        let name = format!("{}\u{1F600}", "a".repeat(127));
        let description = secret_description(&name);
        assert_eq!(description, "a".repeat(127));
        let name = format!("{}\u{1F600}", "a".repeat(126));
        assert_eq!(secret_description(&name), name);
        assert_eq!(secret_description("App"), "App secret");
    }

    #[test]
    fn create_workflows_preserve_ids_and_secret_recovery() {
        for scenario in ["success", "patch-failure"] {
            let directory = tempfile::tempdir().unwrap();
            let output = std::process::Command::new(std::env::current_exe().unwrap())
                .args([
                    "--exact",
                    "app::tests::create_workflow_child",
                    "--nocapture",
                ])
                .env("ENTRA_CREATE_TEST_SCENARIO", scenario)
                .current_dir(directory.path())
                .output()
                .unwrap();
            assert!(
                output.status.success(),
                "{}",
                String::from_utf8_lossy(&output.stderr)
            );
            let stdout = String::from_utf8(output.stdout)
                .unwrap()
                .replace("\r\n", "\n");
            assert!(stdout.contains("AUTH_APP_ID=object\nAUTH_CLIENT_ID=client"));
            assert!(!stdout.contains("fake-one-time-secret"));
            if scenario == "success" {
                let report = std::fs::read_to_string(directory.path().join("App.txt"))
                    .unwrap()
                    .replace("\r\n", "\n");
                assert!(report.contains("Secret value:            fake-one-time-secret"));
                assert!(report.contains("Directory (tenant) ID:   tenant"));
                assert!(report.contains("https://example.com/Callback\n                         https://example.com/callback"));
            } else {
                assert_eq!(std::fs::read_dir(directory.path()).unwrap().count(), 0);
            }
        }
    }

    #[tokio::test]
    async fn create_workflow_child() {
        let Ok(scenario) = std::env::var("ENTRA_CREATE_TEST_SCENARIO") else {
            return;
        };
        let mut responses = vec![(201, r#"{"id":"object","appId":"client"}"#.into(), vec![])];
        if scenario == "patch-failure" {
            responses.push((403, "scope-denied".into(), vec![]));
        } else {
            responses.push((204, String::new(), vec![]));
            responses.push((
                200,
                r#"{"keyId":"key","secretText":"fake-one-time-secret"}"#.into(),
                vec![],
            ));
        }
        let fixture = Fixture::new(responses);
        let result = create(options(&["--name", "App", "--redirect-urls", "https://example.com/Callback,https://example.com/callback,https://example.com/Callback",
            "--audience", "PersonalMicrosoftAccount", "--scope-name", "read", "--scope-consent", "users", "--create-secret", "--secret-expiry", "90"]),
            &fixture.client(), Some("tenant")).await;
        let requests = fixture.take_requests();
        assert!(requests[0].starts_with("POST /v1.0/applications "));
        assert!(requests[1].starts_with("PATCH /v1.0/applications/object "));
        let body = |index: usize| -> Value {
            serde_json::from_str(requests[index].split_once("\r\n\r\n").unwrap().1).unwrap()
        };
        assert_eq!(
            body(0)["spa"]["redirectUris"],
            json!([
                "https://example.com/Callback",
                "https://example.com/callback"
            ])
        );
        assert_eq!(body(0)["api"]["requestedAccessTokenVersion"], 2);
        assert_eq!(body(1)["api"]["requestedAccessTokenVersion"], 2);
        assert_eq!(body(1)["api"]["oauth2PermissionScopes"][0]["type"], "User");
        if scenario == "patch-failure" {
            assert!(result.unwrap_err().to_string().contains("scope-denied"));
            assert_eq!(requests.len(), 2);
        } else {
            result.unwrap();
            assert_eq!(requests.len(), 3);
            assert!(requests[2].starts_with("POST /v1.0/applications/object/addPassword "));
            assert!(body(2)["passwordCredential"].get("startDateTime").is_none());
            let report = std::fs::read_to_string("App.txt").unwrap();
            let scope_id = body(1)["api"]["oauth2PermissionScopes"][0]["id"]
                .as_str()
                .unwrap()
                .to_owned();
            assert!(Uuid::parse_str(&scope_id).is_ok());
            assert!(report.contains(&format!("Scope ID:                {scope_id}")));
            assert!(report.contains("Secret starts:           now"));
        }
    }

    fn response(body: Value) -> (u16, String, Vec<(String, String)>) {
        (200, body.to_string(), vec![])
    }

    #[tokio::test]
    async fn expose_preserves_existing_api_and_identifier_uris() {
        let old_scope = json!({"id":"old", "value":"existing", "unknown":"retained"});
        let api = json!({"oauth2PermissionScopes":[old_scope], "requestedAccessTokenVersion":2,
            "preAuthorizedApplications":[{"appId":"pre", "delegatedPermissionIds":["old"]}],
            "unknown":{"retain":true}});
        let fixture = Fixture::new(vec![
            response(
                json!({"id":"object", "appId":"client", "displayName":"Original",
                "identifierUris":["api://first/", "api://second"], "api":api}),
            ),
            (204, String::new(), vec![]),
        ]);
        expose_api(
            options(&["expose-api", "--appid", "object", "--scope-name", "read"]),
            &fixture.client(),
        )
        .await
        .unwrap();
        let requests = fixture.take_requests();
        assert_eq!(requests.len(), 2);
        assert!(requests[1].starts_with("PATCH /v1.0/applications/object "));
        let mut patch: Value =
            serde_json::from_str(requests[1].split_once("\r\n\r\n").unwrap().1).unwrap();
        assert!(patch.get("identifierUris").is_none());
        let scopes = patch["api"]["oauth2PermissionScopes"]
            .as_array_mut()
            .unwrap();
        let new_scope = scopes.pop().unwrap();
        assert_eq!(new_scope["value"], "read");
        assert_eq!(new_scope["adminConsentDisplayName"], "Access Original");
        assert_eq!(patch["api"], api);
    }

    #[tokio::test]
    async fn expose_duplicate_or_malformed_api_never_patches() {
        for api in [
            json!({"oauth2PermissionScopes":[{"value":"READ"}]}),
            json!("not an object"),
            json!({"oauth2PermissionScopes":{}}),
            json!({"oauth2PermissionScopes":[{"value":123}]}),
        ] {
            let fixture = Fixture::new(vec![response(
                json!({"id":"object", "appId":"client", "api":api}),
            )]);
            let result = expose_api(
                options(&["expose-api", "--appid", "object", "--scope-name", "read"]),
                &fixture.client(),
            )
            .await;
            assert!(matches!(result, Err(AppError::Runtime(_))));
            let requests = fixture.take_requests();
            assert_eq!(requests.len(), 1);
            assert!(requests[0].starts_with("GET "));
        }
    }

    #[tokio::test]
    async fn expose_missing_uri_uses_client_id_and_null_name_uses_reference() {
        let fixture = Fixture::new(vec![
            response(
                json!({"id":"object", "appId":"client", "displayName":null, "api":null, "identifierUris":[]}),
            ),
            (204, String::new(), vec![]),
        ]);
        expose_api(
            options(&["expose-api", "--appid", "reference", "--scope-name", "read"]),
            &fixture.client(),
        )
        .await
        .unwrap();
        let requests = fixture.take_requests();
        let patch: Value =
            serde_json::from_str(requests[1].split_once("\r\n\r\n").unwrap().1).unwrap();
        assert_eq!(patch["identifierUris"], json!(["api://client"]));
        assert_eq!(
            patch["api"]["oauth2PermissionScopes"][0]["adminConsentDisplayName"],
            "Access reference"
        );
    }

    #[tokio::test]
    async fn list_pages_filter_sort_stably_and_limit_before_sorting() {
        let fixture = Fixture::with_responses(|base| {
            vec![
                response(
                    json!({"value":[{"displayName":"Zulu", "appId":"c1", "id":"o1"}], "@odata.nextLink":format!("{base}/next")}),
                ),
                response(json!({"value":[
                {"displayName":"alpha", "appId":"c2", "id":"o2"},
                {"displayName":"ALPHA", "appId":"c3", "id":"o3"},
                {"displayName":"Aardvark", "appId":"c4", "id":"o4"}]})),
            ]
        });
        let (apps, more) = collect_applications(
            &options(&["list", "--name", " O'Brien ", "--top", "3"]),
            &fixture.client(),
        )
        .await
        .unwrap();
        assert!(more);
        assert_eq!(
            apps.iter().map(|app| app.id.as_str()).collect::<Vec<_>>(),
            ["o2", "o3", "o1"]
        );
        let requests = fixture.take_requests();
        assert_eq!(requests.len(), 2);
        let path = requests[0].split_whitespace().nth(1).unwrap();
        let url = url::Url::parse(&format!("http://fixture{path}")).unwrap();
        let query: std::collections::HashMap<_, _> = url.query_pairs().collect();
        assert_eq!(
            query.get("$filter").unwrap(),
            "startswith(displayName,'O''Brien')"
        );
        assert_eq!(query.get("$top").unwrap(), "3");
        assert!(requests[1].starts_with("GET /v1.0/next "));
    }

    #[tokio::test]
    async fn list_counts_next_link_truncation_and_accepts_null_names() {
        let fixture = Fixture::with_responses(|base| {
            vec![response(
                json!({"value":[{"displayName":null, "appId":"client", "id":"object"}],
                "@odata.nextLink":format!("{base}/next")}),
            )]
        });
        let (apps, more) =
            collect_applications(&options(&["list", "--top", "1"]), &fixture.client())
                .await
                .unwrap();
        assert!(more);
        assert_eq!(
            serde_json::to_value(&apps).unwrap(),
            json!([{"displayName":"", "appId":"client", "id":"object"}])
        );
        assert_eq!(fixture.take_requests().len(), 1);
    }

    #[tokio::test]
    async fn list_rejects_self_links_and_two_page_cycles() {
        for cycle in [false, true] {
            let fixture = Fixture::with_responses(|base| {
                let initial = format!("{base}/applications?$select=id,appId,displayName&$top=50");
                if cycle {
                    vec![
                        response(json!({"value":[], "@odata.nextLink":format!("{base}/next")})),
                        response(json!({"value":[], "@odata.nextLink":initial})),
                    ]
                } else {
                    vec![response(json!({"value":[], "@odata.nextLink":initial}))]
                }
            });
            let error = collect_applications(&options(&["list"]), &fixture.client())
                .await
                .err()
                .unwrap();
            assert!(error.to_string().contains("repeated next-page link"));
            assert_eq!(fixture.take_requests().len(), if cycle { 2 } else { 1 });
        }
    }

    #[tokio::test]
    async fn list_validates_next_links_even_after_reaching_limit() {
        for credentials in [false, true] {
            let fixture = Fixture::with_responses(|base| {
                let link = if credentials {
                    base.as_str().replace("http://", "http://user:password@")
                } else {
                    "https://example.invalid/steal".into()
                };
                vec![response(
                    json!({"value":[{"appId":"client", "id":"object"}], "@odata.nextLink":link}),
                )]
            });
            assert!(
                collect_applications(&options(&["list", "--top", "1"]), &fixture.client())
                    .await
                    .is_err()
            );
            assert_eq!(fixture.take_requests().len(), 1);
        }
    }

    #[tokio::test]
    async fn list_rejects_malformed_pages_and_consumed_records() {
        for page in [
            json!([]),
            json!({}),
            json!({"value":{}}),
            json!({"value":[null]}),
            json!({"value":[{"id":"object"}]}),
            json!({"value":[{"id":42,"appId":"client"}]}),
            json!({"value":[{"id":"object","appId":"client","displayName":false}]}),
        ] {
            let fixture = Fixture::new(vec![response(page)]);
            assert!(
                collect_applications(&options(&["list"]), &fixture.client())
                    .await
                    .is_err()
            );
            assert_eq!(fixture.take_requests().len(), 1);
        }
    }
}
