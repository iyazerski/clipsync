//! clipsync-relay: passes clipboard text between your devices.
//!
//! Each device keeps one server-sent event stream open and publishes what
//! you copy with a POST (see [`clipsync_proto`]). The relay holds only the
//! latest clip, in memory, and forwards it to every other device. Like
//! Apple's Universal Clipboard, it forgets the clip after a short while
//! (`CLIP_TTL_SECS`), so a device that reconnects later doesn't get stale
//! text. Clips are sealed on the devices, so the relay can't read them.
//!
//! It serves plain HTTP behind a TLS-terminating proxy, for one token
//! (`CLIPSYNC_TOKEN`), which `clipsync token` prints on a Mac. `/pair` is
//! the page that `clipsync pair`'s QR code opens on the phone.

use std::convert::Infallible;
use std::net::SocketAddr;
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use axum::extract::{DefaultBodyLimit, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::sse::{Event, KeepAlive, Sse};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use clap::Parser;
use clipsync_proto::{CLIP_PATH, DEVICE_HEADER, EVENTS_PATH, MAX_DEVICE_ID, MAX_SEALED};
use futures_util::Stream;
use ring::digest::{SHA256, digest};
use serde_json::json;
use tokio::sync::broadcast;

/// How often an idle event stream gets a comment line. It keeps proxies and
/// NAT from dropping the connection, and lets clients notice a dead one
/// (they reconnect after a few silent intervals).
const KEEP_ALIVE: Duration = Duration::from_secs(45);
/// Clips buffered per stream before a slow one skips ahead to the latest.
const QUEUE: usize = 16;

#[derive(Parser)]
#[command(version, about, long_about = None)]
struct Opts {
    #[arg(long, env = "BIND_ADDR", default_value = "0.0.0.0:8080")]
    bind: SocketAddr,
    /// The token every device sends (`clipsync token` prints it)
    #[arg(long, env = "CLIPSYNC_TOKEN", hide_env_values = true)]
    token: String,
    /// How long, in seconds, the relay keeps the latest clip for devices
    /// that connect later
    #[arg(long, env = "CLIP_TTL_SECS", default_value_t = 120)]
    clip_ttl_secs: u64,
}

/// A sealed clip, and the device that sent it.
struct Clip {
    id: u64,
    from: String,
    sealed: String,
}

/// The latest clip, until it expires, and the last id given out.
#[derive(Default)]
struct Latest {
    clip: Option<Arc<Clip>>,
    id: u64,
}

struct Relay {
    /// The token's SHA-256, compared with the request token's: comparing
    /// digests leaks nothing useful through timing.
    token: Vec<u8>,
    ttl: Duration,
    latest: Mutex<Latest>,
    clips: broadcast::Sender<Arc<Clip>>,
}

impl Relay {
    fn latest(&self) -> MutexGuard<'_, Latest> {
        // A panic aborts the process (see the release profile), so the lock
        // is never poisoned in release; tests just carry on.
        self.latest.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// The requesting device's id, if the request has the right token.
    fn device(&self, headers: &HeaderMap) -> Result<String, StatusCode> {
        let token = headers
            .get(header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.strip_prefix("Bearer "))
            .ok_or(StatusCode::UNAUTHORIZED)?;
        if digest(&SHA256, token.as_bytes()).as_ref() != self.token {
            return Err(StatusCode::UNAUTHORIZED);
        }
        headers
            .get(DEVICE_HEADER)
            .and_then(|v| v.to_str().ok())
            .filter(|id| !id.is_empty() && id.len() <= MAX_DEVICE_ID)
            .map(str::to_owned)
            .ok_or(StatusCode::BAD_REQUEST)
    }
}

#[tokio::main]
async fn main() -> Result<()> {
    let opts = Opts::parse();
    let listener = tokio::net::TcpListener::bind(opts.bind)
        .await
        .with_context(|| format!("binding {}", opts.bind))?;
    eprintln!("listening on {}", opts.bind);
    // Not a graceful shutdown: event streams never end on their own, so it
    // would only wait for the kill. Clients reconnect and catch up.
    tokio::select! {
        served = axum::serve(listener, app(&opts.token, Duration::from_secs(opts.clip_ttl_secs))) => served?,
        stopped = shutdown() => stopped?,
    }
    Ok(())
}

/// The relay's routes, for `token`, keeping clips for `ttl`.
fn app(token: &str, ttl: Duration) -> Router {
    let relay = Arc::new(Relay {
        token: digest(&SHA256, token.as_bytes()).as_ref().to_vec(),
        ttl,
        latest: Mutex::default(),
        clips: broadcast::channel(QUEUE).0,
    });
    Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/pair", get(pair_page))
        .route(CLIP_PATH, post(publish))
        .route(EVENTS_PATH, get(events))
        .layer(DefaultBodyLimit::max(MAX_SEALED))
        .with_state(relay)
}

/// The page a phone opens from `clipsync pair`'s QR code: it hands the
/// secret in the URL fragment to the app.
async fn pair_page() -> impl IntoResponse {
    (
        [
            (header::CONTENT_TYPE, "text/html; charset=utf-8"),
            (header::CACHE_CONTROL, "no-store"),
        ],
        include_str!("../assets/pair.html"),
    )
}

/// Keep a clip as the latest and pass it to the other devices.
async fn publish(State(relay): State<Arc<Relay>>, headers: HeaderMap, sealed: String) -> Response {
    let from = match relay.device(&headers) {
        Ok(device) => device,
        Err(status) => return status.into_response(),
    };
    // Base64url only: event data must not hold line breaks.
    let valid = |c: char| c.is_ascii_alphanumeric() || c == '-' || c == '_';
    if sealed.is_empty() || !sealed.chars().all(valid) {
        return StatusCode::BAD_REQUEST.into_response();
    }
    let clip = {
        let mut latest = relay.latest();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_or(0, |d| u64::try_from(d.as_millis()).unwrap_or(u64::MAX));
        // Ids keep growing even if the clock steps back, so devices that
        // remember the last id never skip a clip.
        latest.id = now.max(latest.id + 1);
        let clip = Arc::new(Clip {
            id: latest.id,
            from,
            sealed,
        });
        latest.clip = Some(clip.clone());
        clip
    };
    // Forget it after the TTL, unless a newer clip replaced it.
    tokio::spawn({
        let (relay, id) = (relay.clone(), clip.id);
        async move {
            tokio::time::sleep(relay.ttl).await;
            let mut latest = relay.latest();
            if latest.clip.as_ref().is_some_and(|clip| clip.id == id) {
                latest.clip = None;
            }
        }
    });
    // No listeners is fine: they get the latest clip when they connect.
    let _ = relay.clips.send(clip.clone());
    Json(json!({ "id": clip.id })).into_response()
}

/// Stream clips from the other devices: the latest one first, if it's newer
/// than `Last-Event-ID`, then each new one.
async fn events(State(relay): State<Arc<Relay>>, headers: HeaderMap) -> Response {
    let device = match relay.device(&headers) {
        Ok(device) => device,
        Err(status) => return status.into_response(),
    };
    let seen = headers
        .get("last-event-id")
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.parse().ok())
        .unwrap_or(0);
    // Subscribe before reading the latest clip, so none falls in between.
    let clips = relay.clips.subscribe();
    let first = relay.latest().clip.clone();
    Sse::new(stream(relay, clips, first, device, seen))
        .keep_alive(KeepAlive::new().interval(KEEP_ALIVE))
        .into_response()
}

