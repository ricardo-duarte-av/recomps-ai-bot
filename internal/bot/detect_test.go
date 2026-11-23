package bot

import (
	"slices"
	"strings"
	"testing"

	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
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
