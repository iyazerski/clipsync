//! The service: send what you copy on this Mac to the relay, and put what
//! the other devices copy on this Mac's clipboard.
//!
//! Three loops run side by side. [`watch`] polls the pasteboard and hands
//! new text to [`send`], which publishes the latest one and retries for a
//! while if the relay is out of reach. [`listen`] keeps an event stream to
//! the relay open, reconnecting when it drops or the Mac wakes from sleep,
//! and writes each clip to the pasteboard. Both sides remember the last
//! text by its digest, so a clip that arrives isn't sent back, and the same
//! text arriving twice (say, from Universal Clipboard too) is ignored.

use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result, bail};
use clipsync_proto::{CLIP_PATH, DEVICE_HEADER, EVENTS_PATH, MAX_TEXT};
use futures_util::StreamExt;
use reqwest::StatusCode;
use reqwest::header::ACCEPT;
use ring::digest::{SHA256, digest};
use tokio::sync::{Notify, watch};

use crate::config::Config;
use crate::pasteboard;
use crate::seal::Keys;

/// How often to look at the pasteboard's change count.
const POLL: Duration = Duration::from_millis(300);
/// A longer gap between polls means the Mac slept, and the event stream
/// is likely dead: reconnect at once rather than wait for the silence limit.
const WAKE_GAP: Duration = Duration::from_secs(5);
/// Reconnect if nothing came from the relay for this long. It sends a
/// comment every 45 s on an idle stream.
const SILENCE_LIMIT: Duration = Duration::from_secs(100);
/// How long one request may take to get a response.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(20);
/// How long to keep retrying a clip the relay didn't take. Later, it
/// would overwrite what the other devices copied since.
const SEND_FOR: Duration = Duration::from_secs(120);
/// The first wait before a retry or reconnect; it doubles after each failure.
const MIN_BACKOFF: Duration = Duration::from_secs(1);
/// The longest wait between retries.
const MAX_BACKOFF: Duration = Duration::from_secs(30);
/// A stream that lasted this long worked, so the next reconnect starts
/// fast again.
const STABLE_AFTER: Duration = Duration::from_secs(60);

/// This Mac's clipboard, as far as syncing goes.
struct Last {
    /// The change count already handled.
    count: isize,
    /// The digest of the text last sent or received.
    text: Option<[u8; 32]>,
}

struct Sync {
    config: Config,
    keys: Keys,
    http: reqwest::Client,
    last: Mutex<Last>,
}

/// Whether the relay accepts a secret's token.
#[derive(Debug, PartialEq)]
pub enum Check {
    Accepted,
    /// The relay doesn't know this secret's token.
    Refused,
}

/// Sync this Mac's clipboard until something fails for good.
pub async fn run(config: Config) -> Result<()> {
    let sync = Arc::new(Sync::new(config)?);
    eprintln!("syncing through {}", sync.config.relay);
    let (copied, pending) = watch::channel(None);
    let woke = Arc::new(Notify::new());
    tokio::select! {
        done = watch(sync.clone(), copied, woke.clone()) => done,
        done = send(sync.clone(), pending) => done,
        done = listen(sync, woke) => done,
    }
}

/// Whether the relay accepts `config`'s secret.
pub async fn check(config: Config) -> Result<Check> {
    let sync = Sync::new(config)?;
    let resp = sync
        .request(reqwest::Method::GET, EVENTS_PATH)
        .timeout(REQUEST_TIMEOUT)
        .send()
        .await?;
    if resp.status() == StatusCode::UNAUTHORIZED {
        return Ok(Check::Refused);
    }
    resp.error_for_status()?;
    Ok(Check::Accepted)
}

