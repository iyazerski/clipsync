# clipsync

Copy on one device, paste on another. clipsync keeps the clipboards of your Macs and your Android phone in
sync through a small relay that you host yourself. Text only.

## Why

Apple's Universal Clipboard only works between Apple devices that are close to each other. Existing
open-source tools for Android work only on the local network, make one device the "host", and lose the
connection when an IP address changes. Their phone apps also have to stay open, because Android doesn't let
background apps read the clipboard.

clipsync works over the internet, from any network, and every device talks to the same relay. Nobody is the
"host". The phone app only does what Android reliably allows, so it doesn't break when the phone saves battery.

## How it works

```
Mac ──┐                      ┌── Mac
      ├────▶  your relay  ◀───┤
phone ┘   (encrypted text)   └── …
```

- **Relay:** a tiny server on your own machine. It passes each copy to your other devices and keeps only
  the latest one for 2 minutes, in memory, like Universal Clipboard.
- **Macs:** a background service sends what you copy and pastes what the other devices copy. It skips
  passwords that password managers mark as private.
- **Phone:** the app puts what you copy on your Macs on the phone's clipboard automatically. To send from
  the phone, it takes one tap: select text and pick **Send to Macs**, share text to clipsync, or tap the
  **Send clipboard** tile in Quick Settings.
- **Privacy:** your devices share one secret. Text is encrypted on the device before it leaves, and the
  relay can't read it. Without the secret, the relay refuses to talk to a device.

## Setup

### 1. Relay

Pushes to `main` deploy the relay to the k3s cluster (`clip.iyazerski.dev`). The relay needs a token, which
comes from the secret on your first Mac (step 2). Set it once:

```sh
kubectl create secret generic clipsync-relay-secrets --from-literal=CLIPSYNC_TOKEN="$(clipsync token)"
kubectl rollout restart deployment/clipsync-relay   # if the relay was already running
```

### 2. Macs (Apple silicon)

```sh
curl -fsSL https://raw.githubusercontent.com/iyazerski/clipsync/main/install.sh | sh
clipsync setup --relay https://clip.iyazerski.dev
```

On the first Mac, this makes a new secret. On your other Macs, run the command that `clipsync pair` shows
on the first one. It includes the secret.

The service starts at login and logs to `~/Library/Logs/clipsync.log`. `clipsync uninstall` stops it.
Running `install.sh` again updates it. If macOS asks whether clipsync may access the pasteboard, choose to
always allow it.

### 3. Phone

1. Install the app: https://github.com/iyazerski/clipsync/releases/latest/download/clipsync.apk
2. Run `clipsync pair` on a Mac, scan the QR code with the phone's camera, and tap **Open clipsync**.
3. In the app, tap **Allow background activity** and **Add the Quick Settings tile**. On ColorOS, also go to
   App info → Battery usage and turn on **Allow background activity** and **Allow auto launch**.

## Good to know

- Keep the secret private. Anyone with it can read and change what you copy. To replace it, delete
  `~/Library/Application Support/clipsync/config.json` on one Mac and run `clipsync setup` there again. Then
  update the relay token (step 1, with `--dry-run=client -o yaml | kubectl apply -f -`), and pair your other
  Macs and the phone again.
- Text only, up to 256 KB. The last copy wins. There's no history.
- A device that was offline for more than 2 minutes doesn't get what was copied in the meantime.

## Development

`cargo test --workspace` tests the Mac service and the relay. `cd android && ./gradlew lintRelease
testDebugUnitTest assembleRelease` builds the app. `prek run --all-files` runs every check. Every push to
`main` runs CI, deploys the relay when it changed, and publishes a new release with the Mac binary and the
signed APK.
