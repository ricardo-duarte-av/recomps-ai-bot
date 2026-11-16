package bot

import (
	"strings"
	"testing"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

func TestAddsSomethingNew(t *testing.T) {
	existing := &store.Entry{Game: "Super Mario 64", OriginalSystem: "Nintendo 64", Kind: "decomp", OSes: []string{"windows", "linux"}}
	cases := []struct {
		name   string
		intent llm.Intent
		want   bool
	}{
		{"bare link", llm.Intent{}, false},
		{"same details", llm.Intent{Game: "Super Mario 64", Kind: "decomp", OSes: []string{"linux"}}, false},
		{"new platform", llm.Intent{OSes: []string{"macos"}}, true},
		{"different kind", llm.Intent{Kind: "recomp"}, true},
		{"different game", llm.Intent{Game: "Super Mario 64 DS"}, true},
	}
	for _, c := range cases {
		if got := addsSomethingNew(existing, &c.intent); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNoMatch(t *testing.T) {
	got := noMatch(&llm.Intent{Kind: "decomp", Game: "Halo", OSes: []string{"linux"}})
	if want := "I don't have any decomp in the list for Halo on Linux."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTitles(t *testing.T) {
	var entries []store.Entry
	for i := range 50 {
		entries = append(entries, store.Entry{Game: string(rune('A'+i%26)) + "game", OriginalSystem: "N64", RepoURL: string(rune('a' + i))})
	}
	out := formatTitles(entries)
	if !strings.Contains(out, "50 repositories for 26 games") || strings.Contains(out, "more") {
		t.Errorf("unexpected summary:\n%s", out)
	}
}
