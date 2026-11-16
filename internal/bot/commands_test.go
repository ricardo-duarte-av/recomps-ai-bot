package bot

import (
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
