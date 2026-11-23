package bot

import (
	"strings"
	"testing"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

var askEntries = []store.Entry{
	{Game: "Pokémon Emerald", OriginalSystem: "Game Boy Advance", Kind: "decomp", OSes: []string{"linux"}, RepoURL: "https://github.com/pret/pokeemerald"},
	{Game: "Super Mario 64", OriginalSystem: "Nintendo 64", Kind: "decomp", OSes: []string{"windows", "linux"}, RepoURL: "https://github.com/n64decomp/sm64"},
	{Game: "Super Mario 64", OriginalSystem: "Nintendo 64", Kind: "recomp", OSes: []string{"windows", "linux", "macos"}, RepoURL: "https://github.com/a/sm64recomp"},
	{OriginalSystem: "Nintendo 64", Kind: "toolkit", OSes: []string{"linux"}, RepoURL: "https://github.com/N64Recomp/N64Recomp"},
}

func TestListToolsSearch(t *testing.T) {
	tools := &listTools{entries: askEntries}
	res := tools.Search(llm.SearchArgs{GameContains: "pokemon", Limit: 10}).(searchResult)
	if res.TotalRepos != 1 || res.Entries[0].Game != "Pokémon Emerald" {
		t.Errorf("accent-insensitive search: %+v", res)
	}
	res = tools.Search(llm.SearchArgs{Kinds: []string{"decomp", "recomp"}, PlatformsNone: []string{"macos"}, PlatformsAll: []string{"windows", "linux"}, Limit: 10}).(searchResult)
	if res.TotalRepos != 1 || res.Entries[0].URL != "https://github.com/n64decomp/sm64" {
		t.Errorf("platform filters: %+v", res)
	}
	res = tools.Search(llm.SearchArgs{Systems: []string{"Nintendo 64"}, GroupBy: "kind", Limit: 1}).(searchResult)
	if res.TotalRepos != 3 || res.DistinctGames != 1 || len(res.Entries) != 1 || !res.Truncated || len(res.Groups) != 3 {
		t.Errorf("grouped, limited search: %+v", res)
	}
	vals := tools.ListValues("game").(map[string]any)["values"].([]toolGroup)
	if len(vals) != 2 || vals[0] != (toolGroup{"Super Mario 64", 2}) {
		t.Errorf("list_values game: %+v", vals)
	}
}

func TestSanitizeAnswer(t *testing.T) {
	in := "See [sm64](https://github.com/n64decomp/sm64) and https://github.com/n64decomp/sm64/releases. " +
		"Download [here](https://evil.example/x.exe) or https://evil.example/y, or [https://evil.example](https://evil.example). " +
		"Not https://github.com/n64decomp/sm64evil either."
	got := sanitizeAnswer(in, askEntries)
	want := "See [sm64](https://github.com/n64decomp/sm64) and https://github.com/n64decomp/sm64/releases. " +
		"Download here or (link removed), or (link removed). " +
		"Not (link removed) either."
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if long := sanitizeAnswer(strings.Repeat("é", maxAnswerLen+10), nil); len([]rune(long)) != maxAnswerLen+1 {
		t.Errorf("length cap: %d runes", len([]rune(long)))
	}
}
