//! clipsync: keep the clipboards of your Macs and an Android phone in sync
//! through a tiny relay.
//!
//! `setup` saves the relay and the shared secret and starts the background
//! service ([`agent`]), which runs [`sync`]. `pair` shows how to add the
//! phone and other Macs, and `update` installs the latest release.

mod agent;
mod config;
mod pasteboard;
mod seal;
mod sync;
mod update;

use anyhow::{Context, Result, bail};
use clap::{Parser, Subcommand};
use qrcode::QrCode;
use qrcode::render::unicode::Dense1x2;

use crate::config::Config;
use crate::seal::Keys;
use crate::sync::Check;

/// The release (CI sets it), else the crate version.
const VERSION: &str = match option_env!("CLIPSYNC_VERSION") {
    Some(version) => version,
    None => env!("CARGO_PKG_VERSION"),
};

#[derive(Parser)]
#[command(version = VERSION, about, long_about = None)]
struct Cli {
    #[command(subcommand)]
    command: Cmd,
}

#[derive(Subcommand)]
enum Cmd {
    /// Link this Mac to the relay and sync in the background from now on
    ///
    /// It starts at login, restarts after a crash, and logs to
    /// ~/Library/Logs/clipsync.log. On the first Mac, leave out --secret to
    /// make a new one; on the others, pass the one `clipsync pair` shows.
    Setup {
        /// The relay's address, like https://clip.example.com
        #[arg(long)]
        relay: String,
        /// The secret your devices share [default: the saved one, else a new one]
        #[arg(long)]
        secret: Option<String>,
    },
    /// Show how to add your phone (a QR code) and other Macs
    Pair,
    /// Print the relay token, for the relay's CLIPSYNC_TOKEN
    Token,
    /// Sync in the foreground (what the background service runs)
    Run,
    /// Stop syncing in the background (the settings stay)
    Uninstall,
    /// Install the latest release if it's newer, and restart the background service on it
    Update,
}

#[tokio::main]
async fn main() -> Result<()> {
    match Cli::parse().command {
        Cmd::Setup { relay, secret } => setup(relay, secret).await,
        Cmd::Pair => pair(),
        Cmd::Token => {
            println!("{}", Keys::derive(&saved()?.secret)?.token);
            Ok(())
        }
        Cmd::Run => sync::run(saved()?).await,
        Cmd::Update => update::run().await,
        Cmd::Uninstall => {
            if agent::uninstall()? {
                println!("stopped; clipsync no longer runs in the background");
            } else {
                println!("clipsync wasn't running in the background");
            }
            Ok(())
        }
    }
}

/// The saved config, or an error that says how to make one.
fn saved() -> Result<Config> {
    Config::load(&config::path())?
        .context("not set up yet: run `clipsync setup --relay <url>` first")
}

/// Save the relay and the secret, check them with the relay, and start the
/// background service.
async fn setup(relay: String, secret: Option<String>) -> Result<()> {
    let relay = relay.trim().trim_end_matches('/').to_owned();
    if !relay.starts_with("https://") && !relay.starts_with("http://") {
        bail!("the relay should be a URL, like https://clip.example.com");
    }
    let path = config::path();
    let old = Config::load(&path)?;
    let secret = match (secret, &old) {
        (Some(secret), _) => secret.trim().to_owned(),
        (None, Some(old)) => old.secret.clone(),
        (None, None) => seal::new_secret()?,
    };
    // Fails early on a mistyped secret.
    Keys::derive(&secret)?;
    let device = match old {
        Some(old) => old.device,
        None => config::new_device_id()?,
    };
    let config = Config {
        relay,
        secret,
        device,
    };
    config.save(&path)?;
    println!("saved to {}", config::short_path(&path));
    match sync::check(config.clone()).await {
        Ok(Check::Accepted) => println!("the relay accepts this secret"),
        Ok(Check::Refused) => println!(
            "the relay doesn't accept this secret yet: set its CLIPSYNC_TOKEN to the output of `clipsync token` (see the README); the service keeps trying"
        ),
        Err(e) => println!("couldn't reach the relay ({e:#}); the service keeps trying"),
    }
    let log = agent::install(&["run"])?;
    println!(
        "clipsync now runs in the background and starts at login; log: {}",
        config::short_path(&log)
    );
    println!("run `clipsync pair` to add your phone and other Macs");
    Ok(())
}

/// Print a QR code for the phone and the setup command for other Macs.
fn pair() -> Result<()> {
    let config = saved()?;
    // The secret goes in the fragment, which the browser never sends to
    // the relay: the page hands it straight to the app.
    let link = format!("{}/pair#{}", config.relay, config.secret);
    let qr = QrCode::new(link.as_bytes())
        .context("making the QR code")?
        .render::<Dense1x2>()
        // Light modules on a dark terminal: scanners read it either way.
        .dark_color(Dense1x2::Light)
        .light_color(Dense1x2::Dark)
        .build();
    println!("Phone: scan this with the camera and open the link, then tap \"Open clipsync\":\n");
    println!("{qr}\n");
    println!("Another Mac: install clipsync, then run");
    println!(
        "  clipsync setup --relay {} --secret {}\n",
        config.relay, config.secret
    );
    println!("Keep the secret private: anyone with it can read and change what you copy.");
    Ok(())
}
