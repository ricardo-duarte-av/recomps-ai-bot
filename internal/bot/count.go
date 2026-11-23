package bot

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

// formatCount answers "how many …?" questions and breakdowns ("per console") over
// an already-filtered search result. toolkits is how many toolkits were left out.
func formatCount(entries []store.Entry, toolkits int, intent *llm.Intent) string {
	var sb strings.Builder
	n := len(entries)
	fmt.Fprintf(&sb, "I have **%d** %s", n, countSubject(n, intent))
	if games := len(titlesOfEntries(entries)); n > 0 && intent.Kind != store.KindToolkit && games != n {
		fmt.Fprintf(&sb, ", for %s", plural(games, "game", "games"))
	}
	if intent.Kind == "" && intent.GroupBy != "kind" {
		if kinds := groupCounts(entries, "kind"); len(kinds) > 1 {
			sb.WriteString(": " + joinAnd(countLabels(kinds, true)))
		}
	}
	sb.WriteString(".")
	if toolkits > 0 {
		fmt.Fprintf(&sb, " Plus %s; ask for toolkits to see them.", plural(toolkits, "toolkit", "toolkits"))
	}
	if intent.GroupBy != "" && n > 0 {
		groups := groupCounts(entries, intent.GroupBy)
		sb.WriteString("\n\n")
		for i, line := range countLabels(groups, false) {
			if i == maxTitles {
				fmt.Fprintf(&sb, "- …and %d more\n", len(groups)-maxTitles)
				break
			}
			sb.WriteString("- " + line + "\n")
		}
	}
	return sb.String()
}

// countSubject describes what was counted, e.g. "decomps from PlayStation 2 running on Linux".
func countSubject(n int, intent *llm.Intent) string {
	s := plural(n, "entry", "entries")
	if intent.Kind != "" {
		s = plural(n, intent.Kind, intent.Kind+"s")
	}
	s = strings.TrimPrefix(s, fmt.Sprint(n)+" ")
	switch {
	case intent.Game != "":
		s += " of " + intent.Game
	case len(intent.Titles) > 0 && len(intent.Titles) <= 3:
		s += " of " + joinAnd(intent.Titles)
	}
	from := " from "
	if intent.Kind == store.KindToolkit {
		from = " for "
	}
	switch {
	case intent.OriginalSystem != "":
		s += from + intent.OriginalSystem
	case len(intent.Systems) > 0:
		s += from + joinAnd(intent.Systems)
	}
	if len(intent.OSes) > 0 {
		s += " running on " + oses.JoinDisplay(intent.OSes)
	}
	return s
}

type groupCount struct {
	label string
	n     int
}

// groupCounts counts entries per group, most first. For "platform" an entry counts
// once for each platform it runs on.
func groupCounts(entries []store.Entry, by string) []groupCount {
	counts := map[string]int{}
	for _, e := range entries {
		switch by {
		case "kind":
			counts[e.Kind]++
		case "system":
			counts[e.OriginalSystem]++
		case "game":
			counts[e.Game]++
		case "platform":
			for _, os := range e.OSes {
				counts[oses.Display(os)]++
			}
		}
	}
	out := make([]groupCount, 0, len(counts))
	for label, n := range counts {
		out = append(out, groupCount{label, n})
	}
	slices.SortFunc(out, func(a, b groupCount) int {
		return cmp.Or(b.n-a.n, strings.Compare(a.label, b.label))
	})
	return out
}

// countLabels renders groups as "7 recomps" (inline, for kinds) or "**Label**: 7".
func countLabels(groups []groupCount, inline bool) []string {
	out := make([]string, len(groups))
	for i, g := range groups {
		if inline {
			out[i] = plural(g.n, g.label, g.label+"s")
		} else {
			out[i] = fmt.Sprintf("**%s**: %d", g.label, g.n)
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
