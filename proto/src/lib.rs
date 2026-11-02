//! What the clipsync relay and its clients (the Mac service and the Android
//! app) agree on. The Android app repeats these values in `Relay.kt`.
//!
//! Every request carries `Authorization: Bearer <token>` and the device's id
//! in [`DEVICE_HEADER`]. The relay keeps only the latest clip, in memory,
//! for a couple of minutes.
//!
//! - `POST` [`CLIP_PATH`] publishes a clip: the body is the sealed text.
//! - `GET` [`EVENTS_PATH`] streams clips from the other devices as
//!   server-sent events: the `id` is the clip's id (milliseconds since the
//!   Unix epoch, always increasing) and the `data` the sealed text. With
//!   `Last-Event-ID`, the stream starts with the latest clip if it's newer.
//!
//! The relay never sees the text: devices seal it with a key derived from a
//! secret the relay doesn't have, and send base64url (no padding), so a
//! sealed clip never holds a line break.

/// Where devices publish a clip.
pub const CLIP_PATH: &str = "/clip";

/// Where devices listen for clips from the other devices.
pub const EVENTS_PATH: &str = "/events";

/// The header with the sending device's id, so it doesn't get its own clips back.
pub const DEVICE_HEADER: &str = "x-clipsync-device";

/// The longest device id the relay accepts.
pub const MAX_DEVICE_ID: usize = 64;

/// The longest text, in UTF-8 bytes, that devices send. Clipboard sync is
/// for text you paste, not for documents.
pub const MAX_TEXT: usize = 256 * 1024;

/// The longest sealed clip the relay accepts: [`MAX_TEXT`] plus the
/// sealing overhead, in base64.
pub const MAX_SEALED: usize = (MAX_TEXT + 64) * 4 / 3 + 4;
