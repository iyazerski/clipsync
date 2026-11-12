//! Sealing clips, so the relay passes them on without reading them.
//!
//! One secret, shared by your devices, gives two values: the relay token
//! (the relay knows it) and the sealing key (only the devices know it). Each
//! is an HMAC-SHA256 of a label, keyed with the secret, so the relay can't
//! work out the key from the token. A sealed clip is AES-256-GCM:
//! `version || nonce || ciphertext || tag`, in base64url without padding.
//! The Android app does the same in `Seal.kt`; the tests here and there
//! share their test vectors.

use anyhow::{Context, Result, bail, ensure};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD as B64;
use ring::aead::{AES_256_GCM, Aad, LessSafeKey, NONCE_LEN, Nonce, UnboundKey};
use ring::hmac;
use ring::rand::SystemRandom;

/// The secret's length in bytes.
const SECRET_LEN: usize = 32;
/// The first byte of a sealed clip; bumped if the format changes.
const VERSION: u8 = 1;
/// The version and the nonce, before the ciphertext.
const HEADER: usize = 1 + NONCE_LEN;

/// The token and the key that a secret gives.
pub struct Keys {
    pub token: String,
    key: LessSafeKey,
}

/// Make a new random secret, in base64url.
pub fn new_secret() -> Result<String> {
    Ok(B64.encode(random::<SECRET_LEN>()?))
}

/// `N` random bytes from the system.
pub fn random<const N: usize>() -> Result<[u8; N]> {
    // ring's error says nothing more than that it failed.
    let bytes =
        ring::rand::generate(&SystemRandom::new()).map_err(|_| anyhow::anyhow!("no randomness"))?;
    Ok(bytes.expose())
}

impl Keys {
    /// Derive the relay token and the sealing key from `secret`.
    pub fn derive(secret: &str) -> Result<Self> {
        let secret = B64
            .decode(secret.trim())
            .ok()
            .filter(|s| s.len() == SECRET_LEN)
            .context(
                "the secret should be 43 characters of base64url, as `clipsync pair` shows it",
            )?;
        let secret = hmac::Key::new(hmac::HMAC_SHA256, &secret);
        let token = B64.encode(hmac::sign(&secret, b"clipsync/token"));
        let key = UnboundKey::new(&AES_256_GCM, hmac::sign(&secret, b"clipsync/key").as_ref())
            .map_err(|_| anyhow::anyhow!("bad key length"))?;
        Ok(Self {
            token,
            key: LessSafeKey::new(key),
        })
    }

    /// Seal `text` with a fresh random nonce.
    pub fn seal(&self, text: &str) -> Result<String> {
        self.seal_with(random()?, text)
    }

    fn seal_with(&self, nonce: [u8; NONCE_LEN], text: &str) -> Result<String> {
        let tag_len = self.key.algorithm().tag_len();
        let mut sealed = Vec::with_capacity(HEADER + text.len() + tag_len);
        sealed.push(VERSION);
        sealed.extend_from_slice(&nonce);
        sealed.extend_from_slice(text.as_bytes());
        let tag = self
            .key
            .seal_in_place_separate_tag(
                Nonce::assume_unique_for_key(nonce),
                Aad::empty(),
                &mut sealed[HEADER..],
            )
            .map_err(|_| anyhow::anyhow!("sealing failed"))?;
        sealed.extend_from_slice(tag.as_ref());
        Ok(B64.encode(sealed))
    }

    /// Open a sealed clip; fails if it was sealed with another secret or changed.
    pub fn open(&self, sealed: &str) -> Result<String> {
        let mut sealed = B64.decode(sealed).context("not base64url")?;
        let Some(&version) = sealed.first() else {
            bail!("empty clip");
        };
        ensure!(
            version == VERSION,
            "clip format {version} is unknown; update clipsync"
        );
        ensure!(sealed.len() >= HEADER, "clip too short");
        let nonce = Nonce::try_assume_unique_for_key(&sealed[1..HEADER])
            .map_err(|_| anyhow::anyhow!("bad nonce"))?;
        // Decrypts in place and moves the text to the front of the buffer.
        let len = self
            .key
            .open_within(nonce, Aad::empty(), &mut sealed, HEADER..)
            .map_err(|_| anyhow::anyhow!("can't open the clip: the devices use different secrets"))?
            .len();
        sealed.truncate(len);
        String::from_utf8(sealed).context("the clip isn't UTF-8")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Shared with the Android app's SealTest: change both together.
    const SECRET: &str = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8";
    const TOKEN: &str = "eJMfuewcmawfRsgaj7Zutkow2oBVjbx8Thv_-3tJnYE";
    const SEALED: &str = "AQcHBwcHBwcHBwcHB9McJVAhssmL1dosNtILnU4lKTcuyGvlFloSsg";

    #[test]
    fn the_test_vectors_hold() {
        let keys = Keys::derive(SECRET).unwrap();
        let sealed = keys.seal_with([7; NONCE_LEN], "héllo 👋").unwrap();
        assert_eq!(keys.token, TOKEN);
        assert_eq!(sealed, SEALED);
        assert_eq!(keys.open(SEALED).unwrap(), "héllo 👋");
    }

    #[test]
    fn only_the_same_secret_opens_a_clip() {
        let keys = Keys::derive(&new_secret().unwrap()).unwrap();
        let sealed = keys.seal("text").unwrap();
        assert_ne!(sealed, keys.seal("text").unwrap(), "nonces must differ");
        assert_eq!(keys.open(&sealed).unwrap(), "text");
        let other = Keys::derive(&new_secret().unwrap()).unwrap();
        assert!(other.open(&sealed).is_err());
        let mut tampered = sealed.into_bytes();
        let last = tampered.len() - 2;
        tampered[last] = if tampered[last] == b'A' { b'B' } else { b'A' };
        assert!(keys.open(std::str::from_utf8(&tampered).unwrap()).is_err());
    }

    #[test]
    fn bad_secrets_are_refused() {
        assert!(Keys::derive("short").is_err());
        assert!(Keys::derive(&"A".repeat(43)).is_ok());
    }
}
