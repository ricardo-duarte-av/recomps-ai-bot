package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Release is a published release of a repository.
type Release struct {
	Tag        string
	Name       string // may be empty or equal to Tag
	URL        string // web page of the release
	Published  time.Time
	Prerelease bool
}

const (
	// releasesPerRepo is how many of a repo's newest releases are looked at; more
	// than that in one day is unlikely and they'd be summed up anyway.
	releasesPerRepo = 10
	// githubBatch is how many repositories one GitHub GraphQL query asks about.
	githubBatch = 50
	// releaseInterval spaces out requests so a full scan doesn't hammer any API.
	releaseInterval = 500 * time.Millisecond
)

// ReleaseResult is what Releases found for one repository.
type ReleaseResult struct {
	Repo     Repo
	Releases []Release // published in the window, newest first
	Err      error     // set when the repo couldn't be checked
}

// Releases returns, for each repo, the releases published in [since, until).
// GitHub repos are queried in batches through the GraphQL API when a GitHub
// token is configured; other forges (and GitHub without a token) one repo at a
// time through their REST APIs. Generic hosts without an API are reported with
// an error. progress, if not nil, is called after each request with the number
// of repos done so far.
func (c *Client) Releases(ctx context.Context, repos []Repo, since, until time.Time, progress func(done int)) []ReleaseResult {
	results := make([]ReleaseResult, 0, len(repos))
	done := 0
	report := func(n int) {
		done += n
		if progress != nil {
			progress(done)
		}
	}
	var github, other []Repo
	for _, r := range repos {
		if r.Type == GitHub && c.hosts[r.Host].Token != "" {
			github = append(github, r)
		} else {
			other = append(other, r)
		}
	}
	// One GraphQL endpoint per GitHub host.
	byHost := map[string][]Repo{}
	var hosts []string
	for _, r := range github {
		if _, ok := byHost[r.Host]; !ok {
			hosts = append(hosts, r.Host)
		}
		byHost[r.Host] = append(byHost[r.Host], r)
	}
	for _, h := range hosts {
		list := byHost[h]
		for i := 0; i < len(list); i += githubBatch {
			if ctx.Err() != nil {
				return append(results, failAll(list[i:], ctx.Err())...)
			}
			batch := list[i:min(i+githubBatch, len(list))]
			results = append(results, c.githubReleases(ctx, batch, since, until)...)
			report(len(batch))
			sleep(ctx, releaseInterval)
		}
	}
	for _, r := range other {
		if ctx.Err() != nil {
			results = append(results, ReleaseResult{Repo: r, Err: ctx.Err()})
			continue
		}
		rels, err := c.restReleases(ctx, r)
		results = append(results, ReleaseResult{Repo: r, Releases: inWindow(rels, since, until), Err: err})
		report(1)
		sleep(ctx, releaseInterval)
	}
	return results
}

