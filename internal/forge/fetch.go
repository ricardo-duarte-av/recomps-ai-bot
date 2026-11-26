package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// MaxReadmeBytes caps how much README text is passed to the LLM.
const MaxReadmeBytes = 15000

// maxPageBytes caps how much of an HTML page is read for the last-resort fallback.
const maxPageBytes = 2 << 20

// errSkip means "this strategy doesn't apply to this host, try the next one".
var errSkip = errors.New("not this forge")

// Host is per-host configuration.
type Host struct {
	Type  string
	Token string
}

// Client fetches READMEs from any forge.
type Client struct {
	hosts map[string]Host
	http  *http.Client
	// githubGraphQL overrides the GitHub GraphQL endpoint (tests only).
	githubGraphQL string
}

// NewClient creates a client. hosts maps lowercase host names to their settings.
// Unless allowPrivate is set, connections to loopback/private/link-local
// addresses are refused so the bot can't be used to probe its own network.
func NewClient(hosts map[string]Host, allowPrivate bool) *Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
				ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
				return fmt.Errorf("refusing to connect to non-public address %s", host)
			}
			return nil
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	normalized := map[string]Host{}
	for h, cfg := range hosts {
		normalized[strings.TrimPrefix(strings.ToLower(h), "www.")] = cfg
	}
	return &Client{hosts: normalized, http: &http.Client{Timeout: 30 * time.Second, Transport: transport}}
}

// HostType returns the configured forge type for host, or "".
func (c *Client) HostType(host string) string {
	return c.hosts[host].Type
}

// ParseRepo parses a repository URL using this client's host configuration.
func (c *Client) ParseRepo(raw string) (Repo, error) { return ParseRepo(raw, c.HostType) }

// FindRepos finds all repository URLs in text using this client's host configuration.
func (c *Client) FindRepos(text string) []Repo { return FindRepos(text, c.HostType) }

// FindRepo finds the first repository URL in text using this client's host configuration.
func (c *Client) FindRepo(text string) (Repo, bool) { return FindRepo(text, c.HostType) }

