package bot

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/forge"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

func TestNextReport(t *testing.T) {
	lisbon, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	for _, tc := range []struct{ now, want string }{
		{"2026-11-26T10:00:00Z", "2026-11-26T20:00:00Z"}, // later today
		{"2026-11-26T20:00:00Z", "2026-11-27T20:00:00Z"}, // exactly now: tomorrow
		{"2026-11-26T21:00:00Z", "2026-11-27T20:00:00Z"},
		{"2026-03-28T21:00:00Z", "2026-03-29T19:00:00Z"}, // DST starts: 20:00 local is 19:00 UTC
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		want, _ := time.Parse(time.RFC3339, tc.want)
		if got := nextReport(now, 20, 0, lisbon); !got.Equal(want) {
			t.Errorf("nextReport(%s) = %s, want %s", tc.now, got.UTC(), tc.want)
		}
	}
}

func TestReleaseReport(t *testing.T) {
	at := time.Date(2026, 11, 26, 14, 3, 0, 0, time.UTC)
	rel := func(tag, name string) forge.Release {
		return forge.Release{Tag: tag, Name: name, URL: "https://github.com/x/releases/tag/" + tag, Published: at}
	}
	found := []repoReleases{
		{Entry: store.Entry{RepoURL: "https://github.com/b/oot-recomp", Game: "Zelda: OoT", OriginalSystem: "Nintendo 64", Kind: "recomp"},
			Releases: []forge.Release{rel("v1.2", "v1.2"), rel("v1.1", "Release v1.1")}},
		{Entry: store.Entry{RepoURL: "https://github.com/a/oot-decomp", Game: "Zelda: OoT", OriginalSystem: "Nintendo 64", Kind: "decomp"},
			Releases: []forge.Release{rel("2.0", "Big update")}},
		{Entry: store.Entry{RepoURL: "https://github.com/n/n64recomp", OriginalSystem: "Nintendo 64", Kind: "toolkit"},
			Releases: []forge.Release{rel("v3", "")}},
		{Entry: store.Entry{RepoURL: "https://github.com/c/sm64", Game: "Super Mario 64", OriginalSystem: "Nintendo 64", Kind: "decomp"},
			Releases: []forge.Release{{Tag: "nightly", URL: "u", Published: at, Prerelease: true}}},
	}
	links := map[string]store.IGDBLink{store.IGDBGameKey("Super Mario 64", "Nintendo 64"): {URL: "https://www.igdb.com/games/super-mario-64"}}
	msgs := releaseReport(found, links, time.UTC)
	want := `**New releases in the last 24 hours:** 5 releases in 4 repositories

**[Super Mario 64](https://www.igdb.com/games/super-mario-64)** (Nintendo 64)
- https://github.com/c/sm64 (decomp)
  - [nightly](u) (pre-release) · Nov 26 14:03

**Zelda: OoT** (Nintendo 64)
- https://github.com/a/oot-decomp (decomp)
  - [2.0](https://github.com/x/releases/tag/2.0) Big update · Nov 26 14:03
- https://github.com/b/oot-recomp (recomp)
  - [v1.2](https://github.com/x/releases/tag/v1.2) · Nov 26 14:03
  - [Release v1.1](https://github.com/x/releases/tag/v1.1) · Nov 26 14:03

Toolkits for Nintendo 64
- https://github.com/n/n64recomp (toolkit)
  - [v3](https://github.com/x/releases/tag/v3) · Nov 26 14:03`
	if len(msgs) != 1 || msgs[0] != want {
		t.Errorf("report:\n%s\n\nwant:\n%s", strings.Join(msgs, "\n----\n"), want)
	}

	// A long report is split between games, never inside one.
	var many []repoReleases
	for i := range 400 {
		many = append(many, repoReleases{Entry: store.Entry{RepoURL: "https://github.com/o/r" + itoa(i), Game: "Game " + itoa(i), OriginalSystem: "PC", Kind: "decomp"},
			Releases: []forge.Release{rel("v1", "")}})
	}
	msgs = releaseReport(many, nil, time.UTC)
	if len(msgs) < 2 {
		t.Fatalf("400 games fit in %d message(s)", len(msgs))
	}
	for i, m := range msgs {
		if len(m) > maxReportBytes {
			t.Errorf("message %d is %d bytes", i, len(m))
		}
		if i > 0 && !strings.HasPrefix(m, "**New releases (continued)**\n\n**Game ") {
			t.Errorf("message %d starts %q", i, m[:60])
		}
	}
	if !releasesCommand("Check releases!") || releasesCommand("check releases for zelda") {
		t.Error("releasesCommand")
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
