FROM python:3.11-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    DEBIAN_FRONTEND=noninteractive \
    MCP_TRANSPORT=sse \
    MCP_HOST=0.0.0.0 \
    MCP_PORT=20129

WORKDIR /app

# Install system dependencies for Camoufox (Firefox) and Playwright
RUN apt-get update && apt-get install -y --no-install-recommends \
    wget \
    curl \
    ca-certificates \
    libgtk-3-0 \
    libdbus-glibc-1-2 \
    libxt6 \
    libx11-xcb1 \
    libasound2 \
    libnss3 \
    libxcomposite1 \
    libxdamage1 \
    libxrandr2 \
    libgbm1 \
    libpango-1.0-0 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libxkbcommon0 \
    fonts-liberation \
    xvfb \
    procps \
    && rm -rf /var/lib/apt/lists/*

# Install uv from official binary
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

# Copy project dependency files and source
COPY pyproject.toml README.md server.py ./

# Sync dependencies and fetch Camoufox browser binary
RUN uv sync --no-install-project && \
    uv add camoufox && \
    uv run python -m camoufox fetch

EXPOSE 20129

CMD ["uv", "run", "python", "server.py", "sse"]
