# ── Build stage ─────────────────────────────────────────────────────────────
FROM golang:1.24-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o tracker .

# ── Runtime stage ────────────────────────────────────────────────────────────
# Use a Debian image with Chromium available so rod can use the system browser
# instead of downloading its own binary at startup.
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        chromium \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/tracker .

# Tell rod to use the system Chromium instead of downloading one
ENV CHROME_PATH=/usr/bin/chromium
# Chromium flags for containerised / headless environments
ENV CHROMIUM_FLAGS="--no-sandbox --disable-dev-shm-usage --disable-gpu"

ENTRYPOINT ["./tracker"]
