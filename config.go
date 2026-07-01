package main

import (
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	maxUploadBytes  = 200 * 1024 // 200 KB
	resultsTTL      = 5 * time.Minute
	cleanupInterval = 2 * time.Minute
	defaultPort     = "5050"
	defaultRPS      = 0.5 // 30 req/min
	defaultBurst    = 10
	// discLookupTimeout bounds the concurrent AccurateRip + gnudb database
	// lookups run during a log check. Kept under the server's 30s WriteTimeout
	// so a slow database never stalls the response.
	discLookupTimeout = 8 * time.Second
)

// Config holds all runtime configuration derived from environment variables.
type Config struct {
	Port      string
	AppRoot   string // empty or "/sub/path" — no trailing slash
	RateRPS   rate.Limit
	RateBurst int
	LogLevel  slog.Level
}

// loadConfig reads configuration from environment variables, applying
// safe defaults for every missing or invalid value.
func loadConfig() Config {
	return Config{
		Port:      envOr("PORT", defaultPort),
		AppRoot:   normalizeSubpath(os.Getenv("SUBPATH")),
		RateRPS:   rate.Limit(envFloat("RATE_LIMIT_RPS", defaultRPS)),
		RateBurst: envInt("RATE_LIMIT_BURST", defaultBurst),
		LogLevel:  parseLogLevel(os.Getenv("LOG_LEVEL")),
	}
}

func normalizeSubpath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return strings.TrimRight(raw, "/")
}

// initLogger creates a structured text logger writing to stdout and,
// optionally, to /app/logs/logchecker.log when that path is writable.
func initLogger(level slog.Level) *slog.Logger {
	writers := []io.Writer{os.Stdout}
	if f, err := os.OpenFile("/app/logs/logchecker.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		writers = append(writers, f)
	}
	h := slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

// --- env helpers ---

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	return f
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}
