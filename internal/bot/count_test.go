package bot

import (
	"strings"
	"testing"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

var countEntries = []store.Entry{
	{Game: "Super Mario 64", OriginalSystem: "Nintendo 64", Kind: "decomp", OSes: []string{"windows", "linux"}, RepoURL: "a"},
	{Game: "Super Mario 64", OriginalSystem: "Nintendo 64", Kind: "recomp", OSes: []string{"linux"}, RepoURL: "b"},
	{Game: "Bloodborne", OriginalSystem: "PlayStation 4", Kind: "recomp", OSes: []string{"windows"}, RepoURL: "c"},
}

func TestFormatCount(t *testing.T) {
	got := formatCount(countEntries, 2, &llm.Intent{Count: true})
	want := "I have **3** entries, for 2 games: 2 recomps and 1 decomp. Plus 2 toolkits; ask for toolkits to see them."
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}

	got = formatCount(countEntries[2:], 0, &llm.Intent{Count: true, Kind: "recomp", OriginalSystem: "PS4", OSes: []string{"windows"}})
	if want := "I have **1** recomp from PS4 running on Windows."; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}

	got = formatCount(nil, 0, &llm.Intent{Count: true, Kind: "decomp"})
	if want := "I have **0** decomps."; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatCountGroupBy(t *testing.T) {
	got := formatCount(countEntries, 0, &llm.Intent{GroupBy: "system"})
	if !strings.Contains(got, "- **Nintendo 64**: 2\n- **PlayStation 4**: 1\n") {
		t.Errorf("system breakdown:\n%s", got)
	}
	got = formatCount(countEntries, 0, &llm.Intent{GroupBy: "platform"})
	if !strings.Contains(got, "- **Linux**: 2\n- **Windows**: 2\n") {
		t.Errorf("platform breakdown:\n%s", got)
	}
	got = formatCount(countEntries, 0, &llm.Intent{GroupBy: "kind"})
	if strings.Contains(got, "2 recomps and") || !strings.Contains(got, "- **recomp**: 2\n- **decomp**: 1\n") {
		t.Errorf("kind breakdown:\n%s", got)
	}
}
