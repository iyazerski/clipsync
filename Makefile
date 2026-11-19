install:
	mkdir -p $(HOME)/.local/bin
	CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags '-s -w' -o $(HOME)/.local/bin/clipsync ./cmd/clipsync

test: test-go test-android

test-go:
	go test -mod=readonly ./...

test-android:
	cd android && ./gradlew -q lintRelease testDebugUnitTest

licenses:
	sh scripts/licenses.sh > THIRD_PARTY_LICENSES.md.tmp
	mv THIRD_PARTY_LICENSES.md.tmp THIRD_PARTY_LICENSES.md