impl Sync {
    fn new(config: Config) -> Result<Self> {
        let keys = Keys::derive(&config.secret)?;
        let http = reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(10))
            .build()
            .context("setting up HTTP")?;
        Ok(Self {
            config,
            keys,
            http,
            last: Mutex::new(Last {
                // What's on the pasteboard now stays here: only new copies sync.
                count: pasteboard::change_count(),
                text: None,
            }),
        })
    }

    fn last(&self) -> MutexGuard<'_, Last> {
        // A panic aborts the process (see the release profile), so the lock
        // is never poisoned in release; tests just carry on.
        self.last.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn request(&self, method: reqwest::Method, path: &str) -> reqwest::RequestBuilder {
        self.http
            .request(method, format!("{}{path}", self.config.relay))
            .bearer_auth(&self.keys.token)
            .header(DEVICE_HEADER, &self.config.device)
    }

    /// The pasteboard's text, if it was copied since the last look and
    /// should be sent. Blocks on AppKit.
    fn copied(&self) -> Option<String> {
        let mut last = self.last();
        let count = pasteboard::change_count();
        if count == last.count {
            return None;
        }
        last.count = count;
        let text = pasteboard::read().filter(|text| !text.trim().is_empty());
        let text = match text {
            Some(text) if text.len() <= MAX_TEXT => text,
            skipped => {
                if let Some(text) = skipped {
                    eprintln!(
                        "not sending a copy of {} KB: the limit is {} KB",
                        text.len() / 1024,
                        MAX_TEXT / 1024
                    );
                }
                // The synced text is no longer on the pasteboard, so the same
                // text arriving or copied again must not be skipped.
                last.text = None;
                return None;
            }
        };
        let id = text_id(&text);
        if last.text == Some(id) {
            return None;
        }
        last.text = Some(id);
        Some(text)
    }

    /// Put `text` from another device on the pasteboard, unless it's there
    /// already. Returns whether it was written. Blocks on AppKit.
    fn received(&self, text: &str) -> bool {
        let mut last = self.last();
        let id = text_id(text);
        if last.text == Some(id) {
            return false;
        }
        last.count = pasteboard::write(text);
        last.text = Some(id);
        true
    }

    /// Seal `text` and publish it.
    async fn publish(&self, text: &str) -> Result<()> {
        let resp = self
            .request(reqwest::Method::POST, CLIP_PATH)
            .timeout(REQUEST_TIMEOUT)
            .body(self.keys.seal(text)?)
            .send()
            .await?;
        if resp.status() == StatusCode::UNAUTHORIZED {
            bail!("the relay refused the token; its CLIPSYNC_TOKEN should be `clipsync token`");
        }
        resp.error_for_status()?;
        Ok(())
    }

    /// Follow the relay's event stream until it ends, goes silent, or the
    /// Mac wakes from sleep. `seen` is the last clip's id, kept across
    /// streams so a reconnect brings only what was missed.
    async fn stream(self: &Arc<Self>, seen: &mut u64, woke: &Notify) -> Result<()> {
        let resp = self
            .request(reqwest::Method::GET, EVENTS_PATH)
            .header(ACCEPT, "text/event-stream")
            .header("last-event-id", seen.to_string())
            .send();
        let resp = tokio::time::timeout(REQUEST_TIMEOUT, resp)
            .await
            .context("the relay didn't answer")??;
        if resp.status() == StatusCode::UNAUTHORIZED {
            bail!("the relay refused the token; its CLIPSYNC_TOKEN should be `clipsync token`");
        }
        let resp = resp.error_for_status()?;
        eprintln!("connected");
        let mut body = resp.bytes_stream();
        let mut events = Events::default();
        loop {
            let chunk = tokio::select! {
                chunk = tokio::time::timeout(SILENCE_LIMIT, body.next()) => chunk,
                () = woke.notified() => {
                    eprintln!("woke from sleep; reconnecting");
                    return Ok(());
                }
            };
            let chunk = match chunk {
                Err(_) => bail!("no word from the relay for {}s", SILENCE_LIMIT.as_secs()),
                Ok(None) => return Ok(()),
                Ok(Some(chunk)) => chunk.context("reading the stream")?,
            };
            for event in events.push(&chunk) {
                *seen = (*seen).max(event.id.unwrap_or(0));
                self.apply(event).await?;
            }
        }
    }

    /// Open a clip and put it on the pasteboard.
    async fn apply(self: &Arc<Self>, event: Event) -> Result<()> {
        let text = match self.keys.open(&event.data) {
            Ok(text) => text,
            Err(e) => {
                eprintln!("skipped a clip: {e:#}");
                return Ok(());
            }
        };
        let chars = text.chars().count();
        let sync = self.clone();
        if tokio::task::spawn_blocking(move || sync.received(&text)).await? {
            eprintln!("received {chars} characters");
        }
        Ok(())
    }

    /// Publish `text`, retrying with backoff for [`SEND_FOR`] while it's
    /// still the last text. Returns early, with `true`, when newer text
    /// arrives in `pending`.
    async fn send_one(
        &self,
        text: &str,
        pending: &mut watch::Receiver<Option<String>>,
    ) -> Result<bool> {
        // The wall clock, unlike Instant on macOS, counts time asleep.
        let deadline = SystemTime::now() + SEND_FOR;
        let id = text_id(text);
        let mut backoff = MIN_BACKOFF;
        loop {
            // A newer copy or a clip from another device replaced it: sending
            // it now would overwrite the newer text on the other devices.
            if self.last().text != Some(id) {
                return Ok(false);
            }
            match self.publish(text).await {
                Ok(()) => {
                    eprintln!("sent {} characters", text.chars().count());
                    return Ok(false);
                }
                Err(e) => eprintln!("sending failed: {e:#}"),
            }
            if SystemTime::now() > deadline {
                eprintln!("gave up sending a copy after {}s", SEND_FOR.as_secs());
                return Ok(false);
            }
            tokio::select! {
                () = tokio::time::sleep(backoff) => {}
                changed = pending.changed() => {
                    changed.context("the pasteboard watch stopped")?;
                    return Ok(true);
                }
            }
            backoff = (backoff * 2).min(MAX_BACKOFF);
        }
    }
}

