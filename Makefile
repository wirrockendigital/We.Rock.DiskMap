GO ?= go

# Stable entrypoints are shared by local work and the future CI pipeline.
.PHONY: lint typecheck test build qa
lint:
	@test -z "$$(find cmd internal -name '*.go' -exec gofmt -l {} +)"
	$(GO) vet ./...
	npm --prefix web run lint
typecheck:
	npm --prefix web run typecheck
test:
	$(GO) test ./...
	node --test tests/foundation/contracts.test.mjs
	npm --prefix web test
build:
	@python3 scripts/native_targets.py "$(TARGET)"
	GO="$(GO)" sh scripts/build.sh "$(TARGET)"
	python3 scripts/package-spk.py "$(TARGET)"
qa:
	GO="$(GO)" sh scripts/quality/run.sh
