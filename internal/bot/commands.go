package bot

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/forge"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

const (
	reactOK     = "OK"
	reactCancel = "Cancel"
)

const helpText = "I keep a list of game recomps, decomps and reimplementations, and of the toolkits used to make them. Try:\n\n" +
	"- `is there any recomp for Pokemon Emerald?`\n" +
	"- `which toolkits are there for N64?`\n" +
	"- `how many decomps are there for PS2?` or `how many entries per console?`\n" +
	"- `which games have both a recomp and a decomp?`\n" +
	"- `add this repo to the list https://github.com/owner/repo` (moderators)\n" +
	"- `remove repo https://github.com/owner/repo from the list` (moderators)\n" +
	"- `add extra https://github.com/owner/repo` followed by a ``` code block with notes or links (moderators; `remove extra <repo>` clears it)"

func (b *Bot) handleCommand(ctx context.Context, evt *event.Event, text string) {
	log := zerolog.Ctx(ctx)
	if cmd, ok := parseExtraCommand(text); ok {
		b.handleExtra(ctx, evt, cmd)
		return
	}
	known, err := b.store.Games(ctx)
	if err != nil {
		log.Err(err).Msg("Failed to list games")
		markFailed(ctx)
		b.reply(ctx, evt, "Database error, sorry.")
		return
	}
	intent, err := b.llm.ParseIntent(ctx, text, known)
	if err != nil {
		log.Err(err).Msg("Failed to parse intent")
		markFailed(ctx)
		b.reply(ctx, evt, "Sorry, I couldn't understand that right now (AI request failed).")
		return
	}
	log.Debug().Interface("intent", intent).Str("text", text).Msg("Parsed intent")

	switch intent.Intent {
	case "add":
		b.handleAdd(ctx, evt, text, intent)
	case "remove":
		b.handleRemove(ctx, evt, text, intent)
	case "query":
		if repo, ok := b.repoFromIntent(text, intent); ok {
			b.handleRepoQuery(ctx, evt, repo)
		} else if intent.HasFilter() || intent.ListAll || intent.Count || intent.GroupBy != "" {
			b.handleQuery(ctx, evt, intent)
		} else {
			b.reply(ctx, evt, helpText)
		}
	case "complex_query":
		b.handleAsk(ctx, evt, text)
	default:
		b.reply(ctx, evt, helpText)
	}
}

// repoFromIntent prefers a URL literally present in the message over the LLM's copy of it.
func (b *Bot) repoFromIntent(text string, intent *llm.Intent) (forge.Repo, bool) {
	if r, ok := b.forge.FindRepo(text); ok {
		return r, true
	}
	if r, err := b.forge.ParseRepo(intent.RepoURL); err == nil {
		return r, true
	}
	return forge.Repo{}, false
}

