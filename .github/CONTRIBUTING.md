# Contributing

Thanks for helping with clipsync. Bug reports, ideas and pull requests are all welcome.

## Report a bug or ask for a feature

[Open an issue](https://github.com/iyazerski/clipsync/issues/new/choose) and pick a template. For questions, use [Discussions](https://github.com/iyazerski/clipsync/discussions). For security problems, follow [SECURITY.md](../SECURITY.md) instead.

## Make a change

clipsync has three parts:

- `desktop/`: the desktop app for macOS and Windows, built with [MyGo](https://mygo.run). Its texts are in `desktop/locales/`, one file per language.
- `relay/`: the relay, a small HTTP server that passes sealed clips between devices.
- `android/`: the Android app's Kotlin and Jetpack Compose UI and OS integration.

`internal/client/` holds the Go desktop client. The Kotlin app speaks the same wire protocol without native bindings.

The desktop app and relay need [Go](https://go.dev/dl/) 1.27.4 or later. Android needs JDK 21 and the Android SDK. Set `ANDROID_HOME` to the SDK directory.

```sh
make dev       # run the desktop app, and reload it as you change it
make app       # build the desktop app into desktop/build
make test      # run the Go and Android checks
```

Before you open a pull request:

1. Install the hooks with [prek](https://github.com/j178/prek): `prek install`. They run `gofmt`, Go vet and tests, and Android lint and tests.
2. Run `make test`.
3. If you changed the desktop app's texts, run `go tool mygo generate` in `desktop/` and commit `desktop/locales/catalog_gen.go`. If you changed dependencies, run `make licenses` and commit `THIRD_PARTY_LICENSES.md`. For Android ones, update `android/licenses.md` first.
4. Keep pull requests small and focused on one change.

## Code style

- Follow the code next to your change.
- Use comments to explain why, not what.

Every merge to `main` that changes the desktop service or the app ships a release to all users, and every change to the relay deploys it, so `main` must always work. To check release packaging without publishing or deploying, run `gh workflow run release.yml --ref <branch> -f publish=false`. The APK uses the debug signing key for this check.

By contributing, you agree that your work is released under the [MIT License](../LICENSE). Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).
