package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleasesGitHubGraphQL(t *testing.T) {
	until := time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)
	since := until.Add(-24 * time.Hour)
	var queries atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		queries.Add(1)
		var req struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		data := map[string]any{}
		var errs []any
		for i := 0; ; i++ {
			name, ok := req.Variables["n"+itoa(i)]
			if !ok {
				break
			}
			alias := "r" + itoa(i)
			switch name {
			case "gone":
				data[alias] = nil
				errs = append(errs, map[string]any{"type": "NOT_FOUND", "message": "Could not resolve to a Repository", "path": []string{alias}})
			case "busy":
				data[alias] = map[string]any{"releases": map[string]any{"nodes": []any{
					map[string]any{"tagName": "v2", "name": "v2", "url": "https://github.com/a/busy/releases/tag/v2", "publishedAt": "2026-11-26T10:00:00Z"},
					map[string]any{"tagName": "v2-rc", "name": "", "url": "u", "publishedAt": "2026-11-26T09:00:00Z", "isPrerelease": true},
					map[string]any{"tagName": "draft", "url": "u", "publishedAt": "2026-11-26T08:00:00Z", "isDraft": true},
					map[string]any{"tagName": "future", "url": "u", "publishedAt": "2026-11-26T20:00:00Z"},
					map[string]any{"tagName": "v1", "url": "u", "publishedAt": "2026-11-25T19:59:59Z"},
				}}}
			default:
				data[alias] = map[string]any{"releases": map[string]any{"nodes": []any{}}}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data, "errors": errs})
	})
	c, host := newTestForge(t, mux)
	c.hosts = map[string]Host{"github.com": {Type: GitHub, Token: "tok"}}
	c.githubGraphQL = "https://" + host + "/graphql"

	var repos []Repo
	for i := range 120 {
		repos = append(repos, Repo{Host: "github.com", Path: "a/repo" + itoa(i), Type: GitHub})
	}
	repos = append(repos, Repo{Host: "github.com", Path: "a/busy", Type: GitHub}, Repo{Host: "github.com", Path: "a/gone", Type: GitHub})
	results := c.Releases(context.Background(), repos, since, until, nil)
	if len(results) != len(repos) {
		t.Fatalf("%d results for %d repos", len(results), len(repos))
	}
	if n := queries.Load(); n != 3 { // 122 repos in batches of 50
		t.Errorf("%d GraphQL queries, want 3", n)
	}
	for _, res := range results {
		switch res.Repo.Name() {
		case "busy":
			var tags []string
			for _, r := range res.Releases {
				tags = append(tags, r.Tag)
			}
			if res.Err != nil || strings.Join(tags, ",") != "v2,v2-rc" || !res.Releases[1].Prerelease {
				t.Errorf("busy: %+v, %v", res.Releases, res.Err)
			}
		case "gone":
			if res.Err == nil || !strings.Contains(res.Err.Error(), "NOT_FOUND") {
				t.Errorf("gone: err = %v", res.Err)
			}
		default:
			if res.Err != nil || len(res.Releases) != 0 {
				t.Errorf("%s: %+v, %v", res.Repo.Path, res.Releases, res.Err)
			}
		}
	}
}

func TestReleasesGitea(t *testing.T) {
	until := time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/Owner/Repo/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"tag_name":"1.1","name":"Winter update","html_url":"https://x/Owner/Repo/releases/tag/1.1","published_at":"2026-11-26T12:00:00Z"},
			{"tag_name":"1.0","html_url":"https://x/r/1.0","published_at":"2026-11-01T12:00:00Z"}]`))
	})
	c, host := newTestForge(t, mux)
	c.hosts = map[string]Host{host: {Type: Gitea}}
	r, err := c.ParseRepo("https://" + host + "/Owner/Repo")
	if err != nil {
		t.Fatal(err)
	}
	res := c.Releases(context.Background(), []Repo{r}, until.Add(-24*time.Hour), until, nil)
	if len(res) != 1 || res[0].Err != nil || len(res[0].Releases) != 1 || res[0].Releases[0].Name != "Winter update" {
		t.Fatalf("results = %+v", res)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
