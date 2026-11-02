install:
	mkdir -p $(HOME)/.local/bin
	cargo install --path . --locked --root $(HOME)/.local

test:
	cargo test --workspace --locked
	cd android && ./gradlew -q testDebugUnitTest
