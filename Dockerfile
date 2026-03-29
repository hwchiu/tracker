# ── Build stage ─────────────────────────────────────────────────────────────
FROM python:3.12-bookworm AS builder

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Pre-fetch Camoufox's anti-detect Firefox binary
RUN python -m camoufox fetch

# Install Patchright's Chromium as a fallback + system deps
RUN python -m patchright install --with-deps chromium

# ── Runtime stage ────────────────────────────────────────────────────────────
FROM python:3.12-slim-bookworm

# System dependencies required by browsers
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        libglib2.0-0 \
        libnss3 \
        libnspr4 \
        libdbus-1-3 \
        libatk1.0-0 \
        libatk-bridge2.0-0 \
        libcups2 \
        libdrm2 \
        libxcomposite1 \
        libxdamage1 \
        libxfixes3 \
        libxrandr2 \
        libgbm1 \
        libpango-1.0-0 \
        libcairo2 \
        libasound2 \
        libxshmfence1 \
        libgtk-3-0 \
        libx11-xcb1 \
        fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy installed Python packages from builder
COPY --from=builder /usr/local/lib/python3.12/site-packages /usr/local/lib/python3.12/site-packages
COPY --from=builder /usr/local/bin /usr/local/bin

# Copy Camoufox browser data
COPY --from=builder /root/.cache /root/.cache

COPY . .

ENTRYPOINT ["python", "main.py"]
