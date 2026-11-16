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

const helpText = "I keep a list of game recomps and decomps. Try:\n\n" +
	"- `is there any recomp for Pokemon Emerald?`\n" +
	"- `add this repo to the list https://github.com/owner/repo` (moderators)\n" +
	"- `remove repo https://github.com/owner/repo from the list` (moderators)"

func (b *Bot) handleCommand(ctx context.Context, evt *event.Event, text string) {
	log := zerolog.Ctx(ctx)
	intent, err := b.llm.ParseIntent(ctx, text)
	if err != nil {
		log.Err(err).Msg("Failed to parse intent")
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
		b.handleQuery(ctx, evt, intent)
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

	var missing []string
	if entry.Game == "" {
		missing = append(missing, "the game")
	}
	if entry.OriginalSystem == "" {
		missing = append(missing, "the original system")
	}
	if entry.Kind == "" {
		missing = append(missing, "whether it's a recomp or a decomp")
	}
	if len(entry.OSes) == 0 {
		missing = append(missing, "the supported OSes")
	}
	if len(missing) > 0 {
		var why string
		if err != nil {
			why = fmt.Sprintf(" (couldn't read the README: %v)", err)
		}
		b.reply(ctx, evt, fmt.Sprintf(
			"I couldn't work out %s for %s%s. Please tell me, e.g. `add this repo to the list %s , a Super Mario 64 recomp for Nintendo 64, for windows, linux and macos`.",
			joinAnd(missing), repo.URL(), why, repo.URL()))
		return
	}

	if existing, err := b.store.GetByRepo(ctx, entry.RepoURL); err == nil && existing != nil &&
		existing.Game == entry.Game && existing.OriginalSystem == entry.OriginalSystem && existing.Kind == entry.Kind &&
		containsAll(existing.OSes, entry.OSes) {
		b.reply(ctx, evt, fmt.Sprintf("%s is already in the list for %s.", entry.RepoURL, oses.JoinDisplay(existing.OSes)))
		return
	}

	msg := fmt.Sprintf("Adding %s , a %s of **%s** from %s, for %s.\n\nA moderator can react with **%s** to confirm or **%s** to cancel.",
		entry.RepoURL, entry.Kind, entry.Game, entry.OriginalSystem, oses.JoinDisplay(entry.OSes), reactOK, reactCancel)
	b.prompt(ctx, evt, "add", entry, msg, false)
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
	msg := fmt.Sprintf("Removing %s from %s for game **%s** on %s.\n\nA moderator can react with **%s** to confirm or **%s** to cancel.",
		entry.RepoURL, oses.JoinDisplay(entry.OSes), entry.Game, entry.OriginalSystem, reactOK, reactCancel)
	b.prompt(ctx, evt, "remove", entry, msg, false)
}

// prompt posts a confirmation message, seeds the OK/Cancel reactions and records the pending action.
func (b *Bot) prompt(ctx context.Context, evt *event.Event, action string, entry store.Entry, msg string, mentionRoom bool) {
	log := zerolog.Ctx(ctx)
	promptID := b.send(ctx, evt, msg, mentionRoom)
	if promptID == "" {
		return
	}
	err := b.store.SavePending(ctx, store.Pending{
		EventID: promptID.String(), RoomID: evt.RoomID.String(), Action: action,
		Entry: entry, RequestedBy: evt.Sender.String(),
	})
	if err != nil {
		log.Err(err).Msg("Failed to save pending action")
		b.reply(ctx, evt, "Database error, sorry.")
		return
	}
	for _, key := range []string{reactOK, reactCancel} {
		if _, err := b.client.SendReaction(ctx, evt.RoomID, promptID, key); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to add reaction")
		}
	}
}

func (b *Bot) handleQuery(ctx context.Context, evt *event.Event, intent *llm.Intent) {
	log := zerolog.Ctx(ctx)
	known, err := b.store.Games(ctx)
	if err != nil {
		log.Err(err).Msg("Failed to list games")
		b.reply(ctx, evt, "Database error, sorry.")
		return
	}
	if len(known) == 0 {
		b.reply(ctx, evt, "The list is empty so far.")
		return
	}

	var query string
	switch {
	case intent.Game != "" && intent.OriginalSystem != "":
		query = intent.Game + " (originally on " + intent.OriginalSystem + ")"
	case intent.Game != "":
		query = intent.Game
	case intent.OriginalSystem != "":
		query = "any game originally released on " + intent.OriginalSystem
	}

	var entries []store.Entry
	if query == "" {
		entries, err = b.store.GetByGames(ctx, titlesOf(known))
	} else {
		var titles []string
		titles, err = b.llm.MatchTitles(ctx, query, known)
		if err != nil {
			log.Warn().Err(err).Msg("LLM title matching failed, falling back to substring search")
			if intent.Game != "" {
				entries, err = b.store.SearchGames(ctx, intent.Game)
			}
		} else {
			entries, err = b.store.GetByGames(ctx, titles)
		}
	}
	if err != nil {
		log.Err(err).Msg("Query failed")
		b.reply(ctx, evt, "Database error, sorry.")
		return
	}

	subject := intent.Game
	if subject == "" {
		subject = intent.OriginalSystem
	}
	if len(intent.OSes) > 0 {
		entries = filterOSes(entries, intent.OSes)
	}
	if intent.Kind != "" {
		entries = slices.DeleteFunc(entries, func(e store.Entry) bool { return e.Kind != intent.Kind })
	}
	if len(entries) == 0 {
		what := "anything"
		if intent.Kind != "" {
			what = "any " + intent.Kind
		}
		msg := fmt.Sprintf("I don't have %s in the list", what)
		if subject != "" {
			msg += " for " + subject
		}
		if len(intent.OSes) > 0 {
			msg += " on " + oses.JoinDisplay(intent.OSes)
		}
		b.reply(ctx, evt, msg+".")
		return
	}
	b.reply(ctx, evt, formatEntries(entries))
}

func formatEntries(entries []store.Entry) string {
	var sb strings.Builder
	lastGame := ""
	for _, e := range entries {
		header := e.Game + "\x00" + e.OriginalSystem
		if header != lastGame {
			if lastGame != "" {
				sb.WriteString("\n")
			}
			fmt.Fprintf(&sb, "**%s** (%s)\n", e.Game, e.OriginalSystem)
			lastGame = header
		}
		fmt.Fprintf(&sb, "- %s — %s — %s\n", e.Kind, oses.JoinDisplay(sortOSes(e.OSes)), e.RepoURL)
	}
	return sb.String()
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

func titlesOf(known []string) []string {
	out := make([]string, len(known))
	for i, k := range known {
		out[i], _, _ = strings.Cut(k, " | ")
	}
	return slices.Compact(out)
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
	return fmt.Sprintf("[%s](%s)", userID.String(), userID.URI().MatrixToURL())
}
