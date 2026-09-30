use std::ffi::OsStr;
use std::io;
use std::process::Output;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Result, anyhow, bail};
use azure_core::credentials::TokenCredential;
use azure_identity::{AzureCliCredential, AzureCliCredentialOptions, Executor};
use uuid::Uuid;

use crate::cli::Command;

const AUTH_TIMEOUT: Duration = Duration::from_secs(10);
const GRAPH_SCOPE: &str = "https://graph.microsoft.com/.default";

// Deliberately not Debug: access tokens are never diagnostics.
pub(crate) struct AuthContext {
    pub(crate) access_token: String,
    pub(crate) tenant_id: Option<String>,
}

// The SDK's default executor leaves its child running when its future is
// cancelled. Keep the SDK's commands unchanged, but terminate the directly
// spawned child when the authentication deadline cancels output collection.
#[derive(Debug)]
struct CliExecutor;

#[async_trait::async_trait]
impl Executor for CliExecutor {
    async fn run(&self, program: &OsStr, args: &[&OsStr]) -> io::Result<Output> {
        tokio::process::Command::new(program)
            .args(args)
            .kill_on_drop(true)
            .output()
            .await
    }
}

pub(crate) async fn authenticate(command: Command) -> Result<AuthContext> {
    authenticate_with(command, Arc::new(CliExecutor), AUTH_TIMEOUT).await
}

async fn current_tenant(executor: &dyn Executor) -> Result<String> {
    // No user-controlled values enter this command, including on Windows where
    // Azure CLI is normally installed as az.cmd.
    #[cfg(windows)]
    let (program, arguments) = (
        "cmd.exe",
        [
            "/d",
            "/c",
            "az",
            "account",
            "show",
            "--query",
            "tenantId",
            "--output",
            "tsv",
            "--only-show-errors",
        ],
    );
    #[cfg(not(windows))]
    let (program, arguments) = (
        "az",
        [
            "account",
            "show",
            "--query",
            "tenantId",
            "--output",
            "tsv",
            "--only-show-errors",
        ],
    );
    let args = arguments.map(OsStr::new);
    let output = executor
        .run(OsStr::new(program), &args)
        .await
        .map_err(|_| anyhow!("Could not run Azure CLI to discover the active tenant."))?;
    if !output.status.success() {
        bail!("Azure CLI could not discover the active tenant.");
    }
    let tenant = std::str::from_utf8(&output.stdout)
        .map_err(|_| anyhow!("Azure CLI returned an invalid tenant ID; expected a UUID."))?;
    Uuid::parse_str(tenant.trim())
        .map(|id| id.hyphenated().to_string())
        .map_err(|_| anyhow!("Azure CLI returned an invalid tenant ID; expected a UUID."))
}

// This private SDK executor seam allows isolated fixtures without changing PATH,
// credentials, or the active Azure CLI account in the test process.
async fn authenticate_with(
    command: Command,
    executor: Arc<dyn Executor>,
    deadline: Duration,
) -> Result<AuthContext> {
    tokio::time::timeout(deadline, async move {
        let tenant_id = if command == Command::Create {
            Some(current_tenant(executor.as_ref()).await?)
        } else {
            None
        };
        let credential = AzureCliCredential::new(Some(AzureCliCredentialOptions {
            tenant_id: tenant_id.clone(),
            executor: Some(executor),
            ..Default::default()
        }))
        .map_err(|_| anyhow!("Could not initialize Azure CLI authentication."))?;
        // The SDK handles token acquisition and expiry parsing. Its errors may
        // contain subprocess output, so replace rather than attach their chains.
        let token = credential
            .get_token(&[GRAPH_SCOPE], None)
            .await
            .map_err(|_| anyhow!("Azure CLI authentication failed; check your sign-in and use Azure CLI 2.54.0 or newer."))?;
        let access_token = token.token.secret();
        if access_token.trim().is_empty() {
            bail!("Azure CLI did not return a nonempty access token.");
        }
        Ok(AuthContext {
            access_token: access_token.to_owned(),
            tenant_id,
        })
    })
    .await
    .map_err(|_| anyhow!("Azure CLI authentication timed out."))?
}

#[cfg(test)]
mod tests {
    use super::*;
    use parking_lot::Mutex;
    use std::collections::VecDeque;
    use std::sync::atomic::{AtomicBool, Ordering};

    const TENANT: &str = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
    const OPAQUE_TOKEN: &str = "opaque-access-token-not-a-jwt";
    const PRIVATE_OUTPUT: &str = "private-cli-output-must-not-leak";

