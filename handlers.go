package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ---------------------------------------------------------------------------
// Log-injection sanitisation regexes
// ---------------------------------------------------------------------------

var (
	reANSI    = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	reControl = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`)
)

// ---------------------------------------------------------------------------
// rdbarr detection
// ---------------------------------------------------------------------------

var (
	reRdbarrFilename = regexp.MustCompile(`Filename\s+[A-Za-z]:\\\d+\.`)
	allowedExts      = map[string]bool{"log": true, "txt": true}
)

func isRdbarr(content string) bool {
	return strings.Contains(content, "Lenovo  Slim_USB_Burner") ||
		reRdbarrFilename.MatchString(content)
}

// decodeToUTF8 converts raw log bytes to a UTF-8 string suitable for pattern
// matching. EAC logs are commonly saved as UTF-16 LE with a BOM (0xFF 0xFE);
// without decoding, ASCII substring searches fail because every character is
// interleaved with a null byte.
func decodeToUTF8(raw []byte) string {
	if len(raw) < 2 {
		return string(raw)
	}
	switch {
	case raw[0] == 0xff && raw[1] == 0xfe: // UTF-16 LE BOM
		return utf16Decode(raw[2:], false)
	case raw[0] == 0xfe && raw[1] == 0xff: // UTF-16 BE BOM
		return utf16Decode(raw[2:], true)
	}
	return string(raw)
}

// utf16Decode converts a BOM-stripped UTF-16 byte slice to a UTF-8 string.
// bigEndian selects byte order; surrogate pairs are handled correctly.
func utf16Decode(b []byte, bigEndian bool) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	runes := make([]rune, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u := readUTF16Unit(b, i, bigEndian)
		// Handle surrogate pairs (U+D800–U+DFFF)
		if u >= 0xD800 && u <= 0xDBFF && i+3 < len(b) {
			lo := readUTF16Unit(b, i+2, bigEndian)
			if lo >= 0xDC00 && lo <= 0xDFFF {
				runes = append(runes, rune(u-0xD800)<<10|rune(lo-0xDC00)+0x10000)
				i += 2
				continue
			}
		}
		runes = append(runes, rune(u))
	}
	return string(runes)
}

// readUTF16Unit reads one UTF-16 code unit from b at offset i.
func readUTF16Unit(b []byte, i int, bigEndian bool) uint16 {
	if bigEndian {
		return uint16(b[i])<<8 | uint16(b[i+1])
	}
	return uint16(b[i]) | uint16(b[i+1])<<8
}

// ---------------------------------------------------------------------------
// Template data types
// ---------------------------------------------------------------------------

type summaryItem struct {
	Key   string
	Value string
}

type pageData struct {
	Subpath  string
	Error    string
	Summary  []summaryItem
	Details  []string
	ResultID string
}

// ---------------------------------------------------------------------------
// Per-IP rate limiter backed by golang.org/x/time/rate
// ---------------------------------------------------------------------------

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type rateLimitMiddleware struct {
	mu       sync.Mutex
	limiters map[string]*ipLimiter
	r        rate.Limit
	b        int
}

func newRateLimitMiddleware(r rate.Limit, b int) *rateLimitMiddleware {
	m := &rateLimitMiddleware{
		limiters: make(map[string]*ipLimiter),
		r:        r,
		b:        b,
	}
	go m.clean()
	return m
}

// allow returns whether the given IP has a token available.
func (m *rateLimitMiddleware) allow(ip string) bool {
	m.mu.Lock()
	il, ok := m.limiters[ip]
	if !ok {
		il = &ipLimiter{limiter: rate.NewLimiter(m.r, m.b)}
		m.limiters[ip] = il
	}
	il.lastSeen = time.Now()
	m.mu.Unlock()
	return il.limiter.Allow()
}

// clean prunes limiters that have been idle for more than 10 minutes.
func (m *rateLimitMiddleware) clean() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		m.mu.Lock()
		for ip, il := range m.limiters {
			if il.lastSeen.Before(cutoff) {
				delete(m.limiters, ip)
			}
		}
		m.mu.Unlock()
	}
}

// wrap wraps a HandlerFunc with the rate-limit check.
func (m *rateLimitMiddleware) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !m.allow(clientIP(r)) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

type server struct {
	cfg    Config
	rl     *rateLimitMiddleware
	tmpl   *template.Template
	store  *resultStore
	logger *slog.Logger
}

// securityHeaders is a middleware that injects CSP and related headers.
func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' fonts.googleapis.com 'unsafe-inline'; font-src fonts.gstatic.com; frame-ancestors 'none';")
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleIndex serves GET / (upload form) and POST / (file submission).
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	root := s.cfg.AppRoot
	if r.URL.Path != "/" && r.URL.Path != root+"/" && r.URL.Path != root {
		http.NotFound(w, r)
		return
	}

	data := pageData{Subpath: root}

	switch r.Method {
	case http.MethodGet:
		s.render(w, data)
	case http.MethodPost:
		s.handleUpload(w, r, &data)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request, data *pageData) {
	ip := clientIP(r)
	raw, safeName, errMsg := s.readUpload(w, r, ip)
	if errMsg != "" {
		if strings.Contains(errMsg, "200 KB") {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
		data.Error = errMsg
		s.render(w, *data)
		return
	}

	safeLog := sanitizeForLog(safeName)
	s.logger.Info("checking file", "ip", ip, "file", safeLog)

	rdbarr := isRdbarr(decodeToUTF8(raw))
	res, htmlLog, err := analyze(raw, filepath.Ext(safeName), rdbarr)
	if err != nil {
		s.logger.Error("analysis failed", "ip", ip, "file", safeLog, "err", err)
		data.Error = "Failed to process the file. Please check your input and try again."
		s.render(w, *data)
		return
	}

	s.logger.Info("analysis complete", "ip", ip, "file", safeLog, "score", res.Score)

	id := newID()
	s.store.set(id, buildResultHTML(sanitizeHTML(htmlLog), s.cfg.AppRoot))

	data.Summary = resultToSummary(res)
	data.Details = res.Details
	data.ResultID = id
	s.render(w, *data)
}

// handleAPI handles POST /api — returns JSON.
func (s *server) handleAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		jsonError(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)
	raw, safeName, errMsg := s.readUpload(w, r, ip)
	if errMsg != "" {
		code := http.StatusBadRequest
		if strings.Contains(errMsg, "200 KB") {
			code = http.StatusRequestEntityTooLarge
		}
		jsonError(w, errMsg, code)
		return
	}

	safeLog := sanitizeForLog(safeName)
	s.logger.Info("API: checking file", "ip", ip, "file", safeLog)

	rdbarr := isRdbarr(decodeToUTF8(raw))
	res, _, err := analyze(raw, filepath.Ext(safeName), rdbarr)
	if err != nil {
		s.logger.Error("API: analysis failed", "ip", ip, "file", safeLog, "err", err)
		jsonError(w, "Failed to process the file. Please check your input and try again.", http.StatusInternalServerError)
		return
	}

	s.logger.Info("API: analysis complete", "ip", ip, "file", safeLog, "score", res.Score)
	json.NewEncoder(w).Encode(res) //nolint:errcheck
}

// handleResult handles GET /result/<id> — serves a one-shot pre-rendered HTML result.
func (s *server) handleResult(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	idx := strings.LastIndex(path, "/result/")
	if idx == -1 {
		http.NotFound(w, r)
		return
	}
	id := path[idx+len("/result/"):]

	if !isHexID(id) {
		http.Error(w, "Invalid result ID", http.StatusBadRequest)
		return
	}

	html, ok := s.store.pop(id)
	if !ok {
		http.Error(w, "No result available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, html) //nolint:errcheck
}

func (s *server) render(w http.ResponseWriter, data pageData) {
	if err := s.tmpl.Execute(w, data); err != nil {
		s.logger.Error("template render error", "err", err)
	}
}

// ---------------------------------------------------------------------------
// Shared upload reader
// ---------------------------------------------------------------------------

// readUpload parses the multipart body, validates the file, and returns raw bytes.
// It fixes the original bug of passing nil as the ResponseWriter to MaxBytesReader.
func (s *server) readUpload(w http.ResponseWriter, r *http.Request, ip string) (raw []byte, safeName string, errMsg string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "too large") || strings.Contains(msg, "request body too large") {
			s.logger.Warn("upload exceeded limit", "ip", ip)
			return nil, "", "File size exceeds the maximum limit of 200 KB. Please upload a smaller file."
		}
		return nil, "", "Failed to parse form."
	}

	file, header, err := r.FormFile("logfile")
	if err != nil {
		s.logger.Info("upload with no file", "ip", ip)
		return nil, "", "No file part. Use the file selector."
	}
	defer file.Close()

	if header.Filename == "" {
		s.logger.Info("upload with empty filename", "ip", ip)
		return nil, "", "No file selected."
	}

	safeName = filepath.Base(filepath.Clean(header.Filename))
	if !allowedFile(safeName) {
		s.logger.Warn("rejected disallowed file type", "ip", ip, "file", sanitizeForLog(safeName))
		return nil, "", "File type not allowed. Only .log and .txt files are permitted."
	}

	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		return nil, "", "Failed to read file."
	}
	if len(data) > maxUploadBytes {
		return nil, "", "File size exceeds the maximum limit of 200 KB. Please upload a smaller file."
	}

	return data, safeName, ""
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func allowedFile(name string) bool {
	dot := strings.LastIndex(name, ".")
	return dot != -1 && allowedExts[strings.ToLower(name[dot+1:])]
}

// isHexID reports whether id is a non-empty lowercase/uppercase hex string.
func isHexID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// newID returns a cryptographically random 32-character hex string.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// sanitizeForLog strips ANSI escapes, control characters, and newlines
// to prevent log-injection attacks. Output is capped at 255 bytes.
func sanitizeForLog(v string) string {
	v = reANSI.ReplaceAllString(v, "")
	v = strings.NewReplacer("\n", "", "\r", "").Replace(v)
	v = reControl.ReplaceAllString(v, "")
	if len(v) > 255 {
		v = v[:255]
	}
	return v
}

// clientIP extracts the real client IP, preferring X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return sanitizeForLog(strings.TrimSpace(ip))
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		addr = addr[:i]
	}
	return sanitizeForLog(addr)
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, msg string, code int) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

// buildResultHTML wraps sanitized log output in a minimal standalone HTML page.
func buildResultHTML(sanitizedBody, subpath string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Logchecker Result</title>
  <link rel="stylesheet" href="%s/style.css">
</head>
<body>
  <pre>%s</pre>
</body>
</html>`, subpath, sanitizedBody)
}
