# clipsync

Keeps the clipboards of your Macs and an Android phone in sync through a tiny relay that you host. Text only.

```
Mac ──┐                        ┌── Mac
      ├──HTTPS──▶ relay ◀──HTTPS──┤
phone ┘   (sealed clips only)  └── …
```

- **Mac → everywhere:** automatic. A background service sees each copy and sends it.
- **Everywhere → phone:** automatic. The app keeps a connection open and writes each clip to the clipboard.
- **Phone → Macs:** one tap. Select text and pick **Send to Macs**, share text to clipsync, or tap the
  **Send clipboard** tile in Quick Settings (or the action in the notification).

The phone side isn't fully automatic on purpose. Since Android 10, only the focused app (or the keyboard) may
read the clipboard. Apps that sync it automatically depend on workarounds: a hidden overlay that takes focus
(it interrupts typing and games), system log access, or Shizuku. These break from time to time, and ColorOS
kills them. Writing the clipboard from the background is allowed, so the Mac → phone direction needs no tricks.

## How it works

- **Relay** (`relay/`, Rust, axum): devices keep one server-sent event stream open (`GET /events`) and publish
  with `POST /clip` (see `proto/src/lib.rs`). It keeps only the latest clip, in memory, and passes it to every
  other device. A device that reconnects gets the latest clip if it hasn't seen it yet (`Last-Event-ID`).
- **End-to-end sealing:** all devices share one secret. It gives two values: the relay token (the relay knows
  it) and an AES-256-GCM key (only the devices know it). Each is an HMAC-SHA256 of a label keyed with the
  secret, so the relay can't work out the key and never sees the text. `src/seal.rs` and `Seal.kt` share
  test vectors.
- **Mac** (`src/`, Rust): a launch agent polls the pasteboard's change count every 300 ms (macOS has no change
  event) and sends new text. It skips copies that password managers mark as concealed or transient
  (nspasteboard.org types). It remembers the last text by its digest, so a received clip isn't sent back, and
  Universal Clipboard delivering the same text again is ignored. It reconnects after the stream drops, goes
  silent for 100 s (the relay sends a keep-alive every 45 s), or the Mac wakes from sleep.
- **Android** (`android/`, Kotlin, no dependencies, ~40 KB): a foreground service keeps the stream open,
  reconnects when the phone switches networks, and starts after a reboot. `SendActivity` is an invisible
  screen that takes focus for a moment to read the clipboard, sends it, and closes.

## Setup

### 1. Relay

`.github/workflows/deploy-relay.yml` deploys `relay/` to the k3s cluster on every push to `main` that changes
it (secret `KUBE_CONFIG_B64`; host `clip.iyazerski.dev`, or the `RELAY_PUBLIC_HOST` variable). It needs the
relay token, which comes from the secret on your first Mac (step 2):

```sh
kubectl create secret generic clipsync-relay-secrets --from-literal=CLIPSYNC_TOKEN="$(clipsync token)"
kubectl rollout restart deployment/clipsync-relay   # if it was already running
```

To use a new secret, set the token again with `--dry-run=client -o yaml | kubectl apply -f -`, restart the
relay, and pair every device again.

### 2. Macs (Apple silicon)

```sh
curl -fsSL https://raw.githubusercontent.com/iyazerski/clipsync/main/install.sh | sh
clipsync setup --relay https://clip.iyazerski.dev               # first Mac: makes a new secret
clipsync setup --relay https://clip.iyazerski.dev --secret …    # other Macs: `clipsync pair` shows the command
```

`setup` saves the relay, the secret, and a device id to `~/Library/Application Support/clipsync/config.json`
(readable only by you). It starts the service, which runs at login, restarts after a crash, and logs to
`~/Library/Logs/clipsync.log`. `clipsync uninstall` stops it. `clipsync run` syncs in the foreground, for
debugging. Running `install.sh` again updates the binary and restarts the service.

If macOS asks whether clipsync may access the pasteboard, choose to always allow it (System Settings → Privacy
& Security → Paste from Other Apps). The service reads the pasteboard only after its change count moves.

### 3. Phone

1. Install the APK: https://github.com/iyazerski/clipsync/releases/latest/download/clipsync.apk (allow
   installing from the browser; ColorOS may run a security scan first).
2. On a Mac, run `clipsync pair` and scan the QR code with the phone's camera. It opens
   `https://<relay>/pair#<secret>`, and **Open clipsync** hands the pairing to the app. The secret stays in the
   URL fragment, which the browser never sends to the relay. You can also type the relay and the secret in
   the app.
3. In the app: allow notifications, tap **Allow background activity**, and **Add the Quick Settings tile**.
   On ColorOS, also set App info → Battery usage → **Allow background activity** and **Allow auto launch**,
   and lock the app in the recent apps screen.

## Build and checks

```sh
cargo test --workspace --locked                 # Mac service and relay
cd android && ./gradlew lintRelease testDebugUnitTest assembleRelease
prek run --all-files                            # fmt, clippy, Android lint and tests
```

Android needs the SDK (platform 36) in `android/local.properties` (`sdk.dir=…`). The Gradle wrapper pins
Gradle 9.8.2 on JDK 21 and AGP 9.5.0. The release APK is signed with the key from `CLIPSYNC_KEYSTORE` and
`CLIPSYNC_KEYSTORE_PASSWORD` (alias `clipsync`) when they're set, else with the debug key. An APK signed with a
different key can't install over the other one: uninstall first.

### CI/CD

- `ci.yml`: Rust fmt, clippy, and tests on macOS; Android lint and unit tests. On every push and pull request.
- `deploy-relay.yml`: builds the relay image (a static binary in an empty image), pushes it to GHCR, deploys it,
  smoke-checks `/healthz` and `/events`, and rolls back on failure.
- `release.yml`: on pushes to `main` that change the Mac service or the app, builds the Mac binary and the
  signed APK and publishes release `v0.1.<run>`. Secrets: `CLIPSYNC_KEYSTORE_BASE64`,
  `CLIPSYNC_KEYSTORE_PASSWORD`.

## Limits

- Text only, up to 256 KB. Last copy wins: there's no history.
- The relay keeps the latest clip in memory only. After a relay restart, devices that missed a clip don't
  get it.
- A device that was offline gets only the latest clip when it reconnects. A Mac service that just started
  skips clips sent before it started.