    #[cfg(unix)]
    fn status(success: bool) -> std::process::ExitStatus {
        use std::os::unix::process::ExitStatusExt;
        std::process::ExitStatus::from_raw(if success { 0 } else { 256 })
    }

    #[cfg(windows)]
    fn status(success: bool) -> std::process::ExitStatus {
        use std::os::windows::process::ExitStatusExt;
        std::process::ExitStatus::from_raw(if success { 0 } else { 1 })
    }

    fn output(success: bool, stdout: impl Into<Vec<u8>>) -> io::Result<Output> {
        Ok(Output {
            status: status(success),
            stdout: stdout.into(),
            stderr: PRIVATE_OUTPUT.as_bytes().to_vec(),
        })
    }

    fn token_body(token: &str) -> String {
        serde_json::json!({
            "accessToken": token, "tokenType": "Bearer", "expires_on": 4102444800_i64,
        })
        .to_string()
    }

    fn error_text(result: Result<AuthContext>) -> String {
        match result {
            Ok(_) => panic!("authentication unexpectedly succeeded"),
            Err(error) => format!("{error:#}"),
        }
    }

    #[derive(Debug)]
    struct Fixture {
        outputs: Mutex<VecDeque<io::Result<Output>>>,
        calls: Mutex<Vec<Vec<String>>>,
        pending_call: Option<usize>,
        cancelled: AtomicBool,
    }

    impl Fixture {
        fn new(outputs: Vec<io::Result<Output>>) -> Self {
            Self {
                outputs: Mutex::new(outputs.into()),
                calls: Mutex::new(Vec::new()),
                pending_call: None,
                cancelled: AtomicBool::new(false),
            }
        }
    }

