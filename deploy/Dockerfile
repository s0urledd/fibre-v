# Build the Go binaries and the static site; run any of the five processes.
FROM golang:1.26-bookworm AS go
WORKDIR /src
COPY . .
ENV GOTOOLCHAIN=auto
# .git is not in the build context, so the revision comes in as an argument:
# docker build --build-arg REVISION=$(git rev-parse --short=12 HEAD) ...
ARG REVISION=
RUN cd fibre-sentinel && go build -ldflags "-X github.com/plsgiveup/fibre/fibre-sentinel/internal/status.revision=${REVISION}" -o /out/ ./cmd/...

FROM node:22-bookworm AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ .
RUN npm run build

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=go /out/ /usr/local/bin/
COPY --from=web /src/web/out/ /var/www/fibre-observer/
COPY fibre-sentinel/observer/policy/policy.example.yaml /etc/fibre-observer/policy.example.yaml
COPY deploy/publishers.yaml.example /etc/fibre-observer/publishers.yaml.example
COPY fibre-sentinel/observer/policy/policy.mocha.yaml /etc/fibre-observer/policy.mocha.yaml
RUN useradd --system --uid 10001 --home /data fibre-observer && install -d -o fibre-observer /data
USER fibre-observer
VOLUME /data