func failAll(repos []Repo, err error) []ReleaseResult {
	out := make([]ReleaseResult, len(repos))
	for i, r := range repos {
		out[i] = ReleaseResult{Repo: r, Err: err}
	}
	return out
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// inWindow keeps the releases published in [since, until), newest first.
func inWindow(rels []Release, since, until time.Time) []Release {
	var out []Release
	for _, r := range rels {
		if !r.Published.Before(since) && r.Published.Before(until) {
			out = append(out, r)
		}
	}
	for i := 1; i < len(out); i++ { // tiny lists: insertion sort, newest first
		for j := i; j > 0 && out[j].Published.After(out[j-1].Published); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// --- GitHub GraphQL ---

type ghRelease struct {
	TagName      string    `json:"tagName"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	PublishedAt  time.Time `json:"publishedAt"`
	IsPrerelease bool      `json:"isPrerelease"`
	IsDraft      bool      `json:"isDraft"`
}

// githubReleases asks one GraphQL query about a batch of repos on one host. If
// the whole query fails it's retried once, then split in halves, so one bad
// repo or a server hiccup doesn't lose the rest of the batch.
func (c *Client) githubReleases(ctx context.Context, batch []Repo, since, until time.Time) []ReleaseResult {
	res, err := c.githubQuery(ctx, batch, since, until)
	if err != nil && ctx.Err() == nil {
		sleep(ctx, 5*time.Second)
		res, err = c.githubQuery(ctx, batch, since, until)
	}
	if err == nil {
		return res
	}
	if len(batch) == 1 || ctx.Err() != nil {
		return failAll(batch, err)
	}
	half := len(batch) / 2
	return append(c.githubReleases(ctx, batch[:half], since, until), c.githubReleases(ctx, batch[half:], since, until)...)
}

func (c *Client) githubQuery(ctx context.Context, batch []Repo, since, until time.Time) ([]ReleaseResult, error) {
	var q, params strings.Builder
	vars := map[string]any{}
	for i, r := range batch {
		fmt.Fprintf(&params, "$o%d: String!, $n%d: String!, ", i, i)
		fmt.Fprintf(&q, "r%d: repository(owner: $o%d, name: $n%d) { releases(first: %d, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { tagName name url publishedAt isPrerelease isDraft } } }\n",
			i, i, i, releasesPerRepo)
		vars["o"+strconv.Itoa(i)] = r.Owner()
		vars["n"+strconv.Itoa(i)] = r.Name()
	}
	query := "query(" + strings.TrimSuffix(params.String(), ", ") + ") {\n" + q.String() + "}"
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})

	host := batch[0].Host
	endpoint := "https://api.github.com/graphql"
	if host != "github.com" {
		endpoint = "https://" + host + "/api/graphql"
	}
	if c.githubGraphQL != "" {
		endpoint = c.githubGraphQL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.hosts[host].Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "recomps-ai-bot")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub GraphQL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub GraphQL: %s", resp.Status)
	}
	var out struct {
		Data map[string]*struct {
			Releases struct {
				Nodes []ghRelease `json:"nodes"`
			} `json:"releases"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Path    []any  `json:"path"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding GitHub GraphQL response: %w", err)
	}
	if out.Data == nil {
		msg := "no data"
		if len(out.Errors) > 0 {
			msg = out.Errors[0].Message
		}
		return nil, fmt.Errorf("GitHub GraphQL: %s", msg)
	}
	// Errors that name one alias (e.g. NOT_FOUND for a deleted repo) only fail that repo.
	repoErr := map[string]error{}
	for _, e := range out.Errors {
		if len(e.Path) > 0 {
			if alias, ok := e.Path[0].(string); ok {
				repoErr[alias] = errors.New(strings.TrimSpace(e.Type + " " + e.Message))
			}
		}
	}
	results := make([]ReleaseResult, len(batch))
	for i, r := range batch {
		alias := "r" + strconv.Itoa(i)
		results[i].Repo = r
		repo := out.Data[alias]
		if repo == nil {
			results[i].Err = repoErr[alias]
			if results[i].Err == nil {
				results[i].Err = errors.New("repository not found")
			}
			continue
		}
		var rels []Release
		for _, n := range repo.Releases.Nodes {
			if n.IsDraft || n.PublishedAt.IsZero() {
				continue
			}
			rels = append(rels, Release{Tag: n.TagName, Name: n.Name, URL: n.URL, Published: n.PublishedAt, Prerelease: n.IsPrerelease})
		}
		results[i].Releases = inWindow(rels, since, until)
	}
	return results, nil
}

// --- REST: GitHub without a token, GitLab, Gitea/Forgejo ---

// restReleases fetches a repo's newest releases from its forge's REST API.
// Hosts of unknown type are probed as Gitea/Forgejo, then GitLab.
func (c *Client) restReleases(ctx context.Context, r Repo) ([]Release, error) {
	token := c.hosts[r.Host].Token
	switch r.Type {
	case GitHub:
		return c.githubRESTReleases(ctx, r, token)
	case GitLab:
		return c.gitlabReleases(ctx, r, token)
	case Gitea, Forgejo:
		return c.giteaReleases(ctx, r, token)
	case Generic:
		return nil, errors.New("this host has no releases API")
	}
	rels, err := c.giteaReleases(ctx, r, token)
	if errors.Is(err, errSkip) {
		rels, err = c.gitlabReleases(ctx, r, token)
	}
	if errors.Is(err, errSkip) {
		return nil, errors.New("couldn't find a releases API on this host")
	}
	return rels, err
}

func (c *Client) githubRESTReleases(ctx context.Context, r Repo, token string) ([]Release, error) {
	api := "https://api.github.com"
	if r.Host != "github.com" {
		api = "https://" + r.Host + "/api/v3"
	}
	headers := map[string]string{"X-GitHub-Api-Version": "2022-11-28"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	var rels []struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Prerelease  bool      `json:"prerelease"`
		Draft       bool      `json:"draft"`
	}
	u := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d", api, url.PathEscape(r.Owner()), url.PathEscape(r.Name()), releasesPerRepo)
	if err := c.getJSON(ctx, u, headers, &rels); err != nil {
		return nil, fmt.Errorf("GitHub: %w", err)
	}
	var out []Release
	for _, x := range rels {
		if !x.Draft && !x.PublishedAt.IsZero() {
			out = append(out, Release{Tag: x.TagName, Name: x.Name, URL: x.HTMLURL, Published: x.PublishedAt, Prerelease: x.Prerelease})
		}
	}
	return out, nil
}

func (c *Client) gitlabReleases(ctx context.Context, r Repo, token string) ([]Release, error) {
	headers := map[string]string{}
	if token != "" {
		headers["PRIVATE-TOKEN"] = token
	}
	var rels []struct {
		TagName         string    `json:"tag_name"`
		Name            string    `json:"name"`
		ReleasedAt      time.Time `json:"released_at"`
		UpcomingRelease bool      `json:"upcoming_release"`
		Links           struct {
			Self string `json:"self"`
		} `json:"_links"`
	}
	u := fmt.Sprintf("https://%s/api/v4/projects/%s/releases?per_page=%d", r.Host, url.PathEscape(r.Path), releasesPerRepo)
	if err := c.getJSON(ctx, u, headers, &rels); err != nil {
		return nil, err
	}
	var out []Release
	for _, x := range rels {
		if x.UpcomingRelease || x.ReleasedAt.IsZero() {
			continue
		}
		link := x.Links.Self
		if link == "" {
			link = r.WebURL() + "/-/releases/" + url.PathEscape(x.TagName)
		}
		out = append(out, Release{Tag: x.TagName, Name: x.Name, URL: link, Published: x.ReleasedAt})
	}
	return out, nil
}

func (c *Client) giteaReleases(ctx context.Context, r Repo, token string) ([]Release, error) {
	if strings.Count(r.Path, "/") != 1 {
		return nil, errSkip
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "token " + token
	}
	var rels []struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Prerelease  bool      `json:"prerelease"`
		Draft       bool      `json:"draft"`
	}
	u := fmt.Sprintf("https://%s/api/v1/repos/%s/%s/releases?limit=%d", r.Host, url.PathEscape(r.Owner()), url.PathEscape(r.Name()), releasesPerRepo)
	if err := c.getJSON(ctx, u, headers, &rels); err != nil {
		return nil, err
	}
	var out []Release
	for _, x := range rels {
		if !x.Draft && !x.PublishedAt.IsZero() {
			out = append(out, Release{Tag: x.TagName, Name: x.Name, URL: x.HTMLURL, Published: x.PublishedAt, Prerelease: x.Prerelease})
		}
	}
	return out, nil
}