/// The events for `device`: `first`, then each clip from `clips`, skipping
/// its own clips and those it has already seen.
fn stream(
    relay: Arc<Relay>,
    clips: broadcast::Receiver<Arc<Clip>>,
    first: Option<Arc<Clip>>,
    device: String,
    seen: u64,
) -> impl Stream<Item = Result<Event, Infallible>> {
    futures_util::stream::unfold(
        (clips, first, seen),
        move |(mut clips, mut next, mut seen)| {
            let (relay, device) = (relay.clone(), device.clone());
            async move {
                loop {
                    let clip = match next.take() {
                        Some(clip) => clip,
                        None => match clips.recv().await {
                            Ok(clip) => clip,
                            // Only the latest clip matters, so a stream that
                            // fell behind skips to it.
                            Err(broadcast::error::RecvError::Lagged(_)) => {
                                match relay.latest().clip.clone() {
                                    Some(clip) => clip,
                                    None => continue,
                                }
                            }
                            Err(broadcast::error::RecvError::Closed) => return None,
                        },
                    };
                    if clip.id <= seen || clip.from == device {
                        continue;
                    }
                    seen = clip.id;
                    let event = Event::default().id(clip.id.to_string()).data(&clip.sealed);
                    return Some((Ok(event), (clips, None, seen)));
                }
            }
        },
    )
}

