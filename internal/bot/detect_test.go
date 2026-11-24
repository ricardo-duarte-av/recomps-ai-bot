package bot

import (
	"slices"
	"strings"
	"testing"

	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

func TestNamedMention(t *testing.T) {
	user := id.UserID("@hna:matrix.org")
	md := "Detected a recomp.\n\nPinging " + namedMention(user, "SuperBio (~256TB HDD, 5TB Tape, Local)") + " " + namedMention("@x:y.org", "")
	content := format.RenderMarkdown(md, true, false)
	if !strings.Contains(content.FormattedBody, `<a href="https://matrix.to/#/%40hna%3Amatrix.org">SuperBio (~256TB HDD, 5TB Tape, Local)</a>`) {
		t.Errorf("pill not rendered with display name:\n%s", content.FormattedBody)
	}
	if !strings.HasSuffix(content.FormattedBody, `</a></p>`) || !strings.Contains(content.FormattedBody, "<p>Pinging ") {
		t.Errorf("pings should be the last paragraph:\n%s", content.FormattedBody)
	}
	if content.Mentions == nil || !slices.Equal(content.Mentions.UserIDs, []id.UserID{user, "@x:y.org"}) {
		t.Errorf("mentions: %+v", content.Mentions)
	}
	if got := namedMention(user, "Evil\n[x](https://e.example) "+strings.Repeat("a", 100)); strings.Contains(got, "\n") || strings.Count(got, "](") != 1 {
		t.Errorf("display name not sanitized: %s", got)
	}
}

func TestSuggestionWhat(t *testing.T) {
	e := store.Entry{Game: "Pokémon Scarlet & Violet", OriginalSystem: "Nintendo Switch", Kind: store.KindRecomp, OSes: []string{"windows", "linux"}}
	got := suggestionWhat(e, "https://www.igdb.com/games/pokemon-scarlet-violet", "https://www.igdb.com/platforms/switch")
	want := "**[Pokémon Scarlet & Violet](https://www.igdb.com/games/pokemon-scarlet-violet)** from [Nintendo Switch](https://www.igdb.com/platforms/switch), for Windows and Linux"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got, want := suggestionWhat(e, "", ""), "**Pokémon Scarlet & Violet** from Nintendo Switch, for Windows and Linux"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
