//! `clipsync update`: replace this binary with the latest GitHub release,
//! if it's newer, and restart the background service on it.

use std::path::Path;
use std::process::Command;
use std::time::Duration;

use anyhow::{Context, Result, bail, ensure};
use ring::digest::{SHA256, digest};
use serde::Deserialize;

use crate::{VERSION, agent};

const REPO: &str = "iyazerski/clipsync";
/// The release asset with the binary; `install.sh` downloads the same one.
const ASSET: &str = "clipsync-aarch64-apple-darwin.tar.gz";

#[derive(Deserialize)]
struct Release {
    tag_name: String,
    assets: Vec<Asset>,
}

#[derive(Deserialize)]
struct Asset {
    name: String,
    browser_download_url: String,
}

impl Release {
    /// The download address of the asset called `name`.
    fn asset(&self, name: &str) -> Result<&str> {
        self.assets
            .iter()
            .find(|asset| asset.name == name)
            .map(|asset| asset.browser_download_url.as_str())
            .with_context(|| format!("release {} has no {name}", self.tag_name))
    }
}

/// Install the latest release over this binary if it's newer than [`VERSION`].
pub async fn run() -> Result<()> {
    let http = reqwest::Client::builder()
        // GitHub's API refuses requests without a user agent.
        .user_agent(format!("clipsync/{VERSION}"))
        .timeout(Duration::from_secs(60))
        .build()
        .context("setting up HTTP")?;
    let release: Release = serde_json::from_slice(
        &http
            .get(format!(
                "https://api.github.com/repos/{REPO}/releases/latest"
            ))
            .header(reqwest::header::ACCEPT, "application/vnd.github+json")
            .send()
            .await?
            .error_for_status()
            .context("asking GitHub for the latest release")?
            .bytes()
            .await?,
    )
    .context("reading the latest release")?;
    let latest = release.tag_name.trim_start_matches('v');
    if !newer(latest, VERSION) {
        println!("clipsync {VERSION} is up to date");
        return Ok(());
    }
    println!("downloading clipsync {latest}…");
    let archive = get(&http, release.asset(ASSET)?).await?.bytes().await?;
    let checksum = get(&http, release.asset(&format!("{ASSET}.sha256"))?)
        .await?
        .bytes()
        .await?;
    let expected = String::from_utf8_lossy(&checksum);
    let expected = expected.split_whitespace().next().unwrap_or_default();
    ensure!(
        hex(digest(&SHA256, &archive).as_ref()) == expected,
        "the download doesn't match its checksum; try again"
    );
    let exe = std::env::current_exe().context("finding the clipsync binary")?;
    replace(&exe, &archive)?;
    println!("updated clipsync {VERSION} → {latest}");
    if agent::restart()? {
        println!("restarted the background service");
    }
    Ok(())
}

/// A successful GET of `url`.
async fn get(http: &reqwest::Client, url: &str) -> Result<reqwest::Response> {
    Ok(http.get(url).send().await?.error_for_status()?)
}

/// Unpack the `clipsync` binary from `archive` next to `exe`, then move it
/// over `exe` in one step, so a failure never leaves a broken binary.
fn replace(exe: &Path, archive: &[u8]) -> Result<()> {
    let dir = exe.parent().context("the binary has no folder")?;
    // Next to the binary, so the rename stays on one file system; removed
    // when dropped.
    let staging = tempfile::Builder::new()
        .prefix(".clipsync-update")
        .tempdir_in(dir)
        .with_context(|| format!("creating a folder in {}", dir.display()))?;
    let tarball = staging.path().join(ASSET);
    std::fs::write(&tarball, archive).context("saving the download")?;
    let out = Command::new("/usr/bin/tar")
        .arg("-xzf")
        .arg(&tarball)
        .arg("-C")
        .arg(staging.path())
        .arg("clipsync")
        .output()
        .context("running tar")?;
    if !out.status.success() {
        bail!("unpacking: {}", String::from_utf8_lossy(&out.stderr).trim());
    }
    std::fs::rename(staging.path().join("clipsync"), exe)
        .with_context(|| format!("replacing {}", exe.display()))
}

/// Whether version `a` (like `0.1.12`) is newer than `b`, comparing the
/// numbers in order.
fn newer(a: &str, b: &str) -> bool {
    let parts = |v: &str| -> Vec<u64> { v.split('.').map(|p| p.parse().unwrap_or(0)).collect() };
    parts(a) > parts(b)
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn versions_compare_by_number() {
        assert!(newer("0.1.10", "0.1.9"));
        assert!(newer("0.2.0", "0.1.99"));
        assert!(!newer("0.1.2", "0.1.2"));
        assert!(!newer("0.1.1", "0.1.2"));
    }
}
