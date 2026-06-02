FROM python:3.12-slim AS builder

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y --no-install-recommends curl ca-certificates && \
    rm -rf /var/lib/apt/lists/*


# Fetch logchecker.phar from GitHub releases
RUN curl -fSL -o /tmp/logchecker \
    "https://github.com/Nirzak/logchecker-go/releases/latest/download/logchecker-linux-arm64" && \
    chmod +x /tmp/logchecker

FROM python:3.12-slim

ENV DEBIAN_FRONTEND=noninteractive

# Install PHP CLI (required to run logchecker.phar)
RUN apt-get update && \
    apt-get install -y --no-install-recommends gosu && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Copy logchecker binary from builder
COPY --from=builder /tmp/logchecker /usr/local/bin/logchecker

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Create non-root user and set up logs directory
RUN useradd --create-home appuser && \
    mkdir -p /app/logs && \
    chown -R appuser:appuser /app

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 5050

ENTRYPOINT ["/entrypoint.sh"]
CMD ["gunicorn", "--bind", "0.0.0.0:5050", "--workers", "4", "--log-level", "warning", "--log-file", "/app/logs/logchecker.log", "--access-logfile", "/app/logs/access.log", "--access-logformat", "%({X-Forwarded-For}i)s %(l)s %(u)s %(t)s \"%(r)s\" %(s)s %(b)s \"%(f)s\" \"%(a)s\"", "--disable-redirect-access-to-syslog", "app:app"]
