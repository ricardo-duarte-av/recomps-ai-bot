package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestForge starts a TLS server and a client trusting it.
func newTestForge(t *testing.T, mux *http.ServeMux) (*Client, string) {
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(nil, true)
	c.http = srv.Client()
	return c, strings.TrimPrefix(srv.URL, "https://")
}

func fetch(t *testing.T, c *Client, url string) string {
	t.Helper()
	r, err := c.ParseRepo(url)
	if err != nil {
		t.Fatal(err)
	}
	text, err := c.FetchReadme(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestFetchGitea(t *testing.T) {
	mux := http.NewServeMux()
	var host string
	mux.HandleFunc("/api/v1/repos/Owner/Repo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"full_name":"Owner/Repo","default_branch":"dev"}`))
	})
	mux.HandleFunc("/api/v1/repos/Owner/Repo/contents", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref") != "dev" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"LICENSE","type":"file","download_url":"https://` + host + `/x"},
			{"name":"README.md","type":"file","download_url":"https://` + host + `/raw/README.md"}]`))
	})
	mux.HandleFunc("/raw/README.md", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("# Gitea readme")) })
	c, h := newTestForge(t, mux)
	host = h
	if got := fetch(t, c, "https://"+host+"/Owner/Repo"); got != "# Gitea readme" {
		t.Errorf("got %q", got)
	}
}

func TestFetchGitLab(t *testing.T) {
	mux := http.NewServeMux()
	var host string
	mux.HandleFunc("/api/v4/projects/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fsub%2Frepo" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":42,"default_branch":"main","readme_url":"https://` + host + `/group/sub/repo/-/blob/main/README.md"}`))
	})
	mux.HandleFunc("/api/v4/projects/42/repository/files/README.md/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# GitLab readme"))
	})
	c, h := newTestForge(t, mux)
	host = h
	if got := fetch(t, c, "https://"+host+"/group/sub/repo/-/tree/main"); got != "# GitLab readme" {
		t.Errorf("got %q", got)
	}
}

func TestFetchRawGuess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repo/plain/README", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("cgit readme"))
	})
	c, host := newTestForge(t, mux)
	if got := fetch(t, c, "https://"+host+"/repo"); got != "cgit readme" {
		t.Errorf("got %q", got)
	}
}

func TestFetchWebPageFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/me/game", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><head><title>x</title></head><body><script>var a</script><h1>Game Recomp</h1><p>Runs on Linux &amp; Windows</p></body></html>`))
	})
	c, host := newTestForge(t, mux)
	got := fetch(t, c, "https://"+host+"/me/game")
	if !strings.Contains(got, "Game Recomp") || !strings.Contains(got, "Linux & Windows") || strings.Contains(got, "var a") {
		t.Errorf("got %q", got)
	}
}

func TestPrivateAddressesBlocked(t *testing.T) {
	srv := httptest.NewTLSServer(http.NewServeMux())
	defer srv.Close()
	c := NewClient(nil, false)
	r, _ := c.ParseRepo(srv.URL + "/a/b")
	if _, err := c.FetchReadme(context.Background(), r); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Errorf("expected non-public address error, got %v", err)
	}
}
