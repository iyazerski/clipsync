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

## What the relay knows about your devices

- To list your devices on each other, every device gives the relay its name and kind (phone or computer), sealed with a key derived from the secret and bound to the device's random ID. The relay can't read them, or pass one device's off as another's.
- The relay does see each device's random ID, how many devices a pairing has, and when each one connects and disconnects. It keeps that for up to 7 days after a device was last online, in memory only. A relay could also say a device is online when it isn't.
- **Unpair** tells the relay, which tells your other devices to drop this one from their list. It doesn't change the secret, so a device that kept it could still join again.

## How removing a device works

- **Remove** on a computer moves it to a new pairing, with a new random secret. The removed device knows only the old secret, so it can't read or send clips in the new pairing, and old links and codes don't let it in.
- Your other devices join the new pairing with a new code, as when you first paired them. The new secret never goes through the old pairing, which the removed device could read.
- Until a device joins the new pairing, it still shares the old secret with the removed device, and they can keep syncing through any relay that lets the old pairing in. Removing can't take back clips that the removed device already got.

## How pairing codes are protected

- A code is a number that the relay gives out, plus 6 random digits that the relay never sees. The two devices turn the digits into a shared key with [CPace](https://datatracker.ietf.org/doc/draft-irtf-cfrg-cpace/), and the device that shows the code sends the secret sealed with that key.
- Each try to pair allows one guess of the digits, and nobody can test guesses offline. After a wrong one, the code stops working and the computer says so. It makes a new code only when you ask. So anyone else, the relay too, gets about one chance in a million each time you open a code or enter one.
- Anyone can make a code stop working by guessing, even on a relay they don't run. That can stop you from pairing for a while, but it can't pair their device.
- A code works for 10 minutes. The pairing link and the QR code hold the secret itself, so share them only with your own devices.

## How releases are protected

- Every release is built by [GitHub Actions](.github/workflows/release.yml) from the public source. The workflow attests each download, so anyone can check which commit and workflow run built it.
- Every desktop update is signed with an Ed25519 key that only the release workflow holds. The app and `install.sh` install an update only if its signature matches the public key built into the app.
- `SHA256SUMS` in every release lists the checksums of the desktop downloads. `install.ps1` refuses a Windows setup that doesn't match it.
- Every APK is signed with the same key, so Android installs an update only if it comes from the same source.
- The Mac and Windows apps aren't signed by Apple or Microsoft, or notarized, yet. Mac apps are signed ad hoc. Windows signing through SignPath Foundation is in progress, see the [code signing policy](CODE_SIGNING.md).

## Check a download

With the [GitHub CLI](https://cli.github.com), check that a file came from this repository's release workflow:

```sh
gh attestation verify clipsync-0.2.6.dmg --repo iyazerski/clipsync
```

This works for the `.dmg`, the Windows setup, the update archives, and `clipsync.apk` from v0.2.6 on. It prints the commit and the workflow run that built the file.

Without the GitHub CLI, compare the file's SHA-256 with the release's `SHA256SUMS` (`shasum -a 256 <file>` on a Mac, `Get-FileHash <file>` in PowerShell).

The Android app is signed with this certificate. Android installs updates only from the same certificate, and apps like [AppVerifier](https://github.com/soupslurpr/AppVerifier) can check it before you install:

```text
dev.iyazerski.clipsync
78:3B:32:90:30:8E:6E:F6:AF:81:58:FB:DB:67:47:6B:E5:CF:9C:48:69:16:59:CD:A3:0B:DB:38:69:46:94:C4
```
