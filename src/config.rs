//! Where clipsync keeps the relay address, the shared secret, and this
//! Mac's device id.

use std::fmt;
use std::io::Write;
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD as B64;
use ring::rand::{SecureRandom, SystemRandom};
use serde::{Deserialize, Serialize};

#[derive(Clone, PartialEq, Serialize, Deserialize)]
pub struct Config {
    /// The relay's address, like `https://clip.example.com`.
    pub relay: String,
    /// The secret all your devices share (see [`crate::seal`]).
    pub secret: String,
    /// This Mac's id, so the relay doesn't send its own clips back.
    pub device: String,
}

// By hand, so logs never show the secret.
impl fmt::Debug for Config {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Config")
            .field("relay", &self.relay)
            .field("device", &self.device)
            .finish_non_exhaustive()
    }
}

/// The home folder: `$HOME`, else `/`.
pub fn home() -> PathBuf {
    std::env::var_os("HOME")
        .map(PathBuf::from)
        .unwrap_or_else(|| "/".into())
}

/// The config file's usual place.
pub fn path() -> PathBuf {
    home().join("Library/Application Support/clipsync/config.json")
}

/// `path` with the home folder shown as `~`.
pub fn short_path(path: &Path) -> String {
    match path.strip_prefix(home()) {
        Ok(rest) => format!("~/{}", rest.display()),
        Err(_) => path.display().to_string(),
    }
}

/// A new random device id for this Mac.
pub fn new_device_id() -> Result<String> {
    let mut id = [0; 9];
    SystemRandom::new()
        .fill(&mut id)
        .map_err(|_| anyhow::anyhow!("no randomness"))?;
    Ok(format!("mac-{}", B64.encode(id)))
}

impl Config {
    /// Read the config at `path`, if there is one.
    pub fn load(path: &Path) -> Result<Option<Self>> {
        match std::fs::read(path) {
            Ok(bytes) => serde_json::from_slice(&bytes)
                .map(Some)
                .with_context(|| format!("reading {}", path.display())),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(e).with_context(|| format!("reading {}", path.display())),
        }
    }

    /// Write the config to `path`, readable only by this user.
    pub fn save(&self, path: &Path) -> Result<()> {
        if let Some(dir) = path.parent() {
            std::fs::create_dir_all(dir).with_context(|| format!("creating {}", dir.display()))?;
        }
        let write = || -> std::io::Result<()> {
            let mut file = std::fs::OpenOptions::new()
                .write(true)
                .create(true)
                .truncate(true)
                .mode(0o600)
                .open(path)?;
            file.write_all(&serde_json::to_vec_pretty(self)?)
        };
        write().with_context(|| format!("writing {}", path.display()))
    }
}

#[cfg(test)]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use super::*;

    #[test]
    fn a_saved_config_loads_back_private() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("sub/config.json");
        assert_eq!(Config::load(&path).unwrap(), None);
        let config = Config {
            relay: "https://relay".into(),
            secret: "s".into(),
            device: new_device_id().unwrap(),
        };
        config.save(&path).unwrap();
        assert_eq!(Config::load(&path).unwrap(), Some(config.clone()));
        let mode = std::fs::metadata(&path).unwrap().permissions().mode();
        assert_eq!(mode & 0o777, 0o600);
        assert!(!format!("{config:?}").contains("secret"));
    }
}
