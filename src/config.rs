//! Where clipsync keeps the relay address, the shared secret, and this
//! computer's device id.

use std::fmt;
use std::io::Write;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD as B64;
use serde::{Deserialize, Serialize};

#[derive(Clone, PartialEq, Serialize, Deserialize)]
pub struct Config {
    /// The relay's address, like `https://clip.example.com`.
    pub relay: String,
    /// The secret all your devices share (see [`crate::seal`]).
    pub secret: String,
    /// This computer's id, so the relay doesn't send its own clips back.
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

/// The home folder, else `/`.
pub fn home() -> PathBuf {
    dirs::home_dir().unwrap_or_else(|| "/".into())
}

/// clipsync's folder: `~/Library/Application Support/clipsync` on a Mac,
/// `%LOCALAPPDATA%\clipsync` on Windows. Not the roaming `%APPDATA%`: a
/// roaming profile would copy the device id to the user's other PCs.
pub fn dir() -> PathBuf {
    dirs::data_local_dir().unwrap_or_else(home).join("clipsync")
}

/// The config file's usual place.
pub fn path() -> PathBuf {
    dir().join("config.json")
}

/// `path` with the home folder shown as `~`.
pub fn short_path(path: &Path) -> String {
    match path.strip_prefix(home()) {
        Ok(rest) => Path::new("~").join(rest).display().to_string(),
        Err(_) => path.display().to_string(),
    }
}

/// A new random device id for this computer.
pub fn new_device_id() -> Result<String> {
    let os = if cfg!(windows) { "win" } else { "mac" };
    Ok(format!("{os}-{}", B64.encode(crate::seal::random::<9>()?)))
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
            let mut options = std::fs::OpenOptions::new();
            options.write(true).create(true).truncate(true);
            // On Windows, the folder's permissions already keep it to this user.
            #[cfg(unix)]
            std::os::unix::fs::OpenOptionsExt::mode(&mut options, 0o600);
            let mut file = options.open(path)?;
            file.write_all(&serde_json::to_vec_pretty(self)?)
        };
        write().with_context(|| format!("writing {}", path.display()))
    }
}

#[cfg(test)]
mod tests {
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
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = std::fs::metadata(&path).unwrap().permissions().mode();
            assert_eq!(mode & 0o777, 0o600);
        }
        assert!(!format!("{config:?}").contains("secret"));
    }
}
