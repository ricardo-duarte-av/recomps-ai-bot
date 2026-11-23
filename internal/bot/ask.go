package bot

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/rs/zerolog"
	"golang.org/x/text/unicode/norm"
	"maunium.net/go/mautrix/event"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

// maxAnswerLen caps a model-written answer (in runes).
const maxAnswerLen = 4000

// handleAsk answers questions the structured filters can't express by letting the
// model call read-only tools over a snapshot of the list. The tools never touch the
// database, and the answer is posted without pings and with links checked against
// the list.
func (b *Bot) handleAsk(ctx context.Context, evt *event.Event, text string) {
	log := zerolog.Ctx(ctx)
	all, err := b.store.All(ctx)
	if err != nil {
		log.Err(err).Msg("Query failed")
		markFailed(ctx)
		b.reply(ctx, evt, "Database error, sorry.")
		return
	}
	if len(all) == 0 {
		b.reply(ctx, evt, "The list is empty so far.")
		return
	}
	answer, err := b.llm.Ask(ctx, text, &listTools{entries: all})
	if err != nil {
		log.Err(err).Msg("Failed to answer question")
		markFailed(ctx)
		b.reply(ctx, evt, "Sorry, I couldn't answer that right now (AI request failed).")
		return
	}
	b.replyNoPings(ctx, evt, sanitizeAnswer(answer, all))
}

// listTools implements llm.AskTools over an in-memory copy of the list.
type listTools struct {
	entries []store.Entry
}

type toolEntry struct {
	Game      string   `json:"game,omitempty"`
	System    string   `json:"original_system"`
	Kind      string   `json:"kind"`
	Platforms []string `json:"platforms"`
	URL       string   `json:"url"`
}

type toolGroup struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type searchResult struct {
	TotalRepos    int         `json:"total_repos"`
	DistinctGames int         `json:"distinct_games"`
	Groups        []toolGroup `json:"groups,omitempty"`
	Entries       []toolEntry `json:"entries"`
	Truncated     bool        `json:"truncated,omitempty"`
}

func (t *listTools) Search(a llm.SearchArgs) any {
	needle := fold(a.GameContains)
	var matched []store.Entry
	for _, e := range t.entries {
		switch {
		case needle != "" && !strings.Contains(fold(e.Game), needle),
			len(a.Titles) > 0 && !slices.Contains(a.Titles, e.Game),
			len(a.Systems) > 0 && !slices.Contains(a.Systems, e.OriginalSystem),
			len(a.Kinds) > 0 && !slices.Contains(a.Kinds, e.Kind),
			len(a.PlatformsAny) > 0 && !slices.ContainsFunc(a.PlatformsAny, func(p string) bool { return slices.Contains(e.OSes, p) }),
			!containsAll(e.OSes, a.PlatformsAll),
			slices.ContainsFunc(a.PlatformsNone, func(p string) bool { return slices.Contains(e.OSes, p) }):
			continue
		}
		matched = append(matched, e)
	}
	games := titlesOfEntries(slices.DeleteFunc(slices.Clone(matched), func(e store.Entry) bool { return e.Kind == store.KindToolkit }))
	res := searchResult{TotalRepos: len(matched), DistinctGames: len(games), Entries: []toolEntry{}}
	if a.GroupBy != "" {
		res.Groups = toolGroups(groupCounts(matched, a.GroupBy))
	}
	for i, e := range matched {
		if i == a.Limit {
			res.Truncated = true
			break
		}
		res.Entries = append(res.Entries, toToolEntry(e))
	}
	return res
}

func (t *listTools) ListValues(field string) any {
	return map[string]any{"field": field, "values": toolGroups(groupCounts(t.entries, field))}
}

func toolGroups(groups []groupCount) []toolGroup {
	out := make([]toolGroup, 0, len(groups))
	for _, g := range groups {
		if g.label == "" {
			continue // toolkits have no game
		}
		out = append(out, toolGroup{g.label, g.n})
	}
	return out
}

func toToolEntry(e store.Entry) toolEntry {
	platforms := make([]string, 0, len(e.OSes))
	for _, os := range sortOSes(e.OSes) {
		platforms = append(platforms, oses.Display(os))
	}
	return toolEntry{Game: e.Game, System: e.OriginalSystem, Kind: e.Kind, Platforms: platforms, URL: e.RepoURL}
}

// fold lowercases s and strips accents, so "pokemon" matches "Pokémon".
func fold(s string) string {
	var sb strings.Builder
	for _, r := range norm.NFD.String(s) {
		if !unicode.Is(unicode.Mn, r) {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}

var (
	mdLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)\)`)
	bareURLRe = regexp.MustCompile("(?i)\\b(?:https?|ftp)://[^\\s<>()\\[\\]\"'`]+")
)

// sanitizeAnswer makes a model-written answer safe to post: links that don't point
// into a repository on the list are removed (so a crafted question can't make the
// bot recommend arbitrary downloads), and the text is length-capped.
func sanitizeAnswer(answer string, entries []store.Entry) string {
	allowed := make([]string, 0, len(entries))
	for _, e := range entries {
		allowed = append(allowed, normalizeURL(e.RepoURL))
	}
	ok := func(u string) bool {
		u = normalizeURL(u)
		return slices.ContainsFunc(allowed, func(a string) bool { return u == a || strings.HasPrefix(u, a+"/") })
	}
	answer = mdLinkRe.ReplaceAllStringFunc(answer, func(m string) string {
		sub := mdLinkRe.FindStringSubmatch(m)
		if ok(sub[2]) {
			return m
		}
		return sub[1]
	})
	answer = bareURLRe.ReplaceAllStringFunc(answer, func(u string) string {
		trimmed := strings.TrimRight(u, ".,;:!?*_~")
		if ok(trimmed) {
			return u
		}
		return "(link removed)" + u[len(trimmed):]
	})
	if r := []rune(answer); len(r) > maxAnswerLen {
		answer = string(r[:maxAnswerLen]) + "…"
	}
	return answer
}

func normalizeURL(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
}
