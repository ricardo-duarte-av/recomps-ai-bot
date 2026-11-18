// Package forge parses git repository URLs from any host (GitHub, GitLab,
// Gitea, Forgejo, cgit, SourceHut, ...) and fetches their READMEs.
package forge

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Forge types.
const (
	GitHub  = "github"
	GitLab  = "gitlab"
	Gitea   = "gitea"
	Forgejo = "forgejo"
	Generic = "generic"
)

var ErrNotRepo = errors.New("not a repository URL")

// knownHosts are recognized without configuration.
var knownHosts = map[string]string{
	"github.com":             GitHub,
	"gitlab.com":             GitLab,
	"codeberg.org":           Forgejo,
	"gitea.com":              Gitea,
	"git.sr.ht":              Generic,
	"salsa.debian.org":       GitLab,
	"gitlab.gnome.org":       GitLab,
	"invent.kde.org":         GitLab,
	"gitlab.freedesktop.org": GitLab,
}

// ignoredHosts are links that are never repositories.
var ignoredHosts = map[string]bool{"matrix.to": true}

// Path segments that start a sub-page of a repository (tree views, files, issues...).
var subpageSegments = map[string]bool{
	"-": true, "tree": true, "blob": true, "src": true, "raw": true, "commits": true, "commit": true,
	"releases": true, "issues": true, "pulls": true, "pull": true, "merge_requests": true, "wiki": true,
	"tags": true, "branches": true, "actions": true, "log": true, "refs": true, "about": true,
	"plain": true, "summary": true, "archive": true, "activity": true,
}

// Repo identifies a repository on some forge.
type Repo struct {
	Host string // lowercase host[:port]
	Path string // repository path as written by the user, without leading/trailing slash or .git
	Type string // forge type if known from the host, else ""
}

// URL is the canonical, lowercase identity of the repository (used as the DB key).
func (r Repo) URL() string {
	return "https://" + r.Host + "/" + strings.ToLower(r.Path)
}

// WebURL keeps the original path casing, for fetching from case-sensitive hosts.
func (r Repo) WebURL() string {
	return "https://" + r.Host + "/" + r.Path
}

// Owner and Name return the first two path segments (GitHub/Gitea/Forgejo layout).
func (r Repo) Owner() string { o, _, _ := strings.Cut(r.Path, "/"); return o }
func (r Repo) Name() string {
	parts := strings.Split(r.Path, "/")
	return parts[len(parts)-1]
}

// URLPattern finds candidate URLs in free text: anything with a scheme,
// or a bare well-known forge host. Brackets, parentheses and backticks end a URL so
// Markdown links like [url](url) and `url` yield the URL itself.
var URLPattern = regexp.MustCompile("(?i)\\bhttps?://[^\\s<>\"'\\[\\]()`]+|\\b(?:www\\.)?(?:github\\.com|gitlab\\.com|codeberg\\.org|gitea\\.com|git\\.sr\\.ht)/[^\\s<>\"'\\[\\]()`]+")

// pagesHosts maps static-site hosting domains to the forge serving the repository:
// https://user.github.io/project is built from https://github.com/user/project.
var pagesHosts = map[string]string{
	".github.io":     "github.com",
	".gitlab.io":     "gitlab.com",
	".codeberg.page": "codeberg.org",
}

// pagesToRepo rewrites a Pages site URL to its repository, or returns false.
// A user site (user.github.io with no path) lives in the user.github.io repository.
func pagesToRepo(host string, segs []string) (string, []string, bool) {
	for suffix, forge := range pagesHosts {
		user, ok := strings.CutSuffix(host, suffix)
		if !ok || user == "" || strings.Contains(user, ".") {
			continue
		}
		if len(segs) == 0 {
			if forge == "codeberg.org" {
				return forge, []string{user, "pages"}, true
			}
			return forge, []string{user, host}, true
		}
		return forge, []string{user, segs[0]}, true
	}
	return "", nil, false
}

// ParseRepo parses a repository URL. hostType resolves configured hosts
// (it may be nil); well-known hosts are recognized automatically.
func ParseRepo(raw string, hostType func(host string) string) (Repo, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "<>()[],.;\"'")
	// Markdown bodies escape punctuation (Reddit writes gravityrush\_pc); a backslash
	// is never part of a real URL, so drop it rather than detect a bogus repo.
	raw = strings.ReplaceAll(raw, "\\", "")
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Repo{}, ErrNotRepo
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	if ignoredHosts[host] {
		return Repo{}, ErrNotRepo
	}
	var segs []string
	for _, s := range strings.Split(u.Path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if forgeHost, repoSegs, ok := pagesToRepo(host, segs); ok {
		host, segs = forgeHost, repoSegs
	}
	typ := ""
	if hostType != nil {
		typ = hostType(host)
	}
	if typ == "" {
		typ = knownHosts[host]
	}

	// Known forges always have at least owner/name; unknown hosts (cgit, gitweb...)
	// may serve a repository at the top level, e.g. https://git.zx2c4.com/wireguard-tools.
	minSegs := 2
	if typ == "" || typ == Generic {
		minSegs = 1
	}
	// Cut off sub-pages such as /tree/main or /-/blob/main/README.md.
	for i := minSegs; i < len(segs); i++ {
		if subpageSegments[strings.ToLower(segs[i])] {
			segs = segs[:i]
			break
		}
	}
	// GitHub, Gitea and Forgejo repos are always owner/name.
	if (typ == GitHub || typ == Gitea || typ == Forgejo) && len(segs) > 2 {
		segs = segs[:2]
	}
	if len(segs) < minSegs {
		return Repo{}, ErrNotRepo
	}
	last := strings.TrimSuffix(segs[len(segs)-1], ".git")
	if last == "" {
		return Repo{}, ErrNotRepo
	}
	segs[len(segs)-1] = last
	return Repo{Host: host, Path: strings.Join(segs, "/"), Type: typ}, nil
}

// FindRepo returns the first repository URL found in text.
func FindRepo(text string, hostType func(host string) string) (Repo, bool) {
	for _, m := range URLPattern.FindAllString(text, -1) {
		if r, err := ParseRepo(m, hostType); err == nil {
			return r, true
		}
	}
	return Repo{}, false
}

// FindRepos returns all distinct repository URLs found in text, in order.
func FindRepos(text string, hostType func(host string) string) []Repo {
	var out []Repo
	seen := map[string]bool{}
	for _, m := range URLPattern.FindAllString(text, -1) {
		r, err := ParseRepo(m, hostType)
		if err != nil || seen[r.URL()] {
			continue
		}
		seen[r.URL()] = true
		out = append(out, r)
	}
	return out
}

// LikelyForge reports whether a repo's host is probably a git forge: a known or
// configured forge host, or a host name containing "git" (git.example.org, gitea.x, ...).
func LikelyForge(r Repo) bool {
	if r.Type != "" {
		return true
	}
	return strings.Contains(r.Host, "git")
}
