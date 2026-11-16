package forge

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveForges hits real forges. Run with: RECOMPBOT_LIVE=1 go test -tags goolm -run Live ./internal/forge
func TestLiveForges(t *testing.T) {
	if os.Getenv("RECOMPBOT_LIVE") == "" {
		t.Skip("set RECOMPBOT_LIVE=1 to run")
	}
	c := NewClient(nil, false)
	for _, u := range []string{
		"https://github.com/pret/pokeemerald",
		"https://gitlab.com/gitlab-org/cli",
		"https://codeberg.org/forgejo/forgejo",
		"https://gitea.com/gitea/tea",
		"https://git.sr.ht/~sircmpwn/hare",
		"https://git.zx2c4.com/wireguard-tools",
		"https://gitlab.winehq.org/wine/wine",      // self-hosted GitLab, auto-detected
		"https://next.forgejo.org/forgejo/forgejo", // self-hosted Forgejo, auto-detected
	} {
		t.Run(u, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			r, err := c.ParseRepo(u)
			if err != nil {
				t.Fatal(err)
			}
			text, err := c.FetchReadme(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%d bytes: %.80q", len(text), text)
		})
	}
}
