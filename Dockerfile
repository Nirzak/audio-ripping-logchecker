FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY *.go .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o logchecker-web .

FROM alpine:3.21

ENV DEBIAN_FRONTEND=noninteractive

RUN apk add --no-cache su-exec shadow

WORKDIR /app

# Copy the binary
COPY --from=builder /build/logchecker-web /app/logchecker-web

# Copy static assets
COPY templates/ /app/templates/
COPY styles/    /app/styles/
COPY scripts/   /app/scripts/

# Create non-root user and logs directory
RUN addgroup -S appuser && adduser -S -G appuser appuser && \
    mkdir -p /app/logs && \
    chown -R appuser:appuser /app

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 5050

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/app/logchecker-web"]