func (b *Bot) handleAdd(ctx context.Context, evt *event.Event, text string, intent *llm.Intent) {
	log := zerolog.Ctx(ctx)
	if !b.isModerator(ctx, evt.RoomID, evt.Sender) {
		b.reply(ctx, evt, "Only moderators can add entries to the list.")
		return
	}
	repo, ok := b.repoFromIntent(text, intent)
	if !ok {
		b.reply(ctx, evt, "I need a repository URL to add (GitHub, GitLab, Gitea, Forgejo or any other git host), e.g. `add this repo to the list https://github.com/owner/repo`.")
		return
	}

	// Check the list before spending a README fetch and an AI call: a repo that's
	// already there only needs work if the moderator stated something new about it.
	existing, err := b.store.GetByRepo(ctx, repo.URL())
	if err != nil {
		log.Err(err).Msg("Failed to look up repo")
		markFailed(ctx)
		b.reply(ctx, evt, "Database error, sorry.")
		return
	}
	if existing != nil && !addsSomethingNew(existing, intent) {
		b.reply(ctx, evt, fmt.Sprintf("%s is already in the list: %s.", existing.RepoURL, describe(*existing)))
		return
	}

	entry := store.Entry{RepoURL: repo.URL()}
	readme, err := b.forge.FetchReadme(ctx, repo)
	if err != nil {
		log.Warn().Err(err).Str("repo", repo.URL()).Msg("Failed to fetch README")
	} else {
		info, err := b.llm.ExtractFromReadme(ctx, repo.URL(), readme)
		if err != nil {
			log.Err(err).Msg("Failed to extract info from README")
		} else {
			entry.Game, entry.OriginalSystem, entry.Kind, entry.OSes = info.Game, info.OriginalSystem, info.Kind, info.OSes
		}
	}
	// Whatever the moderator stated explicitly wins over the README.
	if intent.Game != "" {
		entry.Game = intent.Game
	}
	if intent.OriginalSystem != "" {
		entry.OriginalSystem = intent.OriginalSystem
	}
	if intent.Kind != "" {
		entry.Kind = intent.Kind
	}
	if len(intent.OSes) > 0 {
		entry.OSes = intent.OSes
	}
	if entry.Kind == store.KindToolkit {
		entry.Game = "" // toolkits aren't tied to one game
	}

	var missing []string
	if entry.Game == "" && entry.Kind != store.KindToolkit {
		missing = append(missing, "the game")
	}
	if entry.OriginalSystem == "" {
		missing = append(missing, "the original system")
	}
	if entry.Kind == "" {
		missing = append(missing, "whether it's a recomp, a decomp, a reimpl or a toolkit")
	}
	if len(entry.OSes) == 0 {
		missing = append(missing, "the supported platforms")
	}
	if len(missing) > 0 {
		var why string
		if err != nil {
			why = fmt.Sprintf(" (couldn't read the README: %v)", err)
		}
		markFailed(ctx)
		b.reply(ctx, evt, fmt.Sprintf(
			"I couldn't work out %s for %s%s. Please tell me, e.g. `add this repo to the list %s , a Super Mario 64 recomp for Nintendo 64, for windows, linux and macos`.",
			joinAnd(missing), repo.URL(), why, repo.URL()))
		return
	}

	if existing != nil &&
		existing.Game == entry.Game && existing.OriginalSystem == entry.OriginalSystem && existing.Kind == entry.Kind &&
		containsAll(existing.OSes, entry.OSes) {
		b.reply(ctx, evt, fmt.Sprintf("%s is already in the list for %s.", entry.RepoURL, oses.JoinDisplay(existing.OSes)))
		return
	}

	msg := fmt.Sprintf("Adding %s , a %s.\n\nA moderator can react with **%s** to confirm or **%s** to cancel.",
		entry.RepoURL, describe(entry), reactOK, reactCancel)
	b.prompt(ctx, evt, "add", entry, msg, nil)
}

func (b *Bot) handleRemove(ctx context.Context, evt *event.Event, text string, intent *llm.Intent) {
	if !b.isModerator(ctx, evt.RoomID, evt.Sender) {
		b.reply(ctx, evt, "Only moderators can remove entries from the list.")
		return
	}
	repo, ok := b.repoFromIntent(text, intent)
	if !ok {
		b.reply(ctx, evt, "Which repository? e.g. `remove repo https://github.com/owner/repo from the list`.")
		return
	}
	existing, err := b.store.GetByRepo(ctx, repo.URL())
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to look up repo")
		markFailed(ctx)
		b.reply(ctx, evt, "Database error, sorry.")
		return
	} else if existing == nil {
		b.reply(ctx, evt, fmt.Sprintf("%s isn't in the list.", repo.URL()))
		return
	}
	entry := *existing
	// Optionally only remove some OSes ("remove macos for repo X").
	if len(intent.OSes) > 0 {
		var subset []string
		for _, os := range existing.OSes {
			if slices.Contains(intent.OSes, os) {
				subset = append(subset, os)
			}
		}
		if len(subset) == 0 {
			b.reply(ctx, evt, fmt.Sprintf("%s is only listed for %s.", repo.URL(), oses.JoinDisplay(existing.OSes)))
			return
		}
		entry.OSes = subset
	}
	msg := fmt.Sprintf("Removing %s from %s for %s.\n\nA moderator can react with **%s** to confirm or **%s** to cancel.",
		entry.RepoURL, oses.JoinDisplay(entry.OSes), subject(entry), reactOK, reactCancel)
	b.prompt(ctx, evt, "remove", entry, msg, nil)
}

// prompt posts a confirmation message, seeds the OK/Cancel reactions and records the pending action.
func (b *Bot) prompt(ctx context.Context, evt *event.Event, action string, entry store.Entry, msg string, mentions *event.Mentions) {
	log := zerolog.Ctx(ctx)
	promptID := b.send(ctx, evt, msg, mentions)
	if promptID == "" {
		return
	}
	err := b.store.SavePending(ctx, store.Pending{
		EventID: promptID.String(), RoomID: evt.RoomID.String(), Action: action,
		Entry: entry, RequestedBy: evt.Sender.String(),
	})
	if err != nil {
		log.Err(err).Msg("Failed to save pending action")
		markFailed(ctx)
		b.reply(ctx, evt, "Database error, sorry.")
		return
	}
	for _, key := range []string{reactOK, reactCancel} {
		if _, err := b.client.SendReaction(ctx, evt.RoomID, promptID, key); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to add reaction")
		}
	}
	// Pinning open link-detection prompts makes them easy to find from the room's pinned events.
	if action == "add" && entry.Detected {
		b.setPinned(ctx, evt.RoomID, promptID, true)
	}
}

