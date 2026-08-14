# syntax=docker/dockerfile:1

# Build stage: compiles the real ubx binary this image serves as
# `ubx server` -- the same build `make build` produces, not a separate
# build path (UBI-28: "Same binary as ubx itself, not a second
# codebase").
FROM golang:1.26-bookworm AS build

WORKDIR /src

# go.mod/go.sum copied first so `go mod download` layer-caches across
# builds that only change source, not dependencies.
COPY go.mod go.sum ./
RUN go mod download

# sdk/ts/ and sdk/py/ are real git submodules
# (github.com/Ubiquex/ubx-sdk-typescript, ubx-sdk-python) --
# tseval/pyeval go:embed their own real source, so the build context
# passed to `docker build` must already have them populated
# (`git submodule update --init --recursive` on the host first, exactly
# what the Makefile's own `build` target already requires -- see
# Makefile). This image deliberately does not fetch them itself during
# the build; doing so would need GitHub credentials baked into the
# build process, a real anti-pattern this Dockerfile avoids.
COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ubx ./cmd/ubx

# Runtime stage: a real, current, minimal Debian base -- not scratch,
# since ubx server genuinely needs two real runtime dependencies scratch
# has neither of: the real `git` binary (server/repo.go shells out to it
# for every real clone/fetch), and real CA certificates (every GitHub
# API call, and every git-over-HTTPS clone/fetch, is TLS).
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/ubx /usr/local/bin/ubx

# A real, non-root user -- ubx server has no reason to run as root.
# Config.WorkDir's own real default (/var/lib/ubx-server/repos, where
# every watched repo actually gets cloned) is created here, owned by
# that user, not root, from the start.
RUN useradd --create-home --shell /usr/sbin/nologin ubx \
    && mkdir -p /var/lib/ubx-server/repos \
    && chown -R ubx:ubx /var/lib/ubx-server
USER ubx

EXPOSE 8080

ENTRYPOINT ["ubx", "server"]