/// Poll the pasteboard and pass new text to [`send`]; tell [`listen`] when
/// the Mac woke from sleep.
async fn watch(
    sync: Arc<Sync>,
    copied: watch::Sender<Option<String>>,
    woke: Arc<Notify>,
) -> Result<()> {
    let mut tick = tokio::time::interval(POLL);
    tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    // The wall clock, unlike Instant on macOS, moves while the Mac sleeps.
    let mut wall = SystemTime::now();
    loop {
        tick.tick().await;
        let now = SystemTime::now();
        if now.duration_since(wall).is_ok_and(|gap| gap > WAKE_GAP) {
            woke.notify_one();
        }
        wall = now;
        let sync = sync.clone();
        if let Some(text) = tokio::task::spawn_blocking(move || sync.copied()).await? {
            copied.send_replace(Some(text));
        }
    }
}

/// Publish the latest text from [`watch`]; text copied while an older one
/// is still being sent replaces it.
async fn send(sync: Arc<Sync>, mut pending: watch::Receiver<Option<String>>) -> Result<()> {
    loop {
        pending
            .changed()
            .await
            .context("the pasteboard watch stopped")?;
        loop {
            let text = pending.borrow_and_update().clone();
            let Some(text) = text else { break };
            if !sync.send_one(&text, &mut pending).await? {
                break;
            }
        }
    }
}

/// Keep an event stream to the relay open and apply what comes.
async fn listen(sync: Arc<Sync>, woke: Arc<Notify>) -> Result<()> {
    // Clips from before this start are old news.
    let mut seen = now_ms();
    let mut backoff = MIN_BACKOFF;
    loop {
        let started = Instant::now();
        match sync.stream(&mut seen, &woke).await {
            Ok(()) => {}
            Err(e) => eprintln!("relay: {e:#}"),
        }
        if started.elapsed() > STABLE_AFTER {
            backoff = MIN_BACKOFF;
        }
        tokio::select! {
            () = tokio::time::sleep(backoff) => {}
            () = woke.notified() => {}
        }
        backoff = (backoff * 2).min(MAX_BACKOFF);
    }
}

fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| u64::try_from(d.as_millis()).unwrap_or(u64::MAX))
}

/// A text's SHA-256: what the service remembers instead of the text.
fn text_id(text: &str) -> [u8; 32] {
    let mut id = [0; 32];
    id.copy_from_slice(digest(&SHA256, text.as_bytes()).as_ref());
    id
}

/// One server-sent event.
#[derive(Debug, Default, PartialEq)]
struct Event {
    id: Option<u64>,
    data: String,
}

/// A server-sent event parser for the relay's events: an `id` and one
/// `data` line each, separated by blank lines. Comments (keep-alives) and
/// other fields are skipped.
#[derive(Default)]
struct Events {
    buffer: Vec<u8>,
}

impl Events {
    /// Add a chunk of the stream and return the events it completed.
    fn push(&mut self, chunk: &[u8]) -> Vec<Event> {
        self.buffer.extend_from_slice(chunk);
        let mut events = Vec::new();
        while let Some(end) = self.buffer.windows(2).position(|w| w == b"\n\n") {
            let block: Vec<u8> = self.buffer.drain(..end + 2).collect();
            let mut event = Event::default();
            for line in String::from_utf8_lossy(&block).lines() {
                let (field, value) = line.split_once(':').unwrap_or((line, ""));
                let value = value.strip_prefix(' ').unwrap_or(value);
                match field {
                    "id" => event.id = value.parse().ok(),
                    "data" => event.data.push_str(value),
                    _ => {}
                }
            }
            if !event.data.is_empty() {
                events.push(event);
            }
        }
        events
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn events_are_parsed_across_chunks() {
        let mut events = Events::default();
        assert_eq!(events.push(b":\n\nid: 5\nda"), vec![]);
        let event = |id, data: &str| Event {
            id: Some(id),
            data: data.into(),
        };
        assert_eq!(
            events.push(b"ta: abc\n\nid: 6\ndata: d"),
            vec![event(5, "abc")]
        );
        assert_eq!(events.push(b"ef\n\n"), vec![event(6, "def")]);
    }
}
