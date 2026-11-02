//! The launch agent that keeps `clipsync run` going in the background: it
//! starts at login, restarts after a crash, and logs to a file.

use std::path::PathBuf;
use std::process::Command;

use anyhow::{Context, Result, bail};

const LABEL: &str = "dev.iyazerski.clipsync";

fn plist_path() -> PathBuf {
    crate::config::home()
        .join("Library/LaunchAgents")
        .join(format!("{LABEL}.plist"))
}

fn log_path() -> PathBuf {
    crate::config::home().join("Library/Logs/clipsync.log")
}

/// The launchd domain of the logged-in user's GUI session: the pasteboard
/// lives there.
fn domain() -> String {
    // SAFETY: getuid has no preconditions and can't fail.
    format!("gui/{}", unsafe { libc::getuid() })
}

/// Install the agent to run this binary with `args`, replacing an earlier
/// one, and start it. Returns the log path.
pub fn install(args: &[&str]) -> Result<PathBuf> {
    let exe = std::env::current_exe().context("finding the clipsync binary")?;
    let (plist, log) = (plist_path(), log_path());
    for dir in [plist.parent(), log.parent()].into_iter().flatten() {
        std::fs::create_dir_all(dir).with_context(|| format!("creating {}", dir.display()))?;
    }
    let program = std::iter::once(exe.to_string_lossy().into_owned())
        .chain(args.iter().map(|arg| (*arg).to_owned()))
        .map(|arg| format!("    <string>{}</string>\n", xml_escape(&arg)))
        .collect::<String>();
    let log = xml_escape(&log.to_string_lossy());
    // KeepAlive restarts it after a crash, but not after a clean exit, which
    // would only happen again.
    let body = format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{LABEL}</string>
  <key>ProgramArguments</key>
  <array>
{program}  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ThrottleInterval</key>
  <integer>30</integer>
  <key>StandardOutPath</key>
  <string>{log}</string>
  <key>StandardErrorPath</key>
  <string>{log}</string>
</dict>
</plist>
"#
    );
    // Stop the old one first: bootstrap fails while a job with the label is loaded.
    let _ = launchctl(&["bootout", &format!("{}/{LABEL}", domain())]);
    std::fs::write(&plist, body).with_context(|| format!("writing {}", plist.display()))?;
    launchctl(&["bootstrap", &domain(), &plist.to_string_lossy()])?;
    Ok(log_path())
}

/// Restart the agent on the current binary, if it's installed. Returns
/// whether it was.
pub fn restart() -> Result<bool> {
    if !plist_path().exists() {
        return Ok(false);
    }
    launchctl(&["kickstart", "-k", &format!("{}/{LABEL}", domain())])?;
    Ok(true)
}

/// Stop and remove the agent. Returns whether one was installed.
pub fn uninstall() -> Result<bool> {
    let plist = plist_path();
    if !plist.exists() {
        return Ok(false);
    }
    // Not loaded is fine.
    let _ = launchctl(&["bootout", &format!("{}/{LABEL}", domain())]);
    std::fs::remove_file(&plist).with_context(|| format!("removing {}", plist.display()))?;
    Ok(true)
}

fn launchctl(args: &[&str]) -> Result<()> {
    let out = Command::new("/bin/launchctl")
        .args(args)
        .output()
        .context("running launchctl")?;
    if !out.status.success() {
        bail!(
            "launchctl {}: {}",
            args.join(" "),
            String::from_utf8_lossy(&out.stderr).trim()
        );
    }
    Ok(())
}

fn xml_escape(text: &str) -> String {
    text.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
}
