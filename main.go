package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Nirzak/logchecker-go/logchecker"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

const (
	maxUploadBytes  = 200 * 1024 // 200 KB
	resultsTTL      = 5 * time.Minute
	cleanupInterval = 2 * time.Minute
)

var (
	applicationRoot string
	logger          *slog.Logger

	allowedExts = map[string]bool{"log": true, "txt": true}

	// rdbarr detection patterns
	reRdbarrFilename = regexp.MustCompile(`Filename\s+[A-Za-z]:\\\d+\.`)

	// log-injection sanitisation
	reANSI    = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	reControl = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`)
)

// ---------------------------------------------------------------------------
// In-memory result store (replaces temp-file HTML results)
// ---------------------------------------------------------------------------

type resultEntry struct {
	html      string
	createdAt time.Time
}

var (
	resultsMu sync.Mutex
	results   = make(map[string]*resultEntry)
)

func storeResult(id, html string) {
	resultsMu.Lock()
	defer resultsMu.Unlock()
	results[id] = &resultEntry{html: html, createdAt: time.Now()}
}

func popResult(id string) (string, bool) {
	resultsMu.Lock()
	defer resultsMu.Unlock()
	e, ok := results[id]
	if !ok {
		return "", false
	}
	delete(results, id)
	return e.html, true
}

func startResultCleaner() {
	go func() {
		for range time.Tick(cleanupInterval) {
			resultsMu.Lock()
			for id, e := range results {
				if time.Since(e.createdAt) > resultsTTL {
					delete(results, id)
				}
			}
			resultsMu.Unlock()
		}
	}()
}

// ---------------------------------------------------------------------------
// Rate limiter (token bucket per IP)
// ---------------------------------------------------------------------------

type ipState struct {
	tokens   float64
	lastSeen time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	states   map[string]*ipState
	rate     float64 // tokens per second
	capacity float64
}

func newRateLimiter(requestsPerMinute int) *rateLimiter {
	rl := &rateLimiter{
		states:   make(map[string]*ipState),
		rate:     float64(requestsPerMinute) / 60.0,
		capacity: float64(requestsPerMinute),
	}
	go func() {
		for range time.Tick(5 * time.Minute) {
			rl.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for ip, s := range rl.states {
				if s.lastSeen.Before(cutoff) {
					delete(rl.states, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	s, ok := rl.states[ip]
	if !ok {
		rl.states[ip] = &ipState{tokens: rl.capacity - 1, lastSeen: now}
		return true
	}
	elapsed := now.Sub(s.lastSeen).Seconds()
	s.tokens = clampMax(rl.capacity, s.tokens+elapsed*rl.rate)
	s.lastSeen = now
	if s.tokens >= 1 {
		s.tokens--
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func clampMax(max, v float64) float64 {
	if v > max {
		return max
	}
	return v
}

func sanitizeForLog(v string) string {
	v = reANSI.ReplaceAllString(v, "")
	v = strings.ReplaceAll(v, "\n", "")
	v = strings.ReplaceAll(v, "\r", "")
	v = reControl.ReplaceAllString(v, "")
	if len(v) > 255 {
		v = v[:255]
	}
	return v
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return sanitizeForLog(strings.TrimSpace(parts[0]))
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		addr = addr[:i]
	}
	return sanitizeForLog(addr)
}

func allowedFile(name string) bool {
	dot := strings.LastIndex(name, ".")
	if dot == -1 {
		return false
	}
	return allowedExts[strings.ToLower(name[dot+1:])]
}

// secureBasename strips directory traversal components.
func secureBasename(name string) string {
	return filepath.Base(filepath.Clean(name))
}

// isRdbarr checks content for rdbarr rip indicators.
func isRdbarr(content string) bool {
	if strings.Contains(content, "Lenovo  Slim_USB_Burner") {
		return true
	}
	return reRdbarrFilename.MatchString(content)
}

// parseRateLimit parses strings like "30 per minute", "5 per second", "100 per hour".
func parseRateLimit(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	parts := strings.Fields(s)
	if len(parts) < 3 {
		return 30
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return 30
	}
	switch parts[2] {
	case "second":
		return n * 60
	case "hour":
		return max(1, n/60)
	default: // minute
		return n
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// writeTempFile writes raw bytes to a temp file and returns its path.
// Caller must remove the file when done.
func writeTempFile(raw []byte, suffix string) (string, error) {
	f, err := os.CreateTemp("", "logchecker-*"+suffix)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// newID generates a 32-char hex ID using /dev/urandom.
func newID() string {
	b := make([]byte, 16)
	f, _ := os.Open("/dev/urandom")
	io.ReadFull(f, b)
	f.Close()
	return fmt.Sprintf("%x", b)
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
// Analysis
// ---------------------------------------------------------------------------

type analysisResult struct {
	Ripper        string   `json:"ripper"`
	RipperVersion string   `json:"ripper_version"`
	Score         int      `json:"score"`
	ChecksumState string   `json:"checksum_state"`
	Details       []string `json:"details"`
	Language      string   `json:"language"`
	IsCombinedLog bool     `json:"is_combined_log"`
	RdbarrRip     string   `json:"rdbarr_rip,omitempty"`
}

// analyze runs the logchecker library against raw file bytes.
// It writes a temp file (required by the library for checksum validation),
// parses it, then cleans up.
func analyze(raw []byte, origSuffix string, rdbarrRip bool) (*analysisResult, string, error) {
	// Write raw bytes to a temp file so the library can read + checksum-validate it.
	tmpPath, err := writeTempFile(raw, origSuffix)
	if err != nil {
		return nil, "", fmt.Errorf("could not create temp file: %w", err)
	}
	defer os.Remove(tmpPath)

	lc := logchecker.New()
	if err := lc.NewFile(tmpPath); err != nil {
		return nil, "", fmt.Errorf("could not load file: %w", err)
	}
	lc.Parse()

	score := lc.GetScore()
	htmlLog := lc.GetLog()

	if rdbarrRip {
		score -= 100
		notice := `<div class="bad">Notice: rdbarr rip detected. Score reduced by 100.</div>`
		htmlLog = notice + "\n" + htmlLog
	}

	res := &analysisResult{
		Ripper:        lc.GetRipper(),
		RipperVersion: lc.GetRipperVersion(),
		Score:         score,
		ChecksumState: lc.GetChecksumState(),
		Details:       lc.GetDetails(),
		Language:      lc.GetLanguage(),
		IsCombinedLog: lc.IsCombinedLog(),
	}
	if rdbarrRip {
		res.Details = append(res.Details, "rdbarr rip detected. Score reduced by 100.")
		res.RdbarrRip = "Yes"
	}

	return res, htmlLog, nil
}

// resultToSummary converts analysisResult into display-friendly key/value pairs.
func resultToSummary(r *analysisResult) []summaryItem {
	items := []summaryItem{
		{Key: "ripper", Value: r.Ripper},
		{Key: "ripper_version", Value: r.RipperVersion},
		{Key: "score", Value: strconv.Itoa(r.Score)},
		{Key: "checksum_state", Value: r.ChecksumState},
		{Key: "language", Value: r.Language},
	}
	if r.IsCombinedLog {
		items = append(items, summaryItem{Key: "combined_log", Value: "Yes"})
	}
	if r.RdbarrRip != "" {
		items = append(items, summaryItem{Key: "rdbarr_rip", Value: r.RdbarrRip})
	}
	return items
}

// ---------------------------------------------------------------------------
// HTML sanitisation (allowlist: span, div, p, strong, em, br + class attr)
// ---------------------------------------------------------------------------

var (
	reUnsafeTags  = regexp.MustCompile(`(?i)<(?!/?(span|div|p|strong|em|br)(\s|>))[^>]*>`)
	reUnsafeAttrs = regexp.MustCompile(`(?i)\s(?:on\w+|href|src|action|formaction|data-\S*)=[^\s>]*`)
)

func sanitizeHTML(raw string) string {
	s := reUnsafeTags.ReplaceAllString(raw, "")
	s = reUnsafeAttrs.ReplaceAllString(s, "")
	return s
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

type server struct {
	rl   *rateLimiter
	tmpl *template.Template
}

func (s *server) withRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.rl.allow(clientIP(r)) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func (s *server) addSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' fonts.googleapis.com 'unsafe-inline'; font-src fonts.gstatic.com; frame-ancestors 'none';")
		next.ServeHTTP(w, r)
	})
}

// readUpload reads the uploaded file field, returning raw bytes and the safe filename.
func readUpload(r *http.Request, ip string) (raw []byte, safeName string, errMsg string) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		if strings.Contains(err.Error(), "too large") || strings.Contains(err.Error(), "request body too large") {
			logger.Warn("file upload exceeded limit", "ip", ip)
			return nil, "", "File size exceeds the maximum limit of 200 KB. Please upload a smaller file."
		}
		return nil, "", "Failed to parse form."
	}

	file, header, err := r.FormFile("logfile")
	if err != nil {
		logger.Info("upload with no file", "ip", ip)
		return nil, "", "No file part. Use the file selector."
	}
	defer file.Close()

	if header.Filename == "" {
		logger.Info("upload with empty filename", "ip", ip)
		return nil, "", "No file selected."
	}

	safeName = secureBasename(header.Filename)
	if !allowedFile(safeName) {
		logger.Warn("rejected disallowed file type", "ip", ip, "file", sanitizeForLog(safeName))
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

// handleIndex handles GET / and POST /
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Reject requests to paths that aren't exactly the index.
	if r.URL.Path != "/" && r.URL.Path != applicationRoot+"/" && r.URL.Path != applicationRoot {
		http.NotFound(w, r)
		return
	}

	data := pageData{Subpath: applicationRoot}

	if r.Method == http.MethodGet {
		s.renderPage(w, data)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)
	raw, safeName, errMsg := readUpload(r, ip)
	if errMsg != "" {
		if strings.Contains(errMsg, "200 KB") {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
		data.Error = errMsg
		s.renderPage(w, data)
		return
	}

	safeNameLog := sanitizeForLog(safeName)
	logger.Info("checking file", "ip", ip, "file", safeNameLog)

	// rdbarr check: decode content to string for pattern matching.
	// We pass raw bytes to analyse() so the library handles encoding itself.
	contentStr := string(raw)
	rdbarr := isRdbarr(contentStr)

	ext := filepath.Ext(safeName)
	res, htmlLog, err := analyze(raw, ext, rdbarr)
	if err != nil {
		data.Error = "Failed to process the file. Please check your input and try again."
		logger.Error("analysis failed", "ip", ip, "file", safeNameLog, "err", err)
		s.renderPage(w, data)
		return
	}

	logger.Info("analysis complete", "ip", ip, "file", safeNameLog, "score", res.Score)

	sanitized := sanitizeHTML(htmlLog)
	wrappedHTML := buildResultHTML(sanitized, applicationRoot)
	resultID := newID()
	storeResult(resultID, wrappedHTML)

	data.Summary = resultToSummary(res)
	data.Details = res.Details
	data.ResultID = resultID
	s.renderPage(w, data)
}

// handleAPI handles POST /api
func (s *server) handleAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		jsonError(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)
	raw, safeName, errMsg := readUpload(r, ip)
	if errMsg != "" {
		code := http.StatusBadRequest
		if strings.Contains(errMsg, "200 KB") {
			code = http.StatusRequestEntityTooLarge
		}
		jsonError(w, errMsg, code)
		return
	}

	safeNameLog := sanitizeForLog(safeName)
	logger.Info("API: checking file", "ip", ip, "file", safeNameLog)

	contentStr := string(raw)
	rdbarr := isRdbarr(contentStr)

	ext := filepath.Ext(safeName)
	res, _, err := analyze(raw, ext, rdbarr)
	if err != nil {
		logger.Error("API: analysis failed", "ip", ip, "file", safeNameLog, "err", err)
		jsonError(w, "Failed to process the file. Please check your input and try again.", http.StatusInternalServerError)
		return
	}

	logger.Info("API: analysis complete", "ip", ip, "file", safeNameLog, "score", res.Score)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(res)
}

// handleServeHTML handles GET /result/<id>
func (s *server) handleServeHTML(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	idx := strings.LastIndex(path, "/result/")
	if idx == -1 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	id := path[idx+len("/result/"):]

	// Validate: only lowercase hex characters
	if len(id) == 0 {
		http.Error(w, "Invalid result ID", http.StatusBadRequest)
		return
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			http.Error(w, "Invalid result ID", http.StatusBadRequest)
			return
		}
	}

	html, ok := popResult(id)
	if !ok {
		http.Error(w, "No result available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, html)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *server) renderPage(w http.ResponseWriter, data pageData) {
	if err := s.tmpl.Execute(w, data); err != nil {
		logger.Error("template render error", "err", err)
	}
}

// ---------------------------------------------------------------------------
// Static file handler
// ---------------------------------------------------------------------------

func serveFile(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, path)
	}
}

// ---------------------------------------------------------------------------
// HTML result wrapper
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Template
// ---------------------------------------------------------------------------

func loadTemplate(baseDir string) (*template.Template, error) {
	funcMap := template.FuncMap{
		"capitalize": func(s string) string {
			s = strings.ReplaceAll(s, "_", " ")
			if len(s) == 0 {
				return s
			}
			return strings.ToUpper(s[:1]) + s[1:]
		},
	}
	tmplPath := filepath.Join(baseDir, "templates", "index.html")
	return template.New("index.html").Funcs(funcMap).ParseFiles(tmplPath)
}

// assetPath resolves a relative asset sub-path against baseDir,
// falling back to the working directory.
func assetPath(baseDir, rel string) string {
	p := filepath.Join(baseDir, rel)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return rel // relative to cwd (dev mode)
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	// --- Determine base directory (where the binary lives) ---
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	baseDir := filepath.Dir(exe)

	// --- Logging ---
	logLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warning", "warn":
		level = slog.LevelWarn
	default:
		level = slog.LevelError
	}

	writers := []io.Writer{os.Stdout}
	if logFile, err := os.OpenFile("/app/logs/logchecker.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		writers = append(writers, logFile)
	}
	handler := slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: level})
	logger = slog.New(handler)

	// --- Config ---
	rawSubpath := strings.TrimSpace(os.Getenv("SUBPATH"))
	switch {
	case rawSubpath == "" || rawSubpath == "/":
		applicationRoot = ""
	default:
		if !strings.HasPrefix(rawSubpath, "/") {
			rawSubpath = "/" + rawSubpath
		}
		applicationRoot = strings.TrimRight(rawSubpath, "/")
	}

	rateLimitStr := os.Getenv("RATE_LIMIT")
	if rateLimitStr == "" {
		rateLimitStr = "30 per minute"
	}
	rpm := parseRateLimit(rateLimitStr)
	rl := newRateLimiter(rpm)

	// --- Template ---
	tmpl, err := loadTemplate(baseDir)
	if err != nil {
		// fallback: try cwd
		tmpl, err = loadTemplate(".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "fatal: cannot load template: %v\n", err)
			os.Exit(1)
		}
	}

	srv := &server{rl: rl, tmpl: tmpl}
	startResultCleaner()

	// --- Routes ---
	mux := http.NewServeMux()

	// register registers a handler under pattern, and also under
	// applicationRoot+pattern when a subpath is configured.
	register := func(pattern string, h http.Handler) {
		mux.Handle(pattern, h)
		if applicationRoot != "" {
			mux.Handle(applicationRoot+pattern, h)
		}
	}

	// Index — exact paths only (wildcard "/" catches everything else)
	indexHandler := srv.withRateLimit(srv.handleIndex)
	mux.HandleFunc("/", indexHandler) // catch-all (also serves index at /)
	if applicationRoot != "" {
		mux.HandleFunc(applicationRoot, indexHandler)
		mux.HandleFunc(applicationRoot+"/", indexHandler)
	}

	// API
	register("/api", srv.withRateLimit(srv.handleAPI))

	// Result
	register("/result/", http.HandlerFunc(srv.handleServeHTML))

	// Static assets
	register("/style.css", serveFile(assetPath(baseDir, filepath.Join("styles", "log.css"))))
	register("/main.css", serveFile(assetPath(baseDir, filepath.Join("styles", "main.css"))))
	register("/main.js", serveFile(assetPath(baseDir, filepath.Join("scripts", "main.js"))))

	// --- Server ---
	port := os.Getenv("PORT")
	if port == "" {
		port = "5050"
	}
	addr := "0.0.0.0:" + port

	logger.Info("starting logchecker", "addr", addr, "subpath", applicationRoot, "rateLimit", rateLimitStr)

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      srv.addSecurityHeaders(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := httpSrv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
