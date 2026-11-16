# syntax=docker/dockerfile:1
FROM golang:1.27.4-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The build cache only helps local rebuilds (CI caches layers, not mounts).
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -tags nodynamic -trimpath -ldflags="-s -w" -o slackpack .

FROM alpine:3.24.4

RUN apk --no-cache add ca-certificates dcron tini

WORKDIR /root/

COPY migrations ./migrations
COPY --from=builder /app/slackpack .

# Job output goes to PID 1's (tini's) stdout/stderr, i.e. `docker logs`.
RUN echo "0 2 * * * /root/slackpack > /proc/1/fd/1 2> /proc/1/fd/2" > /etc/crontabs/root

# tini forwards SIGTERM, so the container stops right away.
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["crond", "-f", "-L/dev/stderr"]
