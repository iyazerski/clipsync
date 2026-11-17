//! The background service on Windows. A Run key starts `clipsyncw.exe
//! service` at login, which keeps `clipsyncw.exe run` going, restarts it
//! after a crash, and logs to a file.
//!
//! clipsyncw.exe is clipsync.exe built without a console, so nothing opens
//! a window. Task Scheduler would restart it by itself, but a task that
//! starts at login needs an administrator to create it.

use std::fs::OpenOptions;
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use windows_registry::{CURRENT_USER, Key};

const RUN_KEY: &str = r"Software\Microsoft\Windows\CurrentVersion\Run";
const VALUE: &str = "clipsync";
const SERVICE: &str = "clipsyncw.exe";
/// How long to wait before restarting `clipsync run` after a crash.
const THROTTLE: Duration = Duration::from_secs(30);

fn log_path() -> PathBuf {
    crate::config::dir().join("clipsync.log")
}

/// clipsyncw.exe, which the installer puts next to clipsync.exe.
fn service_exe() -> Result<PathBuf> {
    let service = std::env::current_exe()
        .context("finding the clipsync binary")?
        .with_file_name(SERVICE);
    ensure!(
        service.exists(),
        "{} is missing: install clipsync again",
        service.display()
    );
    Ok(service)
}

fn run_key() -> Result<Key> {
    CURRENT_USER
        .create(RUN_KEY)
        .context("opening the Run registry key")
}

fn installed() -> bool {
    CURRENT_USER
        .open(RUN_KEY)
        .and_then(|key| key.get_string(VALUE))
        .is_ok()
}

/// Install the service to run `clipsync run` at login, replacing an
/// earlier one, and start it. Returns the log path.
pub fn install() -> Result<PathBuf> {
    let service = service_exe()?;
    run_key()?
        .set_string(VALUE, format!("\"{}\" service", service.display()))
        .context("adding clipsync to the programs that start at login")?;
    stop();
    start()?;
    Ok(log_path())
}

/// Restart the service on the current binary, if it's installed. Returns
/// whether it was.
pub fn restart() -> Result<bool> {
    if !installed() {
        return Ok(false);
    }
    stop();
    start()?;
    Ok(true)
}

/// Stop and remove the service. Returns whether one was installed.
pub fn uninstall() -> Result<bool> {
    if !installed() {
        return Ok(false);
    }
    stop();
    run_key()?
        .remove_value(VALUE)
        .context("removing clipsync from the programs that start at login")?;
    Ok(true)
}

/// Keep `clipsync run` going until it exits cleanly, with its output in the
/// log. What the Run key starts.
pub fn serve() -> Result<()> {
    let exe = std::env::current_exe().context("finding the clipsync binary")?;
    let path = log_path();
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir).with_context(|| format!("creating {}", dir.display()))?;
    }
    loop {
        let log = OpenOptions::new()
            .create(true)
            .append(true)
            .open(&path)
            .with_context(|| format!("opening {}", path.display()))?;
        let status = Command::new(&exe)
            .arg("run")
            .stdin(Stdio::null())
            .stdout(log.try_clone()?)
            .stderr(log)
            .status()
            .context("starting clipsync run")?;
        if status.success() {
            return Ok(());
        }
        std::thread::sleep(THROTTLE);
    }
}

/// Start the service apart from this process, which it outlives.
fn start() -> Result<()> {
    Command::new(service_exe()?)
        .arg("service")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .context("starting the service")?;
    Ok(())
}

/// End the service and the `clipsync run` it started. None running is fine.
fn stop() {
    let _ = Command::new("taskkill")
        .args(["/F", "/T", "/IM", SERVICE])
        .output();
}
