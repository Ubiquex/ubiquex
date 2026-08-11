.PHONY: build install submodules

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
