# Contributing

Thanks for helping with clipsync. Bug reports, ideas and pull requests are all welcome.

## Report a bug or ask for a feature

[Open an issue](https://github.com/iyazerski/clipsync/issues/new/choose) and pick a template. For questions, use [Discussions](https://github.com/iyazerski/clipsync/discussions). For security problems, follow [SECURITY.md](SECURITY.md) instead.

## Make a change

clipsync has three parts:

- `cmd/clipsync/`: the desktop service for macOS and Windows.
- `relay/`: the relay, a small HTTP server that passes sealed clips between devices.
- `android/`: the Android app's Kotlin and Jetpack Compose UI and OS integration.

`internal/client/` holds the shared relay client and encryption. Android calls it through the bindings in `androidbridge/`. `internal/protocol/` defines the shared wire limits and routes.

You need [Go](https://go.dev/dl/) 1.27.4 or later. For Android, also install JDK 21, the Android SDK, and NDK `27.2.12479018`. Set `ANDROID_HOME` to the SDK directory. Gradle builds the Go Android library automatically.

```sh
make install   # build clipsync and put it in ~/.local/bin
make test      # run Go tests and Android lint
```

Before you open a pull request:

1. Install the hooks with [prek](https://github.com/j178/prek): `prek install`. They run `gofmt`, Go vet and tests, and Android lint.
2. Run `make test`.
3. If you changed Go or Android dependencies, update `scripts/licenses.sh` or `android/licenses.md` as needed. Run `make licenses` and commit `THIRD_PARTY_LICENSES.md`.
4. Keep pull requests small and focused on one change.

## Code style

- Follow the code next to your change.
- Use comments to explain why, not what.

Every merge to `main` that changes the desktop service or the app ships a release to all users, and every change to the relay deploys it, so `main` must always work.

By contributing, you agree that your work is released under the [MIT License](LICENSE). Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).
