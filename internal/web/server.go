// Package web serves a read-only, filterable page listing everything in the database.
// All assets are embedded in the binary.
package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

//go:embed static
var staticFiles embed.FS

var indexTmpl = template.Must(template.ParseFS(staticFiles, "static/index.html"))

// Row is one repository as sent to the page.
type Row struct {
	Game           string   `json:"game"`
	OriginalSystem string   `json:"system"`
	Kind           string   `json:"kind"`
	OSes           []string `json:"oses"`
	URL            string   `json:"url"`
}

type Server struct {
	store *store.Store
	log   zerolog.Logger
	title string
	index []byte
}

func New(st *store.Store, log zerolog.Logger, title string) (*Server, error) {
	var buf bytes.Buffer
	if err := indexTmpl.Execute(&buf, map[string]string{"Title": title}); err != nil {
		return nil, err
	}
	return &Server{store: st, log: log, title: title, index: buf.Bytes()}, nil
}

func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(staticFiles, "static")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveIndex)
	mux.HandleFunc("GET /api/recomps", s.serveRecomps)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	fileServer := http.FileServerFS(static)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})))
	return securityHeaders(gzipMiddleware(mux))
}

// Run serves on addr until ctx is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	s.log.Info().Str("listen", addr).Msg("Web server listening")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) serveIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(s.index)
}

func (s *Server) serveRecomps(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.All(r.Context())
	if err != nil {
		s.log.Err(err).Msg("Failed to load entries for web page")
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		row := Row{Game: e.Game, OriginalSystem: e.OriginalSystem, Kind: e.Kind, URL: e.RepoURL}
		for _, os := range oses.All { // canonical order
			for _, have := range e.OSes {
				if have == os {
					row.OSes = append(row.OSes, oses.Display(os))
				}
			}
		}
		rows = append(rows, row)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(rows)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (g gzipWriter) Write(b []byte) (int, error) { return g.gz.Write(b) }

// WriteHeader drops Content-Length, which handlers like http.FileServer set for the
// uncompressed body.
func (g gzipWriter) WriteHeader(code int) {
	g.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next.ServeHTTP(gzipWriter{ResponseWriter: w, gz: gz}, r)
	})
}
