#!/bin/sh
set -e

# Default to UID/GID 1000 if not provided
PUID=${PUID:-1000}
PGID=${PGID:-1000}

echo "Starting with UID: $PUID, GID: $PGID"

# Update appuser's UID and GID to match the provided values
groupmod -o -g "$PGID" appuser
usermod -o -u "$PUID" appuser

# Fix ownership of app and log directories
chown -R appuser:appuser /app /app/logs

# Run the application as appuser
exec su-exec appuser "$@"
