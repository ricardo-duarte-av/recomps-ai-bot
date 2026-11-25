package web

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	st, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	err = st.Add(context.Background(), store.Entry{RepoURL: "https://github.com/pret/pokeemerald", Game: "Pokémon Emerald",
		OriginalSystem: "Game Boy Advance", Kind: "decomp", OSes: []string{"macos", "windows", "linux"}}, "@mod:x")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetIGDBLink(context.Background(), store.IGDBGameKey("Pokémon Emerald", "Game Boy Advance"),
		"https://www.igdb.com/games/pokemon-emerald-version", 1234); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIGDBCover(context.Background(), 1234, []byte("\xff\xd8\xff\xe0fake jpeg"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, zerolog.Nop(), "Test <Title>")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string, gz bool) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if gz {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	resp, err := http.DefaultTransport.RoundTrip(req) // no transparent decompression
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var r io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		if r, err = gzip.NewReader(resp.Body); err != nil {
			t.Fatal(err)
		}
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func TestIndexEscapesTitle(t *testing.T) {
	srv := newTestServer(t)
	resp, body := get(t, srv.URL+"/", false)
	if resp.StatusCode != 200 || !strings.Contains(body, "Test &lt;Title&gt;") {
		t.Fatalf("status %d body %q", resp.StatusCode, body[:min(len(body), 300)])
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("missing CSP")
	}
}

func TestAPI(t *testing.T) {
	srv := newTestServer(t)
	_, body := get(t, srv.URL+"/api/recomps", true)
	var rows []Row
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err, body)
	}
	if len(rows) != 1 || rows[0].Game != "Pokémon Emerald" || strings.Join(rows[0].OSes, ",") != "Windows,Linux,macOS" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].GameURL != "https://www.igdb.com/games/pokemon-emerald-version" || rows[0].SystemURL != "" {
		t.Errorf("IGDB links = %q, %q", rows[0].GameURL, rows[0].SystemURL)
	}
	if rows[0].Cover != "covers/1234" {
		t.Errorf("cover = %q", rows[0].Cover)
	}
}

func TestCover(t *testing.T) {
	srv := newTestServer(t)
	resp, body := get(t, srv.URL+"/covers/1234", true)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" ||
		resp.Header.Get("Content-Encoding") != "" || !strings.HasSuffix(body, "fake jpeg") {
		t.Errorf("cover: status %d type %q enc %q body %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"), body)
	}
	for _, p := range []string{"/covers/999", "/covers/abc", "/covers/-1"} {
		if resp, _ := get(t, srv.URL+p, false); resp.StatusCode != 404 {
			t.Errorf("%s: status %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestAssetsGzipped(t *testing.T) {
	srv := newTestServer(t)
	for _, f := range []string{"app.js", "style.css"} {
		resp, body := get(t, srv.URL+"/assets/"+f, true)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" || len(body) < 1000 {
			t.Errorf("%s: status %d enc %q len %q body %d", f, resp.StatusCode, resp.Header.Get("Content-Encoding"), resp.Header.Get("Content-Length"), len(body))
		}
	}
	if resp, _ := get(t, srv.URL+"/assets/index.html", false); resp.StatusCode != 404 {
		t.Errorf("raw template should not be served, got %d", resp.StatusCode)
	}
	if resp, _ := get(t, srv.URL+"/nope", false); resp.StatusCode != 404 {
		t.Errorf("unknown path should 404, got %d", resp.StatusCode)
	}
}

func TestAccessLog(t *testing.T) {
	var buf strings.Builder
	st, err := store.Open(filepath.Join(t.TempDir(), "log.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(st, zerolog.New(&buf).Level(zerolog.InfoLevel), "T")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	get(t, srv.URL+"/", true)
	get(t, srv.URL+"/assets/app.js", true)
	get(t, srv.URL+"/covers/1", false)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"path":"/"`) || !strings.Contains(lines[0], `"status":200`) ||
		!strings.Contains(lines[1], `"path":"/covers/1"`) || !strings.Contains(lines[1], `"status":404`) {
		t.Errorf("access log:\n%s", buf.String())
	}
}
