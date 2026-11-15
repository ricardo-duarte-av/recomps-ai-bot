// Package github normalizes GitHub repository URLs and fetches READMEs.
package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MaxReadmeBytes caps how much README text is passed to the LLM.
const MaxReadmeBytes = 15000

var ErrNotGitHub = errors.New("not a GitHub repository URL")

// URLPattern finds GitHub repository URLs inside free text.
var URLPattern = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+`)

// Repo identifies a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

// URL returns the canonical repository URL.
func (r Repo) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s", r.Owner, r.Name)
}

// ParseRepo parses a GitHub URL (with or without scheme, www, .git suffix,
// trailing paths, query or fragment) into a Repo with lowercase owner/name.
func ParseRepo(raw string) (Repo, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "<>()[],.;\"'")
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Repo{}, ErrNotGitHub
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "github.com" {
		return Repo{}, ErrNotGitHub
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, ErrNotGitHub
	}
	name := strings.TrimSuffix(parts[1], ".git")
	if name == "" {
		return Repo{}, ErrNotGitHub
	}
	return Repo{Owner: strings.ToLower(parts[0]), Name: strings.ToLower(name)}, nil
}

// FindRepo returns the first GitHub repository URL found in text.
func FindRepo(text string) (Repo, bool) {
	for _, m := range URLPattern.FindAllString(text, -1) {
		if r, err := ParseRepo(m); err == nil {
			return r, true
		}
	}
	return Repo{}, false
}

// Client fetches READMEs via the GitHub REST API.
type Client struct {
	Token string
	HTTP  *http.Client
}

func NewClient(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// FetchReadme returns the raw README of the repository's default branch,
// truncated to MaxReadmeBytes.
func (c *Client) FetchReadme(ctx context.Context, r Repo) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/readme", url.PathEscape(r.Owner), url.PathEscape(r.Name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.raw")
	req.Header.Set("User-Agent", "recomps-ai-bot")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("repository or README not found")
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxReadmeBytes))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
