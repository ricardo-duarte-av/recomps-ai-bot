package bot

import (
	"testing"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

func TestLogLine(t *testing.T) {
	room := "[Recomps](https://matrix.to/#/%23recomps:example.org)"
	e := store.Entry{RepoURL: "https://github.com/n64decomp/sm64", Game: "Super Mario 64", OriginalSystem: "Nintendo 64",
		Kind: store.KindDecomp, OSes: []string{"linux", "windows"}}
	got := logLine(e, addLog{Manual: true, By: "@mod:example.org", ByName: "Mod", Room: room})
	want := "Manually adding https://github.com/n64decomp/sm64 by [Mod](https://matrix.to/#/%40mod%3Aexample.org) in room " + room +
		", a decomp for **Super Mario 64** from _Nintendo 64_ , for _Windows and Linux_"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	got = logLine(e, addLog{Manual: true, Update: true, By: "@mod:example.org", ByName: "Mod", RequestedBy: "@u:example.org", RequestedByName: "U", Room: room})
	want = "Manually updating https://github.com/n64decomp/sm64 by [Mod](https://matrix.to/#/%40mod%3Aexample.org) (requested by [U](https://matrix.to/#/%40u%3Aexample.org)) in room " + room +
		", a decomp for **Super Mario 64** from _Nintendo 64_ , for _Windows and Linux_"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	tk := store.Entry{RepoURL: "https://github.com/n64recomp/n64recomp", OriginalSystem: "Nintendo 64", Kind: store.KindToolkit, OSes: []string{"linux"}}
	got = logLine(tk, addLog{By: "@feed:example.org", ByName: "Feed", Room: room})
	want = "Adding https://github.com/n64recomp/n64recomp , auto-added from a link posted by [Feed](https://matrix.to/#/%40feed%3Aexample.org) in room " + room +
		", a toolkit for _Nintendo 64_ games, for _Linux_"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