    struct Cancellation<'a>(&'a AtomicBool);

    impl Drop for Cancellation<'_> {
        fn drop(&mut self) {
            self.0.store(true, Ordering::SeqCst);
        }
    }

    #[async_trait::async_trait]
    impl Executor for Fixture {
        async fn run(&self, program: &OsStr, args: &[&OsStr]) -> io::Result<Output> {
            let call = {
                let mut calls = self.calls.lock();
                calls.push(
                    std::iter::once(program)
                        .chain(args.iter().copied())
                        .map(|arg| arg.to_string_lossy().into_owned())
                        .collect(),
                );
                calls.len()
            };
            if self.pending_call == Some(call) {
                let _cancellation = Cancellation(&self.cancelled);
                std::future::pending::<()>().await;
            }
            self.outputs
                .lock()
                .pop_front()
                .expect("unexpected Azure CLI invocation")
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn create_discovers_and_pins_normalized_tenant_accepting_opaque_token() {
        let fixture = Arc::new(Fixture::new(vec![
            output(true, "  AAAAAAAABBBBCCCCDDDDEEEEEEEEEEEE\n"),
            output(true, token_body(OPAQUE_TOKEN)),
        ]));
        let context = authenticate_with(Command::Create, fixture.clone(), AUTH_TIMEOUT)
            .await
            .unwrap();
        assert_eq!(context.access_token, OPAQUE_TOKEN);
        assert_eq!(context.tenant_id.as_deref(), Some(TENANT));
        let calls = fixture.calls.lock();
        assert_eq!(calls.len(), 2);
        assert!(
            calls[0]
                .join(" ")
                .ends_with("az account show --query tenantId --output tsv --only-show-errors")
        );
        let token_command = calls[1].join(" ");
        assert!(token_command.contains(&format!("--scope {GRAPH_SCOPE}")));
        assert!(token_command.contains(&format!("--tenant {TENANT}")));
        assert!(!token_command.contains("--subscription"));
    }

    #[tokio::test(flavor = "current_thread")]
    async fn list_and_expose_use_active_cli_account_without_tenant_discovery() {
        for command in [Command::List, Command::ExposeApi] {
            let fixture = Arc::new(Fixture::new(vec![output(true, token_body(OPAQUE_TOKEN))]));
            let context = authenticate_with(command, fixture.clone(), AUTH_TIMEOUT)
                .await
                .unwrap();
            assert_eq!(context.access_token, OPAQUE_TOKEN);
            assert_eq!(context.tenant_id, None);
            let calls = fixture.calls.lock();
            assert_eq!(calls.len(), 1);
            let token_command = calls[0].join(" ");
            assert!(token_command.contains("account get-access-token"));
            assert!(token_command.contains(&format!("--scope {GRAPH_SCOPE}")));
            assert!(!token_command.contains("--tenant"));
            assert!(!token_command.contains("--subscription"));
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn invalid_tenant_stops_before_token_request_without_disclosing_output() {
        for stdout in [
            Vec::new(),
            b" \t\n".to_vec(),
            PRIVATE_OUTPUT.as_bytes().to_vec(),
            b"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee; echo unsafe".to_vec(),
            vec![0xff],
        ] {
            let fixture = Arc::new(Fixture::new(vec![output(true, stdout)]));
            let error =
                error_text(authenticate_with(Command::Create, fixture.clone(), AUTH_TIMEOUT).await);
            assert!(error.contains("UUID"));
            assert!(!error.contains(PRIVATE_OUTPUT));
            assert!(!error.contains("unsafe"));
            assert_eq!(fixture.calls.lock().len(), 1);
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn malformed_tokens_and_missing_sdk_expiry_fail_without_disclosure() {
        for body in [
            format!("malformed {PRIVATE_OUTPUT} {OPAQUE_TOKEN}"),
            serde_json::json!({"tokenType": "Bearer", "expires_on": 4102444800_i64}).to_string(),
            serde_json::json!({"accessToken": OPAQUE_TOKEN, "tokenType": "Bearer", "expiresOn": "2100-01-01 00:00:00.000000"}).to_string(),
            serde_json::json!({"accessToken": OPAQUE_TOKEN, "tokenType": "Bearer", "expires_on": "4102444800"}).to_string(),
            serde_json::json!({"accessToken": OPAQUE_TOKEN, "tokenType": "Bearer", "expires_on": i64::MAX}).to_string(),
            serde_json::json!({"accessToken": OPAQUE_TOKEN, "expires_on": 4102444800_i64}).to_string(),
            token_body(""),
            token_body(" \t\n"),
        ] {
            let fixture = Arc::new(Fixture::new(vec![output(true, body)]));
            let error = error_text(
                authenticate_with(Command::List, fixture.clone(), AUTH_TIMEOUT).await,
            );
            assert!(!error.contains(PRIVATE_OUTPUT));
            assert!(!error.contains(OPAQUE_TOKEN));
            assert_eq!(fixture.calls.lock().len(), 1);
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn process_failure_never_discloses_stderr_stdout_or_io_error_details() {
        for command in [Command::Create, Command::List] {
            for response in [
                output(false, token_body(OPAQUE_TOKEN)),
                Err(io::Error::new(io::ErrorKind::NotFound, PRIVATE_OUTPUT)),
                Err(io::Error::other(PRIVATE_OUTPUT)),
            ] {
                let fixture = Arc::new(Fixture::new(vec![response]));
                let error =
                    error_text(authenticate_with(command, fixture.clone(), AUTH_TIMEOUT).await);
                assert!(error.contains("Azure CLI"));
                assert!(!error.contains(PRIVATE_OUTPUT));
                assert!(!error.contains(OPAQUE_TOKEN));
                assert_eq!(fixture.calls.lock().len(), 1);
            }
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn deadline_cancels_tenant_discovery_and_sdk_token_collection() {
        for (command, pending_call, outputs) in [
            (Command::Create, 1, vec![]),
            (Command::Create, 2, vec![output(true, TENANT)]),
            (Command::List, 1, vec![]),
        ] {
            let mut fixture = Fixture::new(outputs);
            fixture.pending_call = Some(pending_call);
            let fixture = Arc::new(fixture);
            let error = error_text(
                authenticate_with(command, fixture.clone(), Duration::from_millis(10)).await,
            );
            assert!(error.contains("timed out"));
            assert_eq!(fixture.calls.lock().len(), pending_call);
            assert!(fixture.cancelled.load(Ordering::SeqCst));
        }
    }

    #[cfg(unix)]
    #[tokio::test(flavor = "current_thread")]
    async fn executor_drains_both_pipes_without_deadlock() {
        let output = tokio::time::timeout(
            Duration::from_secs(5),
            CliExecutor.run(
                OsStr::new("/bin/sh"),
                &[
                    OsStr::new("-c"),
                    OsStr::new("i=0; while [ $i -lt 4096 ]; do printf 'stdout-0123456789\\n'; printf 'stderr-0123456789\\n' >&2; i=$((i+1)); done"),
                ],
            ),
        )
        .await
        .unwrap()
        .unwrap();
        assert!(output.status.success());
        assert_eq!(output.stdout, b"stdout-0123456789\n".repeat(4096));
        assert_eq!(output.stderr, b"stderr-0123456789\n".repeat(4096));
    }
}