// FetchReadme returns the repository's README (truncated to MaxReadmeBytes),
// preceded by its short description ("Repository description: ...") when the
// forge has one.
// Hosts of unknown type are probed as Gitea/Forgejo, then GitLab, then by
// guessing raw file URLs, and finally by reading the repository's web page.
func (c *Client) FetchReadme(ctx context.Context, r Repo) (string, error) {
	token := c.hosts[r.Host].Token
	var strategies []func(context.Context, Repo, string) (string, error)
	switch r.Type {
	case GitHub:
		strategies = append(strategies, c.github)
	case GitLab:
		strategies = append(strategies, c.gitlab)
	case Gitea, Forgejo:
		strategies = append(strategies, c.gitea)
	case Generic:
	default:
		strategies = append(strategies, c.gitea, c.gitlab)
	}
	strategies = append(strategies, c.rawGuess, c.webPage)

	var errs []error
	for _, s := range strategies {
		text, err := s(ctx, r, token)
		if err == nil && strings.TrimSpace(text) != "" {
			return truncate(text, MaxReadmeBytes), nil
		}
		if err != nil && !errors.Is(err, errSkip) {
			errs = append(errs, err)
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if len(errs) > 0 {
		return "", errs[0]
	}
	return "", fmt.Errorf("couldn't find a README for %s", r.WebURL())
}

// withAbout puts a repository's short description (GitHub's "About") before its
// README, since the README alone may not name the game clearly. Either may be
// empty. The README is cut so the whole stays within MaxReadmeBytes.
func withAbout(about, readme string) string {
	about = strings.TrimSpace(about)
	if about == "" {
		return readme
	}
	head := "Repository description: " + about + "\n\n"
	if strings.TrimSpace(readme) == "" {
		return head
	}
	return head + "README:\n" + readme
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// get performs a GET and returns the body (up to limit bytes) and content type.
// 404/401/403 and similar are reported as errSkip.
func (c *Client) get(ctx context.Context, u string, headers map[string]string, limit int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "recomps-ai-bot")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: %s returned %s", errSkip, u, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body, resp.Header.Get("Content-Type"), err
}

func (c *Client) getJSON(ctx context.Context, u string, headers map[string]string, out any) error {
	headers["Accept"] = "application/json"
	body, ctype, err := c.get(ctx, u, headers, 1<<20)
	if err != nil {
		return err
	}
	if !strings.Contains(ctype, "json") {
		return errSkip
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errSkip
	}
	return nil
}

// --- GitHub (github.com and GitHub Enterprise) ---

func (c *Client) github(ctx context.Context, r Repo, token string) (string, error) {
	api := "https://api.github.com"
	if r.Host != "github.com" {
		api = "https://" + r.Host + "/api/v3"
	}
	headers := map[string]string{"Accept": "application/vnd.github.raw", "X-GitHub-Api-Version": "2022-11-28"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	base := fmt.Sprintf("%s/repos/%s/%s", api, url.PathEscape(r.Owner()), url.PathEscape(r.Name()))
	// The description is best effort: a README alone is still worth classifying.
	var repo struct {
		Description string `json:"description"`
	}
	jsonHeaders := map[string]string{"X-GitHub-Api-Version": "2022-11-28"}
	if token != "" {
		jsonHeaders["Authorization"] = "Bearer " + token
	}
	_ = c.getJSON(ctx, base, jsonHeaders, &repo)
	body, _, err := c.get(ctx, base+"/readme", headers, MaxReadmeBytes)
	if err != nil {
		if repo.Description != "" && errors.Is(err, errSkip) {
			return withAbout(repo.Description, ""), nil // no README, but the description says something
		}
		return "", fmt.Errorf("GitHub: %w", err)
	}
	return withAbout(repo.Description, string(body)), nil
}

// --- GitLab (gitlab.com and self-hosted) ---

func (c *Client) gitlab(ctx context.Context, r Repo, token string) (string, error) {
	headers := map[string]string{}
	if token != "" {
		headers["PRIVATE-TOKEN"] = token
	}
	var project struct {
		ID            int    `json:"id"`
		DefaultBranch string `json:"default_branch"`
		ReadmeURL     string `json:"readme_url"`
		Description   string `json:"description"`
	}
	u := fmt.Sprintf("https://%s/api/v4/projects/%s", r.Host, url.PathEscape(r.Path))
	if err := c.getJSON(ctx, u, headers, &project); err != nil {
		return "", err
	}
	if project.ID == 0 {
		return "", errSkip
	}
	if project.ReadmeURL == "" {
		if strings.TrimSpace(project.Description) != "" {
			return withAbout(project.Description, ""), nil
		}
		return "", fmt.Errorf("GitLab project %s has no README", r.WebURL())
	}
	// readme_url is the web blob URL, e.g. https://host/group/repo/-/blob/main/README.md
	_, file, ok := strings.Cut(project.ReadmeURL, "/-/blob/"+project.DefaultBranch+"/")
	if !ok {
		raw := strings.Replace(project.ReadmeURL, "/-/blob/", "/-/raw/", 1)
		body, _, err := c.get(ctx, raw, headers, MaxReadmeBytes)
		return withAbout(project.Description, string(body)), err
	}
	rawURL := fmt.Sprintf("https://%s/api/v4/projects/%d/repository/files/%s/raw?ref=%s",
		r.Host, project.ID, url.PathEscape(file), url.QueryEscape(project.DefaultBranch))
	body, _, err := c.get(ctx, rawURL, headers, MaxReadmeBytes)
	if err != nil {
		return "", fmt.Errorf("GitLab: %w", err)
	}
	return withAbout(project.Description, string(body)), nil
}

// --- Gitea / Forgejo (codeberg.org, gitea.com and self-hosted) ---

func (c *Client) gitea(ctx context.Context, r Repo, token string) (string, error) {
	if strings.Count(r.Path, "/") != 1 {
		return "", errSkip
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "token " + token
	}
	base := fmt.Sprintf("https://%s/api/v1/repos/%s/%s", r.Host, url.PathEscape(r.Owner()), url.PathEscape(r.Name()))
	var repo struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Description   string `json:"description"`
	}
	if err := c.getJSON(ctx, base, headers, &repo); err != nil {
		return "", err
	}
	if repo.FullName == "" {
		return "", errSkip
	}
	var entries []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		DownloadURL string `json:"download_url"`
	}
	if err := c.getJSON(ctx, base+"/contents?ref="+url.QueryEscape(repo.DefaultBranch), headers, &entries); err != nil {
		return "", fmt.Errorf("Gitea/Forgejo: listing files: %w", err)
	}
	best, bestRank := "", 99
	for _, e := range entries {
		if e.Type != "file" || e.DownloadURL == "" {
			continue
		}
		if rank := readmeRank(e.Name); rank < bestRank {
			best, bestRank = e.DownloadURL, rank
		}
	}
	if best == "" {
		if strings.TrimSpace(repo.Description) != "" {
			return withAbout(repo.Description, ""), nil
		}
		return "", fmt.Errorf("Gitea/Forgejo repository %s has no README", r.WebURL())
	}
	body, _, err := c.get(ctx, best, headers, MaxReadmeBytes)
	if err != nil {
		return "", fmt.Errorf("Gitea/Forgejo: %w", err)
	}
	return withAbout(repo.Description, string(body)), nil
}

// readmeRank orders README file names by preference (lower is better); 99 = not a README.
func readmeRank(name string) int {
	lower := strings.ToLower(name)
	switch {
	case lower == "readme.md":
		return 0
	case lower == "readme":
		return 1
	case strings.HasPrefix(lower, "readme.") && !strings.Contains(lower[7:], "."):
		return 2
	case strings.HasPrefix(lower, "readme"):
		return 3
	}
	return 99
}

// --- Generic: guess raw file URLs (cgit, SourceHut, unknown Gitea/GitLab setups) ---

var rawNames = []string{"README.md", "readme.md", "README", "README.rst", "README.txt", "README.org"}

func (c *Client) rawGuess(ctx context.Context, r Repo, _ string) (string, error) {
	web := r.WebURL()
	var candidates []string
	for _, name := range rawNames {
		candidates = append(candidates,
			web+"/plain/"+name,             // cgit
			web+"/blob/HEAD/"+name,         // SourceHut
			web+"/raw/HEAD/"+name,          // GitHub-style raw
			web+"/-/raw/HEAD/"+name,        // GitLab
			web+"/raw/branch/main/"+name,   // Gitea/Forgejo
			web+"/raw/branch/master/"+name, // Gitea/Forgejo
		)
	}
	for _, u := range candidates {
		body, ctype, err := c.get(ctx, u, nil, MaxReadmeBytes)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			var netErr net.Error
			if errors.As(err, &netErr) || strings.Contains(err.Error(), "non-public address") {
				return "", err // host unreachable; don't hammer it with every guess
			}
			continue
		}
		// Raw files are served as text/plain (or markdown); HTML means we hit a web page.
		if strings.Contains(ctype, "html") || looksLikeHTML(body) {
			continue
		}
		return string(body), nil
	}
	return "", errSkip
}

// --- Last resort: the repository's web page as plain text ---

var (
	scriptRe = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head)[^>]*>.*?</(script|style|noscript|svg|head)>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRe  = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankRe  = regexp.MustCompile(`\n\s*\n+`)
)

func (c *Client) webPage(ctx context.Context, r Repo, _ string) (string, error) {
	body, ctype, err := c.get(ctx, r.WebURL(), map[string]string{"Accept": "text/html"}, maxPageBytes)
	if err != nil {
		return "", err
	}
	if !strings.Contains(ctype, "html") && !looksLikeHTML(body) {
		return string(body), nil
	}
	return htmlToText(string(body)), nil
}

func htmlToText(s string) string {
	s = scriptRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, "\n")
	s = html.UnescapeString(s)
	s = spaceRe.ReplaceAllString(s, " ")
	s = blankRe.ReplaceAllString(s, "\n")
	return strings.TrimSpace(s)
}

func looksLikeHTML(b []byte) bool {
	head := strings.ToLower(strings.TrimSpace(string(b[:min(len(b), 512)])))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}
