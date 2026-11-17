install:
	mkdir -p $(HOME)/.local/bin
	cargo install --path . --locked --root $(HOME)/.local

test:
	cargo test --workspace --locked
	cd android && ./gradlew -q testDebugUnitTest

# Regenerate THIRD_PARTY_LICENSES.md after changing dependencies; CI checks it's current.
licenses:
	{ cargo about generate --workspace --locked --fail about.hbs && cat android/licenses.md; } > THIRD_PARTY_LICENSES.md.tmp
	mv THIRD_PARTY_LICENSES.md.tmp THIRD_PARTY_LICENSES.md
