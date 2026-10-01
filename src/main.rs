mod app;
mod auth;
mod cli;
mod graph;
mod report;

use cli::{HelpTopic, Invocation};
use std::{io::IsTerminal, process::ExitCode};

#[tokio::main(flavor = "current_thread")]
async fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let options = match cli::parse(&args) {
        Ok(Invocation::Help(topic)) => {
            cli::show_help(topic);
            return ExitCode::SUCCESS;
        }
        Ok(Invocation::Version) => {
            println!("entra-appreg {}", env!("CARGO_PKG_VERSION"));
            return ExitCode::SUCCESS;
        }
        Ok(Invocation::NoArguments) => {
            eprintln!("No arguments given. Here is how to use this program:\n");
            cli::show_help(HelpTopic::General);
            return ExitCode::from(2);
        }
        Ok(Invocation::Run(options)) => options,
        Err(error) => {
            eprintln!("{error}");
            return ExitCode::from(2);
        }
    };
    if options.command == cli::Command::Delete
        && !(std::io::stdin().is_terminal() && std::io::stderr().is_terminal())
    {
        eprintln!(
            "delete requires terminal input and terminal stderr for confirmation; nothing was deleted."
        );
        return ExitCode::from(2);
    }
    let auth = match auth::authenticate(options.command).await {
        Ok(auth) => auth,
        Err(error) => {
            eprintln!("Could not get an access token to call Microsoft Graph.");
            eprintln!(
                "Install Azure CLI 2.54.0 or newer and sign in with 'az login' before running this command."
            );
            eprintln!(
                "Ensure the signed-in account has access to the intended tenant and the required Microsoft Graph permissions."
            );
            eprintln!("Details: {error:#}");
            return ExitCode::from(1);
        }
    };
    let graph = match graph::GraphClient::new(&auth.access_token) {
        Ok(graph) => graph,
        Err(error) => {
            eprintln!("Error: {error:#}");
            return ExitCode::from(1);
        }
    };
    match app::run_command(options, &graph, auth.tenant_id.as_deref()).await {
        Ok(()) => ExitCode::SUCCESS,
        Err(app::AppError::Usage(error)) => {
            eprintln!("{error}");
            ExitCode::from(2)
        }
        Err(app::AppError::Runtime(error)) => {
            eprintln!("Error: {error:#}");
            ExitCode::from(1)
        }
    }
}
