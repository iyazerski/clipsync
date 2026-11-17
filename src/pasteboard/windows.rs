//! The Windows clipboard, through Win32.
//!
//! Like the Mac's pasteboard, it has a sequence number that moves on every
//! copy, so polling it is cheap. Only one app at a time can open the
//! clipboard, so reads and writes retry for a moment while another app
//! has it.

use std::num::NonZeroU32;

use anyhow::{Context, Result};
use clipboard_win::formats::CF_UNICODETEXT;
use clipboard_win::{Clipboard, raw};

/// Tries to open the clipboard, 5 ms apart: about a second.
const OPEN_ATTEMPTS: usize = 200;

/// The clipboard's sequence number: it moves on every copy.
pub fn change_count() -> isize {
    raw::seq_num().map_or(0, |count| count.get() as isize)
}

/// The clipboard's text, unless there is none or the copy is marked private.
pub fn read() -> Result<Option<String>> {
    let _open = Clipboard::new_attempts(OPEN_ATTEMPTS).context("opening the clipboard")?;
    if private() || !raw::is_format_avail(CF_UNICODETEXT) {
        return Ok(None);
    }
    let mut text = Vec::new();
    raw::get_string(&mut text).context("reading the clipboard")?;
    Ok(Some(String::from_utf8_lossy(&text).into_owned()))
}

/// Replace the clipboard's contents with `text`, and return the new
/// sequence number. It's read while the clipboard is still open, so it
/// can't be another app's copy that came right after.
pub fn write(text: &str) -> Result<isize> {
    let _open = Clipboard::new_attempts(OPEN_ATTEMPTS).context("opening the clipboard")?;
    raw::set_string(text).context("writing the clipboard")?;
    Ok(change_count())
}

/// Whether the app that copied asked Windows not to sync the copy to other
/// devices, as password managers do. Needs the clipboard open.
fn private() -> bool {
    let format = |name| raw::register_format(name).map(NonZeroU32::get);
    if format("ExcludeClipboardContentFromMonitorProcessing").is_some_and(raw::is_format_avail) {
        return true;
    }
    // A DWORD: 0 to keep the copy off the user's other devices.
    format("CanUploadToCloudClipboard").is_some_and(|format| {
        let mut flag = [0; 4];
        raw::get(format, &mut flag)
            .is_ok_and(|len| len == flag.len() && u32::from_ne_bytes(flag) == 0)
    })
}
