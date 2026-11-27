package forge

import "testing"

func TestParseRepo(t *testing.T) {
	hostType := func(h string) string {
		if h == "git.example.org" {
			return GitLab
		}
		return ""
	}
	cases := map[string]string{
		// GitHub
		"https://www.github.com/zeldaret/oot":                                  "https://github.com/zeldaret/oot",
		"https://github.com/ZeldaRet/OoT.git":                                  "https://github.com/zeldaret/oot",
		"github.com/zeldaret/oot/tree/main/src":                                "https://github.com/zeldaret/oot",
		"http://github.com/zeldaret/oot?tab=readme#build":                      "https://github.com/zeldaret/oot",
		"<https://github.com/zeldaret/oot>":                                    "https://github.com/zeldaret/oot",
		`https://github.com/LucaPazienza/gravityrush\_pc/releases/tag/v0.1.15`: "https://github.com/lucapazienza/gravityrush_pc",
		// GitLab, including nested groups and /-/ sub-pages
		"https://gitlab.com/group/sub/repo/-/tree/main": "https://gitlab.com/group/sub/repo",
		"https://git.example.org/a/b/c.git":             "https://git.example.org/a/b/c",
		// Gitea / Forgejo
		"https://codeberg.org/owner/repo/src/branch/main/README.md": "https://codeberg.org/owner/repo",
		"https://gitea.example.net/owner/repo/src/branch/main":      "https://gitea.example.net/owner/repo",
		// SourceHut, cgit
		"https://git.sr.ht/~user/repo/tree/main":      "https://git.sr.ht/~user/repo",
		"https://git.zx2c4.com/wireguard-tools/tree/": "https://git.zx2c4.com/wireguard-tools",
		"https://git.example.com:8443/Owner/Repo":     "https://git.example.com:8443/owner/repo",
		// Pages sites map to the repository they're built from
		"https://johannruiz.github.io/daytona-usa-2-web/": "https://github.com/johannruiz/daytona-usa-2-web",
		"https://user.github.io/project/play/index.html":  "https://github.com/user/project",
		"https://user.github.io/":                         "https://github.com/user/user.github.io",
		"https://group.gitlab.io/project":                 "https://gitlab.com/group/project",
		"https://someone.codeberg.page/game/":             "https://codeberg.org/someone/game",
		// Files served from a GitHub repository map to the repository
		"https://raw.githubusercontent.com/nxengine/nxengine-evo/master/screenshot.png": "https://github.com/nxengine/nxengine-evo",
		"https://media.githubusercontent.com/media/owner/repo/main/big.bin":             "https://github.com/owner/repo",
		"https://codeload.github.com/owner/repo/zip/refs/heads/main":                    "https://github.com/owner/repo",
		"https://api.github.com/repos/owner/repo/releases":                              "https://github.com/owner/repo",
	}
	for in, want := range cases {
		r, err := ParseRepo(in, hostType)
		if err != nil || r.URL() != want {
			t.Errorf("ParseRepo(%q) = %q, %v; want %q", in, r.URL(), err, want)
		}
	}
	for _, in := range []string{"https://github.com/onlyowner", "nope", "ftp://x.org/a/b", "https://matrix.to/#/@bot:x",
		"https://user-images.githubusercontent.com/123/abc.png", "https://avatars.githubusercontent.com/u/1",
		"https://gist.github.com/user/0123abcd", "https://raw.githubusercontent.com/owner"} {
		if r, err := ParseRepo(in, hostType); err == nil {
			t.Errorf("ParseRepo(%q) should fail, got %q", in, r.URL())
		}
	}
}

func TestWebURLKeepsCase(t *testing.T) {
	r, _ := ParseRepo("https://git.example.com/Owner/Repo", nil)
	if r.WebURL() != "https://git.example.com/Owner/Repo" {
		t.Errorf("got %q", r.WebURL())
	}
}

func TestFindRepo(t *testing.T) {
	for text, want := range map[string]string{
		"bot add this repo to the list https://www.github.com/pret/pokeemerald , a gba decomp": "https://github.com/pret/pokeemerald",
		"add codeberg.org/someone/game-recomp please":                                          "https://codeberg.org/someone/game-recomp",
		"add https://git.example.net/me/port for linux":                                        "https://git.example.net/me/port",
	} {
		r, ok := FindRepo(text, nil)
		if !ok || r.URL() != want {
			t.Errorf("FindRepo(%q) = %q %v; want %q", text, r.URL(), ok, want)
		}
	}
}

func TestFindReposAndLikelyForge(t *testing.T) {
	text := `new release! https://github.com/PlagueDrRiess/salsburysteaks/releases/tag/v2026.11.24
also https://github.com/plaguedrriess/SalsburySteaks again, https://www.youtube.com/watch?v=abc/def
and https://git.example.net/me/port plus https://example.com/some/page`
	repos := FindRepos(text, nil)
	var likely []string
	for _, r := range repos {
		if LikelyForge(r) {
			likely = append(likely, r.URL())
		}
	}
	want := []string{"https://github.com/plaguedrriess/salsburysteaks", "https://git.example.net/me/port"}
	if len(likely) != len(want) || likely[0] != want[0] || likely[1] != want[1] {
		t.Errorf("likely forges = %v, want %v", likely, want)
	}
}

func TestFindReposMarkdownLink(t *testing.T) {
	text := "Play here 👉 [https://johannruiz.github.io/daytona-usa-2-web/](https://johannruiz.github.io/daytona-usa-2-web/) " +
		"or see (https://github.com/zeldaret/oot) and `https://gitlab.com/a/b`"
	var got []string
	for _, r := range FindRepos(text, nil) {
		got = append(got, r.URL())
	}
	want := []string{"https://github.com/johannruiz/daytona-usa-2-web", "https://github.com/zeldaret/oot", "https://gitlab.com/a/b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestFindRepoInvisibleChars(t *testing.T) {
	want := "https://github.com/vectordev58/smb3"
	for _, text := range []string{
		"Repo: https://github.com/vectorDev58/smb3⁠",
		"https://github.com/vectorDev58/smb3​ and more",
		"github.com/vectorDev58/smb3 for linux",
	} {
		r, ok := FindRepo(text, nil)
		if !ok || r.URL() != want || r.WebURL() != "https://github.com/vectorDev58/smb3" {
			t.Errorf("FindRepo(%q) = %q %v; want %q", text, r.WebURL(), ok, want)
		}
	}
	if r, err := ParseRepo("https://github.com/vectorDev58/smb3⁠", nil); err != nil || r.Name() != "smb3" {
		t.Errorf("ParseRepo kept the word joiner: %q %v", r.Path, err)
	}
}
