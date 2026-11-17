# Contributing

Thanks for helping with clipsync. Bug reports, ideas and pull requests are all welcome.

## Report a bug or ask for a feature

[Open an issue](https://github.com/iyazerski/clipsync/issues/new/choose) and pick a template. For questions, use [Discussions](https://github.com/iyazerski/clipsync/discussions). For security problems, follow [SECURITY.md](SECURITY.md) instead.

## Make a change

clipsync has three parts:

- `src/`: the desktop service for macOS and Windows.
- `relay/`: the relay, a small HTTP server that passes sealed clips between devices.
- `android/`: the Android app, in Kotlin and Jetpack Compose.

`proto/` holds what the relay and the desktop service agree on, and the app follows it too. The desktop service and the app seal clips the same way and share their test vectors, so a change to one needs the same change to the other.

You need [Rust](https://rustup.rs). For the app, you also need JDK 21 and the Android SDK.

```sh
make install   # build clipsync and put it in ~/.local/bin
make test      # test the desktop service, the relay and the app
```

Before you open a pull request:

1. Install the hooks with [prek](https://github.com/j178/prek): `prek install`. They run `cargo fmt`, `cargo clippy`, and Android lint and tests.
2. Run `make test`.
3. If you changed Rust dependencies, run `make licenses` and commit `THIRD_PARTY_LICENSES.md`. It needs [cargo-about](https://github.com/EmbarkStudios/cargo-about). If you changed the app's dependencies, update `android/licenses.md` first.
4. Keep pull requests small and focused on one change.

## Code style

- Follow the code next to your change.
- Give every item a doc comment, and every `unsafe` block a `// SAFETY:` comment.
- Use comments to say why, not what.

Every merge to `main` that changes the desktop service or the app ships a release to all users, and every change to the relay deploys it, so `main` must always work.

By contributing, you agree that your work is released under the [MIT License](LICENSE). Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).
