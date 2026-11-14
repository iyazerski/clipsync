//! Sealing clips, so the relay passes them on without reading them.
//!
//! One secret, shared by your devices, gives two values: the relay token
//! (the relay knows it) and the sealing key (only the devices know it). Each
//! is an HMAC-SHA256 of a label, keyed with the secret, so the relay can't
//! work out the key from the token. A sealed clip is AES-256-GCM:
//! `version || nonce || ciphertext || tag`, in base64url without padding.
//! The sealed text starts with the time it was sealed, so a device refuses
//! clips that are old, or that it opened already: the relay can't replay
//! them. The Android app does the same in `Seal.kt`; the tests here and
//! there share their test vectors.

use anyhow::{Context, Result, bail, ensure};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD as B64;
use ring::aead::{AES_256_GCM, Aad, LessSafeKey, NONCE_LEN, Nonce, UnboundKey};
use ring::hmac;
use ring::rand::SystemRandom;
use std::time::{SystemTime, UNIX_EPOCH};

/// The secret's length in bytes.
const SECRET_LEN: usize = 32;
/// The first byte of a sealed clip; bumped if the format changes.
const VERSION: u8 = 2;
/// The version and the nonce, before the ciphertext.
const HEADER: usize = 1 + NONCE_LEN;
/// The sealing time's length: milliseconds since the Unix epoch, big-endian.
const STAMP: usize = 8;
/// How far a clip's sealing time may be from the opening device's clock.
/// It covers how long the relay keeps a clip (`CLIP_TTL_SECS`, 2 minutes by
/// default) and clocks that drift apart.
const MAX_AGE_MS: u64 = 5 * 60 * 1000;

/// The token and the key that a secret gives.
pub struct Keys {
    pub token: String,
    key: LessSafeKey,
}

/// The clips opened lately, so that none opens twice.
#[derive(Default)]
pub struct Opened(Vec<(u64, [u8; NONCE_LEN])>);

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

/// Milliseconds since the Unix epoch.
pub fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| u64::try_from(d.as_millis()).unwrap_or(u64::MAX))
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

    /// Seal `text` with a fresh random nonce and the current time.
    pub fn seal(&self, text: &str) -> Result<String> {
        self.seal_with(random()?, now_ms(), text)
    }

    fn seal_with(&self, nonce: [u8; NONCE_LEN], sealed_at: u64, text: &str) -> Result<String> {
        let tag_len = self.key.algorithm().tag_len();
        let mut sealed = Vec::with_capacity(HEADER + STAMP + text.len() + tag_len);
        sealed.push(VERSION);
        sealed.extend_from_slice(&nonce);
        sealed.extend_from_slice(&sealed_at.to_be_bytes());
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

    /// Open a sealed clip at `now` (ms since the Unix epoch) and add it to
    /// `opened`. Fails if it was sealed with another secret, changed, sealed
    /// too far from `now`, or opened already.
    pub fn open(&self, sealed: &str, now: u64, opened: &mut Opened) -> Result<String> {
        let mut sealed = B64.decode(sealed).context("not base64url")?;
        let Some(&version) = sealed.first() else {
            bail!("empty clip");
        };
        ensure!(
            version == VERSION,
            "clip format {version} is unknown; update clipsync"
        );
        ensure!(sealed.len() >= HEADER, "clip too short");
        let nonce: [u8; NONCE_LEN] = sealed[1..HEADER].try_into().context("bad nonce")?;
        // Decrypts in place and moves the text to the front of the buffer.
        let len = self
            .key
            .open_within(
                Nonce::assume_unique_for_key(nonce),
                Aad::empty(),
                &mut sealed,
                HEADER..,
            )
            .map_err(|_| anyhow::anyhow!("can't open the clip: the devices use different secrets"))?
            .len();
        sealed.truncate(len);
        let (stamp, _) = sealed
            .split_first_chunk::<STAMP>()
            .context("clip too short")?;
        let sealed_at = u64::from_be_bytes(*stamp);
        let off = now.abs_diff(sealed_at);
        ensure!(
            off <= MAX_AGE_MS,
            "the clip was sealed {}s away from now: it's a replay, or a clock is wrong",
            off / 1000
        );
        // Older entries can't match: their clips fail the time check above.
        opened.0.retain(|(at, _)| now.abs_diff(*at) <= MAX_AGE_MS);
        ensure!(
            !opened.0.iter().any(|(_, seen)| *seen == nonce),
            "the clip came before: it's a replay"
        );
        opened.0.push((sealed_at, nonce));
        sealed.drain(..STAMP);
        String::from_utf8(sealed).context("the clip isn't UTF-8")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Shared with the Android app's SealTest: change both together.
    const SECRET: &str = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8";
    const TOKEN: &str = "eJMfuewcmawfRsgaj7Zutkow2oBVjbx8Thv_-3tJnYE";
    const SEALED_AT: u64 = 1_700_000_000_000;
    const SEALED: &str = "AgcHBwcHBwcHBwcHB7vfjbeCOIF7IogOfIGZOG33yZnkFUm3yjqv3ibc0DouywRo";

    #[test]
    fn the_test_vectors_hold() {
        let keys = Keys::derive(SECRET).unwrap();
        let sealed = keys
            .seal_with([7; NONCE_LEN], SEALED_AT, "héllo 👋")
            .unwrap();
        assert_eq!(keys.token, TOKEN);
        assert_eq!(sealed, SEALED);
        let opened = keys.open(SEALED, SEALED_AT, &mut Opened::default());
        assert_eq!(opened.unwrap(), "héllo 👋");
    }

    #[test]
    fn only_the_same_secret_opens_a_clip() {
        let keys = Keys::derive(&new_secret().unwrap()).unwrap();
        let sealed = keys.seal("text").unwrap();
        assert_ne!(sealed, keys.seal("text").unwrap(), "nonces must differ");
        let mut opened = Opened::default();
        assert_eq!(keys.open(&sealed, now_ms(), &mut opened).unwrap(), "text");
        let other = Keys::derive(&new_secret().unwrap()).unwrap();
        assert!(
            other
                .open(&sealed, now_ms(), &mut Opened::default())
                .is_err()
        );
        let mut tampered = sealed.into_bytes();
        let last = tampered.len() - 2;
        tampered[last] = if tampered[last] == b'A' { b'B' } else { b'A' };
        let tampered = std::str::from_utf8(&tampered).unwrap();
        assert!(keys.open(tampered, now_ms(), &mut opened).is_err());
    }

    #[test]
    fn a_clip_opens_once_and_only_while_fresh() {
        let keys = Keys::derive(SECRET).unwrap();
        let mut opened = Opened::default();
        let late = SEALED_AT + MAX_AGE_MS + 1;
        assert!(keys.open(SEALED, late, &mut opened).is_err());
        assert!(
            keys.open(SEALED, SEALED_AT - MAX_AGE_MS - 1, &mut opened)
                .is_err()
        );
        assert!(keys.open(SEALED, SEALED_AT + 1000, &mut opened).is_ok());
        assert!(keys.open(SEALED, SEALED_AT + 2000, &mut opened).is_err());
    }

    #[test]
    fn bad_secrets_are_refused() {
        assert!(Keys::derive("short").is_err());
        assert!(Keys::derive(&"A".repeat(43)).is_ok());
    }
}
