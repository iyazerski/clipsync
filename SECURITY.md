# Security

clipsync moves what you copy between your devices, so it handles text you may not want anyone else to see. Please report anything that could let someone read, change or replay clips, get clips meant for another user's devices, or get into a private relay without its token.

## Report a vulnerability

Report it privately through [GitHub's vulnerability reporting](https://github.com/iyazerski/clipsync/security/advisories/new). Please don't open a public issue.

Include what you found, how to reproduce it, and the version (in the app's **Settings** on a computer, or the version at the bottom of the phone app). You'll get an answer within a week.

## Supported versions

Only the latest release gets fixes. Computers update themselves, or from **Check for updates** in the app's **Settings**. Use **Check for updates** in the phone app, and run `docker compose pull && docker compose up -d` on the relay.

## How clips are protected

- Your devices share a 256-bit secret. Each clip is sealed with AES-256-GCM, with a key derived from the secret, before it leaves the device.
- The relay gets only a token derived from the secret, which can't be turned back into the key. It can't read clips. It passes each clip only to devices that send the same token.
- Each sealed clip carries the time it was sealed. Devices refuse clips sealed more than 5 minutes away from their clock, and clips they opened already since they started, which limits what a relay could replay.
- Devices talk to the relay only over HTTPS.

## How releases are protected

- Every release is built by [GitHub Actions](.github/workflows/release.yml) from the public source.
- Every desktop update is signed with an Ed25519 key that only the release workflow holds. The app and `install.sh` install an update only if its signature matches the public key built into the app.
- `SHA256SUMS` in every release lists the checksums of the desktop downloads. `install.ps1` refuses a Windows setup that doesn't match it.
- Every APK is signed with the same key, so Android installs an update only if it comes from the same source.
- The Mac and Windows apps aren't signed by Apple or Microsoft, or notarized, yet. Mac apps are signed ad hoc.