/// Wait for Ctrl-C or SIGTERM (Kubernetes stopping the pod).
async fn shutdown() -> Result<()> {
    use tokio::signal::unix::{SignalKind, signal};
    let mut term = signal(SignalKind::terminate()).context("listening for SIGTERM")?;
    tokio::select! {
        _ = tokio::signal::ctrl_c() => {}
        _ = term.recv() => {}
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use futures_util::StreamExt;

    use super::*;

    const TOKEN: &str = "test-token";
    const TTL: Duration = Duration::from_millis(500);

    /// Serve the relay on a free local port and return its address.
    async fn serve() -> String {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move { axum::serve(listener, app(TOKEN, TTL)).await });
        format!("http://{addr}")
    }

    fn request(
        method: reqwest::Method,
        url: &str,
        token: &str,
        device: &str,
    ) -> reqwest::RequestBuilder {
        reqwest::Client::new()
            .request(method, url)
            .bearer_auth(token)
            .header(DEVICE_HEADER, device)
    }

    async fn publish(base: &str, device: &str, sealed: &str) -> u64 {
        let reply: serde_json::Value = request(
            reqwest::Method::POST,
            &format!("{base}{CLIP_PATH}"),
            TOKEN,
            device,
        )
        .body(sealed.to_owned())
        .send()
        .await
        .unwrap()
        .error_for_status()
        .unwrap()
        .json()
        .await
        .unwrap();
        reply["id"].as_u64().unwrap()
    }

    /// Open an event stream as `device` and return the `(id, data)` pairs
    /// of the events that arrive within `wait`.
    async fn listen(
        base: &str,
        device: &str,
        seen: Option<u64>,
        wait: Duration,
    ) -> Vec<(u64, String)> {
        let mut req = request(
            reqwest::Method::GET,
            &format!("{base}{EVENTS_PATH}"),
            TOKEN,
            device,
        );
        if let Some(seen) = seen {
            req = req.header("last-event-id", seen.to_string());
        }
        let resp = req.send().await.unwrap().error_for_status().unwrap();
        let mut body = resp.bytes_stream();
        let mut text = String::new();
        let _ = tokio::time::timeout(wait, async {
            while let Some(Ok(chunk)) = body.next().await {
                text.push_str(std::str::from_utf8(&chunk).unwrap());
            }
        })
        .await;
        text.split("\n\n")
            .filter_map(|event| {
                let field = |name: &str| {
                    event
                        .lines()
                        .find_map(|line| line.strip_prefix(name))
                        .map(str::to_owned)
                };
                Some((field("id: ")?.parse().ok()?, field("data: ")?))
            })
            .collect()
    }

    #[tokio::test]
    async fn clips_reach_the_other_devices_only() {
        let base = serve().await;
        let mac = tokio::spawn({
            let base = base.clone();
            async move { listen(&base, "mac", None, Duration::from_millis(500)).await }
        });
        let phone = tokio::spawn({
            let base = base.clone();
            async move { listen(&base, "phone", None, Duration::from_millis(500)).await }
        });
        tokio::time::sleep(Duration::from_millis(100)).await;
        let first = publish(&base, "phone", "aGVsbG8").await;
        let second = publish(&base, "mac", "d29ybGQ").await;
        assert!(second > first);
        assert_eq!(mac.await.unwrap(), vec![(first, "aGVsbG8".to_owned())]);
        assert_eq!(phone.await.unwrap(), vec![(second, "d29ybGQ".to_owned())]);
    }

    #[tokio::test]
    async fn a_new_stream_starts_with_the_latest_clip_it_has_not_seen() {
        let base = serve().await;
        publish(&base, "phone", "b2xk").await;
        let id = publish(&base, "phone", "bmV3").await;
        let wait = Duration::from_millis(200);
        assert_eq!(
            listen(&base, "mac", None, wait).await,
            vec![(id, "bmV3".to_owned())]
        );
        assert_eq!(listen(&base, "mac", Some(id - 1), wait).await.len(), 1);
        assert_eq!(listen(&base, "mac", Some(id), wait).await, vec![]);
    }

    #[tokio::test]
    async fn clips_expire() {
        let base = serve().await;
        publish(&base, "phone", "b2xk").await;
        tokio::time::sleep(TTL + Duration::from_millis(100)).await;
        let wait = Duration::from_millis(200);
        assert_eq!(listen(&base, "mac", None, wait).await, vec![]);
        // A newer clip resets the clock: the first one's expiry leaves it alone.
        publish(&base, "phone", "YQ").await;
        tokio::time::sleep(TTL / 2).await;
        let id = publish(&base, "phone", "Yg").await;
        tokio::time::sleep(TTL * 2 / 3).await;
        assert_eq!(
            listen(&base, "mac", None, wait).await,
            vec![(id, "Yg".to_owned())]
        );
    }

    #[tokio::test]
    async fn bad_requests_are_refused() {
        let base = serve().await;
        let clip = format!("{base}{CLIP_PATH}");
        let status =
            |req: reqwest::RequestBuilder| async move { req.send().await.unwrap().status() };
        let wrong = request(reqwest::Method::POST, &clip, "wrong", "mac").body("YQ");
        assert_eq!(status(wrong).await, StatusCode::UNAUTHORIZED);
        let events = request(
            reqwest::Method::GET,
            &format!("{base}{EVENTS_PATH}"),
            "wrong",
            "mac",
        );
        assert_eq!(status(events).await, StatusCode::UNAUTHORIZED);
        let no_device = reqwest::Client::new()
            .post(&clip)
            .bearer_auth(TOKEN)
            .body("YQ");
        assert_eq!(status(no_device).await, StatusCode::BAD_REQUEST);
        let line_break = request(reqwest::Method::POST, &clip, TOKEN, "mac").body("YQ\ndata: x");
        assert_eq!(status(line_break).await, StatusCode::BAD_REQUEST);
        let too_big =
            request(reqwest::Method::POST, &clip, TOKEN, "mac").body("A".repeat(MAX_SEALED + 1));
        assert_eq!(status(too_big).await, StatusCode::PAYLOAD_TOO_LARGE);
    }
}
