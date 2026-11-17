# Security

clipsync moves what you copy between your devices, so it handles text you may not want anyone else to see. Please report anything that could let someone read, change or replay clips, or get into a relay without the token.

## Report a vulnerability

Report it privately through [GitHub's vulnerability reporting](https://github.com/iyazerski/clipsync/security/advisories/new). Please don't open a public issue.

Include what you found, how to reproduce it, and the version (`clipsync --version` on a computer, or the version at the bottom of the phone app). You'll get an answer within a week.

## Supported versions

Only the latest release gets fixes. Update with `clipsync update` (or `brew upgrade clipsync`) on computers, **Check for updates** in the phone app, and `docker compose pull && docker compose up -d` on the relay.

## How clips are protected

- Your devices share a 256-bit secret. Each clip is sealed with AES-256-GCM, with a key derived from the secret, before it leaves the device.
- The relay gets only a token derived from the secret, which can't be turned back into the key. It can't read clips.
- Each sealed clip carries the time it was sealed. Devices refuse clips sealed more than 5 minutes away from their clock, and clips they opened already since they started, which limits what a relay could replay.
- Devices talk to the relay only over HTTPS.

## How releases are protected

- Every release is built by [GitHub Actions](.github/workflows/release.yml) from the public source.
- Every desktop archive has a SHA-256 checksum next to it. `install.sh`, `install.ps1` and `clipsync update` refuse a download that doesn't match it, and Homebrew checks the checksum in its formula.
- Every APK is signed with the same key, so Android installs an update only if it comes from the same source.
- The Mac and Windows binaries aren't code-signed or notarized yet.
