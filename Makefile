dev:
	go tool mygo dev desktop

app:
	go tool mygo build desktop

test: test-go test-android

test-go:
	go test -mod=readonly ./...

test-android:
	cd android && ./gradlew -q lintRelease testDebugUnitTest

licenses:
	sh scripts/licenses.sh > THIRD_PARTY_LICENSES.md.tmp
	mv THIRD_PARTY_LICENSES.md.tmp THIRD_PARTY_LICENSES.md
