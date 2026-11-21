package bot

import (
	"testing"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

func TestAnnouncement(t *testing.T) {
	e := store.Entry{RepoURL: "https://github.com/n64decomp/sm64", Game: "Super Mario 64", OriginalSystem: "Nintendo 64",
		Kind: store.KindDecomp, OSes: []string{"linux", "windows"}}
	got := announcement(e, "Mod [x]", "@mod:example.org")
	want := "New decompilation added to the DB: **sm64**, a decompilation of Super Mario 64 from Nintendo 64 for Windows and Linux - https://github.com/n64decomp/sm64 , by [Mod \\[x\\]](https://matrix.to/#/@mod:example.org)"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	tk := store.Entry{RepoURL: "https://github.com/n64recomp/n64recomp", OriginalSystem: "Nintendo 64", Kind: store.KindToolkit, OSes: []string{"linux"}}
	got = announcement(tk, "Mod", "@mod:example.org")
	want = "New toolkit added to the DB: **n64recomp**, a toolkit for Nintendo 64 games, running on Linux - https://github.com/n64recomp/n64recomp , by [Mod](https://matrix.to/#/@mod:example.org)"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
