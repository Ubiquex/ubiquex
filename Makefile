.PHONY: build install

# build and install both print `ubx version` immediately after rebuilding
# (UBI-63 session 4): a real live finding was a founder re-test that
# silently ran a stale, pre-fix binary and got an identical-looking
# result, only caught afterward via a manual `which`/`version` check.
# Making rebuild+verify one command instead of a remembered multi-step
# sequence is the fix that actually holds -- see CLAUDE.md's own
# "rebuild+reinstall before re-testing any fix" line.

build:
	go build -o ./ubx ./cmd/ubx
	./ubx version

install:
	go install ./cmd/ubx
	ubx version