// addsSomethingNew reports whether an add request states details that differ from
// what's already stored for the repo (so it's an update rather than a duplicate).
func addsSomethingNew(existing *store.Entry, intent *llm.Intent) bool {
	return (intent.Game != "" && intent.Game != existing.Game) ||
		(intent.OriginalSystem != "" && intent.OriginalSystem != existing.OriginalSystem) ||
		(intent.Kind != "" && intent.Kind != existing.Kind) ||
		!containsAll(existing.OSes, intent.OSes)
}

// handleRepoQuery answers "is <repo> in the list?".
func (b *Bot) handleRepoQuery(ctx context.Context, evt *event.Event, repo forge.Repo) {
	existing, err := b.store.GetByRepo(ctx, repo.URL())
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to look up repo")
		markFailed(ctx)
		b.reply(ctx, evt, "Database error, sorry.")
		return
	}
	if existing == nil {
		msg := fmt.Sprintf("%s isn't in the list.", repo.URL())
		if pending, err := b.store.HasPendingAddForRepo(ctx, repo.URL()); err == nil && pending {
			msg += " It's waiting for a moderator to confirm adding it."
		}
		b.reply(ctx, evt, msg)
		return
	}
	msg := fmt.Sprintf("Yes, %s is in the list: %s.", existing.RepoURL, describe(*existing))
	if existing.Extra != "" {
		msg += formatExtra(existing.Extra)
	}
	b.reply(ctx, evt, msg)
}

// maxListed is how many repositories one reply may list with links; beyond that the
// bot lists just the game titles so it doesn't flood the room.
const maxListed = 15

// maxTitles caps the titles-only summary.
const maxTitles = 40

// handleQuery runs a search: every filter the user stated (games, original systems,
// kind, target platforms) narrows the list, resolved by the intent call. Toolkits
// only show up when asked for; otherwise the reply just mentions how many matched.
func (b *Bot) handleQuery(ctx context.Context, evt *event.Event, intent *llm.Intent) {
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

	titles := intent.Titles
	if len(titles) == 0 && intent.Game != "" {
		// The model found no match; a plain substring search catches anything it missed.
		if found, err := b.store.SearchGames(ctx, intent.Game); err == nil {
			titles = titlesOfEntries(found)
		}
		if len(titles) == 0 {
			b.reply(ctx, evt, noMatch(intent))
			return
		}
	}
	if len(intent.Systems) == 0 && intent.OriginalSystem != "" && len(titles) == 0 {
		// A system the list doesn't have at all.
		b.reply(ctx, evt, noMatch(intent))
		return
	}

	entries := slices.DeleteFunc(all, func(e store.Entry) bool {
		return (len(titles) > 0 && !slices.Contains(titles, e.Game)) ||
			(len(intent.Systems) > 0 && !slices.Contains(intent.Systems, e.OriginalSystem)) ||
			(intent.Kind != "" && e.Kind != intent.Kind)
	})
	if len(intent.OSes) > 0 {
		entries = filterOSes(entries, intent.OSes)
	}
	entries, toolkits := splitToolkits(entries, intent.Kind)
	if intent.Count || intent.GroupBy != "" {
		b.reply(ctx, evt, formatCount(entries, toolkits, intent))
		return
	}
	var msg string
	switch {
	case len(entries) == 0:
		msg = noMatch(intent)
	case len(entries) > maxListed:
		msg = formatTitles(entries)
	default:
		msg = formatEntries(entries)
	}
	b.reply(ctx, evt, msg+toolkitsHint(toolkits, len(entries) > 0))
}

// splitToolkits removes toolkits from a search result unless the user asked for
// them (kind), returning how many were removed.
func splitToolkits(entries []store.Entry, kind string) ([]store.Entry, int) {
	if kind == store.KindToolkit {
		return entries, 0
	}
	n := len(entries)
	entries = slices.DeleteFunc(entries, func(e store.Entry) bool { return e.Kind == store.KindToolkit })
	return entries, n - len(entries)
}

// toolkitsHint mentions toolkits that a search matched but didn't show.
func toolkitsHint(n int, shown bool) string {
	if n == 0 {
		return ""
	}
	what := "a toolkit matches"
	if n > 1 {
		what = fmt.Sprintf("%d toolkits match", n)
	}
	if shown {
		return fmt.Sprintf("\n\n(%s too; ask for toolkits to see them.)", what)
	}
	return fmt.Sprintf(" However, %s; ask for toolkits to see them.", what)
}

