package main

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	// Resolve the directory that contains templates/ and styles/ relative to
	// the running binary, with a fallback to the current working directory for
	// development (go run ./...).
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	baseDir := filepath.Dir(exe)

	cfg := loadConfig()
	log := initLogger(cfg.LogLevel)

	tmpl, err := loadTemplate(baseDir)
	if err != nil {
		// Fallback: try the working directory (useful during development).
		tmpl, err = loadTemplate(".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "fatal: cannot load template: %v\n", err)
			os.Exit(1)
		}
	}

	srv := &server{
		cfg:    cfg,
		rl:     newRateLimitMiddleware(cfg.RateRPS, cfg.RateBurst),
		tmpl:   tmpl,
		store:  newResultStore(),
		logger: log,
	}

	mux := http.NewServeMux()

	// register mounts h at pattern and, when a sub-path is configured,
	// also at cfg.AppRoot+pattern.
	register := func(pattern string, h http.Handler) {
		mux.Handle(pattern, h)
		if cfg.AppRoot != "" {
			mux.Handle(cfg.AppRoot+pattern, h)
		}
	}

	// Index — catch-all "/" plus explicit subpath variants.
	indexHandler := srv.rl.wrap(srv.handleIndex)
	mux.HandleFunc("/", indexHandler)
	if cfg.AppRoot != "" {
		mux.HandleFunc(cfg.AppRoot, indexHandler)
		mux.HandleFunc(cfg.AppRoot+"/", indexHandler)
	}

	// API
	register("/api", srv.rl.wrap(srv.handleAPI))

	// Result viewer (one-shot)
	register("/result/", http.HandlerFunc(srv.handleResult))

	// Static assets
	register("/style.css", serveFile(assetPath(baseDir, filepath.Join("styles", "log.css"))))
	register("/main.css", serveFile(assetPath(baseDir, filepath.Join("styles", "main.css"))))
	register("/main.js", serveFile(assetPath(baseDir, filepath.Join("scripts", "main.js"))))
	register("/logo.png", serveFile(assetPath(baseDir, filepath.Join("styles", "logo.png"))))

	addr := "0.0.0.0:" + cfg.Port
	log.Info("starting logchecker",
		"addr", addr,
		"subpath", cfg.AppRoot,
		"rate_rps", float64(cfg.RateRPS),
		"rate_burst", cfg.RateBurst,
	)

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      srv.securityHeaders(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := httpSrv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// loadTemplate parses the index.html template from baseDir/templates/.
func loadTemplate(baseDir string) (*template.Template, error) {
	funcMap := template.FuncMap{
		"getLabel": func(key string) string {
			switch key {
			case "ripper":
				return "Ripper"
			case "ripper_version":
				return "Version"
			case "language":
				return "Language"
			case "checksum_state":
				return "Checksum"
			case "combined_log":
				return "Combined Log"
			case "rdbarr_rip":
				return "Rdbarr Rip"
			default:
				return strings.Title(strings.ReplaceAll(key, "_", " "))
			}
		},
	}
	p := filepath.Join(baseDir, "templates", "index.html")
	return template.New("index.html").Funcs(funcMap).ParseFiles(p)
}

// assetPath resolves rel relative to baseDir, falling back to rel itself
// (relative to the working directory) when the joined path does not exist.
func assetPath(baseDir, rel string) string {
	p := filepath.Join(baseDir, rel)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return rel
}

// serveFile returns a handler that serves a single file at path.
func serveFile(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, path)
	}
}
