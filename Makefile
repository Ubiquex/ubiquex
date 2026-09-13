.PHONY: build install submodules vendor-assets python-wasi

# build and install both print `ubx version` immediately after rebuilding
# (UBI-63 session 4): a real live finding was a founder re-test that
# silently ran a stale, pre-fix binary and got an identical-looking
# result, only caught afterward via a manual `which`/`version` check.
# Making rebuild+verify one command instead of a remembered multi-step
# sequence is the fix that actually holds -- see CLAUDE.md's own
# "rebuild+reinstall before re-testing any fix" line.
#
# install's own PATH check (UBI-63 session 5): `go install` writes to
# $(go env GOPATH)/bin, which found live to NOT be the same `ubx` a
# separately-installed copy elsewhere on PATH (/usr/local/bin, in that
# session's own case) resolves to -- a rebuild that silently updates a
# binary nothing actually runs, worse than session 4's own stale-binary
# finding because `ubx version` right after still looked correct (it
# printed the OLD binary's own, unrelated version, giving no signal
# anything was wrong). This fails loudly instead of printing a
# version that isn't the one you're about to run.
#
# submodules (UBI-139): sdk/ts/ and sdk/py/ are real git submodules
# (github.com/Ubiquex/ubx-sdk-typescript, ubx-sdk-python) -- their own
# real source is a go:embed build input for tseval/pyeval (the runtime
# ubx's own hermetic evaluator actually executes against), so a plain
# `git clone` (no --recurse-submodules) leaves both empty and `go
# build` fails with a confusing "no required module provides package"
# error that never mentions submodules at all -- live-verified this
# session, not a hypothetical. build/install both depend on this target
# so the fix is automatic, not a remembered extra step.
submodules:
	git submodule update --init --recursive

# vendor-assets re-copies the three files tseval/pyeval embed out of the
# sdk/ts and sdk/py submodules (UBI-254).
#
# The copies exist because the Go module proxy zips a repository WITHOUT
# submodule contents, so github.com/ubiquex/ubiquex/blueprint could not
# be imported by anyone outside this repository at all: the packages it
# needs were absent from every published version.
#
# The submodule is the source. Never edit a vendored copy: run this,
# which is what tseval/pyeval's own drift tests tell you to do when they
# catch a divergence.
vendor-assets: submodules
	cp sdk/ts/evaluator/guards.ts tseval/vendored/evaluator/guards.ts
	cp sdk/ts/runtime/src/index.ts tseval/vendored/runtime/src/index.ts
	cp sdk/py/ubx_sdk/__init__.py pyeval/vendored/ubx_sdk/__init__.py
	@echo "re-synced the vendored evaluator assets from sdk/ts and sdk/py"

# python-wasi downloads and caches the pinned CPython-WASI build the
# Python evaluator needs (UBI-255).
#
# Running it is optional locally, since pyeval acquires the asset on
# first use anyway. It exists so CI can do that acquisition in a named
# step: an outage at the third-party release URL then fails there,
# honestly, instead of surfacing as a scatter of unrelated-looking test
# failures in whichever packages happen to evaluate Python.
python-wasi:
	go run ./internal/tools/prefetchpython

build: submodules
	go build -o ./ubx ./cmd/ubx
	./ubx version

install: submodules
	go install ./cmd/ubx
	@installed="$$(go env GOPATH)/bin/ubx"; \
	onpath="$$(command -v ubx || true)"; \
	if [ "$$onpath" != "$$installed" ]; then \
		echo "ERROR: go install wrote $$installed, but \`ubx\` on your PATH resolves to $${onpath:-<not found>} instead."; \
		echo "  This rebuild is NOT what \`ubx\` actually runs -- your PATH doesn't put $$(go env GOPATH)/bin ahead of it."; \
		echo "  Fix one of: add $$(go env GOPATH)/bin to PATH ahead of $$(dirname "$$onpath" 2>/dev/null || echo '<that other location>'); or: cp $$installed $$onpath"; \
		exit 1; \
	fi; \
	echo "$$onpath: $$($$onpath version)"