// noMatch describes an empty search result in terms of what the user asked.
func noMatch(intent *llm.Intent) string {
	what := "anything"
	if intent.Kind != "" {
		what = "any " + intent.Kind
	}
	msg := "I don't have " + what + " in the list"
	from := " from "
	if intent.Kind == store.KindToolkit {
		from = " for "
	}
	switch {
	case intent.Game != "":
		msg += " for " + intent.Game
	case len(intent.Titles) > 0:
		msg += " for " + joinAnd(intent.Titles)
	}
	switch {
	case intent.OriginalSystem != "":
		msg += from + intent.OriginalSystem
	case len(intent.Systems) > 0:
		msg += from + joinAnd(intent.Systems)
	}
	if len(intent.OSes) > 0 {
		msg += " on " + oses.JoinDisplay(intent.OSes)
	}
	return msg + "."
}

// formatTitles summarizes a large result as game titles (or toolkit systems) only.
func formatTitles(entries []store.Entry) string {
	var groups []string
	for _, e := range entries {
		if h := heading(e); !slices.Contains(groups, h) {
			groups = append(groups, h)
		}
	}
	var sb strings.Builder
	what, ask := "games", "game"
	if entries[0].Kind == store.KindToolkit {
		what, ask = "systems", "system" // a large result is all toolkits or none
	}
	fmt.Fprintf(&sb, "%d repositories for %d %s match. Ask about a specific %s for the links.\n\n", len(entries), len(groups), what, ask)
	for i, g := range groups {
		if i == maxTitles {
			fmt.Fprintf(&sb, "- …and %d more\n", len(groups)-maxTitles)
			break
		}
		fmt.Fprintf(&sb, "- %s\n", g)
	}
	return sb.String()
}

func titlesOfEntries(entries []store.Entry) []string {
	var out []string
	for _, e := range entries {
		if !slices.Contains(out, e.Game) {
			out = append(out, e.Game)
		}
	}
	return out
}

func formatEntries(entries []store.Entry) string {
	var sb strings.Builder
	lastHeading := ""
	for _, e := range entries {
		if h := heading(e); h != lastHeading {
			if lastHeading != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(h + "\n")
			lastHeading = h
		}
		fmt.Fprintf(&sb, "- %s — %s — %s\n", e.Kind, oses.JoinDisplay(sortOSes(e.OSes)), e.RepoURL)
	}
	return sb.String()
}

// heading groups search results: "**Game** (System)", or "**System** toolkits".
func heading(e store.Entry) string {
	if e.Kind == store.KindToolkit {
		return fmt.Sprintf("**%s** toolkits", e.OriginalSystem)
	}
	return fmt.Sprintf("**%s** (%s)", e.Game, e.OriginalSystem)
}

// describe says what an entry is, e.g. "recomp of **Game** from System, for
// Windows and Linux" or "toolkit for **System** games, running on Linux".
func describe(e store.Entry) string {
	platforms := oses.JoinDisplay(sortOSes(e.OSes))
	if e.Kind == store.KindToolkit {
		return fmt.Sprintf("toolkit for **%s** games, running on %s", e.OriginalSystem, platforms)
	}
	return fmt.Sprintf("%s of **%s** from %s, for %s", e.Kind, e.Game, e.OriginalSystem, platforms)
}

// subject names what an entry is for: "game **Game** on System" or "the **System** toolkit".
func subject(e store.Entry) string {
	if e.Kind == store.KindToolkit {
		return fmt.Sprintf("the **%s** toolkit", e.OriginalSystem)
	}
	return fmt.Sprintf("game **%s** on %s", e.Game, e.OriginalSystem)
}

func filterOSes(entries []store.Entry, want []string) []store.Entry {
	var out []store.Entry
	for _, e := range entries {
		var keep []string
		for _, os := range e.OSes {
			if slices.Contains(want, os) {
				keep = append(keep, os)
			}
		}
		if len(keep) > 0 {
			e.OSes = keep
			out = append(out, e)
		}
	}
	return out
}

func sortOSes(list []string) []string {
	out := slices.Clone(list)
	slices.SortFunc(out, func(a, b string) int { return slices.Index(oses.All, a) - slices.Index(oses.All, b) })
	return out
}

func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

func joinAnd(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// mention renders a user mention for messages.
func mention(userID id.UserID) string {
	return fmt.Sprintf("[%s](%s)", userID.String(), matrixToURL(userID))
}
