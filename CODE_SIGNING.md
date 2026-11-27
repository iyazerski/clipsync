# Code signing policy

> **Status:** clipsync has applied to SignPath Foundation for free Windows code signing. Until the application is approved, Windows releases aren't signed.

Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org).

## What gets signed

- **Windows:** the installer, `clipsync-setup-<version>-amd64.exe`. It's built from this repository by the [release workflow](.github/workflows/release.yml) on GitHub-hosted runners, and only that build gets signed. A maintainer approves each release in SignPath before it's signed.
- **Mac:** signed ad hoc, not by Apple yet.
- **Android:** signed with the project's own key. Its fingerprint is in [SECURITY.md](SECURITY.md#check-a-download).

Every download is also attested by GitHub Actions, so you can check where it was built. See [how to check a download](SECURITY.md#check-a-download).

## Team roles

- **Committers and reviewers:** [Ihar Yazerski](https://github.com/iyazerski)
- **Approvers:** [Ihar Yazerski](https://github.com/iyazerski)

Changes from anyone else come in as pull requests, and a committer reviews each one before it's merged.

## Privacy

clipsync sends what you copy, encrypted, only to the relay you pick during setup. See the [privacy policy](PRIVACY.md).
