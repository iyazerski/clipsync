# Privacy policy

clipsync has no accounts, analytics, ads, or crash reporting. It doesn't collect data about you.

## What leaves your devices

- **What you copy.** clipsync encrypts each copy on your device and sends it to the relay you picked during setup: the public relay at `clip.iyazerski.dev`, or your own. The relay can't read it. It keeps only the latest encrypted copy, in memory, for 2 minutes.
- **Your device list.** Each device sends the relay a random ID, plus its name and kind (phone or computer), encrypted so that only your devices can read them. The relay keeps this in memory for up to 7 days after a device was last online.
- **Connection details.** Like any server, the relay sees IP addresses, when devices connect, how many devices a pairing has, and how big each copy is. The relay software keeps these in memory only and doesn't write them to logs. The proxy in front of the public relay may keep standard connection logs to protect it from abuse.
- **Update checks.** The desktop app checks GitHub for a new release once a day. The Android app checks only when you tap **Check for updates**. GitHub sees your IP address, as it does for any download. See [GitHub's privacy statement](https://docs.github.com/site-policy/privacy-policies/github-general-privacy-statement).

Passwords that your password manager marks as private stay on the computer you copied them on.

## Your own relay

If you [host your own relay](docs/self-hosting.md), all of the above goes to your server only, apart from update checks.

## The website

The website is hosted on GitHub Pages and asks GitHub for the latest release, so GitHub sees your IP address. It sets no cookies and has no analytics.

## Questions

Open an [issue](https://github.com/iyazerski/clipsync/issues), or report a privacy problem privately through [GitHub's vulnerability reporting](https://github.com/iyazerski/clipsync/security/advisories/new).
