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

func TestParseExtraCommand(t *testing.T) {
	cmd, ok := parseExtraCommand("add extra https://github.com/papand7/nightfire-recomp\n```\nFile1: https://file1.com\n```")
	if !ok || cmd.clear || strings.TrimSpace(cmd.head) != "https://github.com/papand7/nightfire-recomp" {
		t.Fatalf("got %+v, %v", cmd, ok)
	}
	if cmd, ok := parseExtraCommand("remove extra https://github.com/a/b"); !ok || !cmd.clear {
		t.Fatalf("remove: got %+v, %v", cmd, ok)
	}
	for _, s := range []string{"add this repo https://github.com/a/b", "is there an extra recomp for Halo?", "add extras2 x"} {
		if _, ok := parseExtraCommand(s); ok {
			t.Errorf("%q should not be an extra command", s)
		}
	}
}

func TestExtractCodeBlock(t *testing.T) {
	body := "@recompbot add extra https://github.com/papand7/nightfire-recomp\n```text\nThis is the text\nFile1: https://file1.com\n```\n"
	got, ok := extractCodeBlock(body, "")
	if want := "This is the text\nFile1: https://file1.com"; !ok || got != want {
		t.Fatalf("got %q, %v", got, ok)
	}
	got, ok = extractCodeBlock("add extra x", `add extra x<pre><code class="language-text">a &amp; b
c
</code></pre>`)
	if !ok || got != "a & b\nc" {
		t.Fatalf("html: got %q, %v", got, ok)
	}
	if _, ok := extractCodeBlock("add extra x", "add extra x"); ok {
		t.Fatal("expected no block")
	}
}
