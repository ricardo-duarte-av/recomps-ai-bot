package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/config"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/forge"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

// detectLinks looks for git repository links in a regular room message and offers
// to add any recomp/decomp/reimpl/toolkit that isn't in the list yet.
func (b *Bot) detectLinks(evt *event.Event, body string) {
	var repos []forge.Repo
	for _, r := range b.forge.FindRepos(body) {
		if forge.LikelyForge(r) {
			repos = append(repos, r)
		}
		if len(repos) >= b.cfg.Detect.MaxLinksPerMessage {
			break
		}
	}
	if len(repos) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ctx = b.log.With().Str("component", "detect").Stringer("room", evt.RoomID).
			Stringer("event", evt.ID).Logger().WithContext(ctx)

		// ⏳ goes on the message only once a link actually needs checking, so
		// dismissed links stay quiet. Each link already in the list gets a reaction
		// naming the repo (e.g. "github.com/owner/repo") instead.
		var prog *progress
		defer func() {
			if prog != nil {
				prog.finish(ctx)
			}
		}()
		// Whether this message's repos may be added without asking, looked up once
		// and only when a repo actually needs it.
		var auto *bool
		canAuto := func(ctx context.Context) bool {
			if auto == nil {
				v := b.canAutoAdd(ctx, evt)
				auto = &v
			}
			return *auto
		}
		for _, repo := range repos {
			url := repo.URL()
			unlock := b.lockRepo(url)
			rctx := zerolog.Ctx(ctx).With().Str("repo", url).Logger().WithContext(ctx)
			state, entry := b.detectionState(rctx, url, evt.RoomID)
			switch state {
			case detectKnown:
				label := repo.Host + "/" + repo.Path
				if b.autoAddedElsewhere(url, evt.RoomID) {
					label = outcomeAutoAddedElsewhere
				}
				b.react(rctx, evt, label)
			case detectAskedElsewhere:
				// Already classified: no README or AI needed.
				if canAuto(rctx) && len(missingInfo(*entry)) == 0 {
					b.react(rctx, evt, b.autoAddOutcome(rctx, evt, repo, *entry))
					break
				}
				zerolog.Ctx(rctx).Info().Msg("Suggesting already classified repo")
				b.suggest(rctx, evt, *entry)
			case detectNew:
				if prog == nil {
					ctx, prog = b.startWork(ctx, evt)
					rctx = zerolog.Ctx(ctx).With().Str("repo", url).Logger().WithContext(ctx)
				}
				b.detectRepo(rctx, evt, repo, canAuto)
			}
			unlock()
		}
	}()
}

type detectState int

const (
	detectSkip           detectState = iota // dismissed, recently checked, or already asked here
	detectKnown                             // already in the list
	detectAskedElsewhere                    // already classified: waiting in another room, or asked before and unanswered
	detectNew                               // needs a README fetch and classification
)

// detectionState does the cheap checks for a link posted in room. For
// detectAskedElsewhere it also returns the already classified entry.
func (b *Bot) detectionState(ctx context.Context, url string, room id.RoomID) (detectState, *store.Entry) {
	existing, err := b.store.GetByRepo(ctx, url)
	if err != nil {
		return detectSkip, nil
	} else if existing != nil {
		return detectKnown, nil
	}
	pending, err := b.store.PendingDetectedAdds(ctx, url)
	if err != nil {
		return detectSkip, nil
	}
	for _, p := range pending {
		if p.RoomID == room.String() {
			return detectSkip, nil
		}
	}
	if len(pending) > 0 {
		return detectAskedElsewhere, &pending[0].Entry
	}
	if skip, err := b.store.ShouldSkipDetection(ctx, url); err != nil || skip {
		return detectSkip, nil
	}
	if cached, err := b.store.GetCachedDetected(ctx, url); err != nil {
		return detectSkip, nil
	} else if cached != nil {
		return detectAskedElsewhere, cached
	}
	// A moderator's own add request is waiting.
	if pending, err := b.store.HasPendingAddForRepo(ctx, url); err != nil || pending {
		return detectSkip, nil
	}
	return detectNew, nil
}

// react adds a reaction to evt, only logging failures.
func (b *Bot) react(ctx context.Context, evt *event.Event, key string) {
	if _, err := b.client.SendReaction(ctx, evt.RoomID, evt.ID, key); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Str("reaction", key).Msg("Failed to react")
	}
}

// autoAddOutcome auto-adds entry and returns the reaction for evt: outcomeAutoAdded,
// the repo's name when it was already in the list by then, or ❌
// on a database error.
func (b *Bot) autoAddOutcome(ctx context.Context, evt *event.Event, repo forge.Repo, entry store.Entry) string {
	added, err := b.autoAdd(ctx, evt, entry)
	switch {
	case err != nil:
		zerolog.Ctx(ctx).Err(err).Msg("Failed to auto-add detected repo")
		markFailed(ctx)
		return reactFailed
	case !added:
		// A moderator added it while it was being checked.
		return repo.Host + "/" + repo.Path
	}
	return outcomeAutoAdded
}

// detectRepo fetches and classifies a new repo, then adds it (when canAuto allows
// and it's fully identified), suggests it, or asks for help.
func (b *Bot) detectRepo(ctx context.Context, evt *event.Event, repo forge.Repo, canAuto func(context.Context) bool) {
	log := zerolog.Ctx(ctx)
	url := repo.URL()

	log.Info().Msg("Checking detected repository link")
	readme, err := b.forge.FetchReadme(ctx, repo)
	if err != nil {
		log.Warn().Err(err).Msg("Detected link but couldn't fetch README")
		markFailed(ctx)
		markOutcome(ctx, outcomeMissingData)
		_ = b.store.MarkSeen(ctx, url, store.SeenFailed)
		return
	}
	info, err := b.llm.ExtractFromReadme(ctx, url, readme)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to classify detected repo")
		markFailed(ctx)
		markOutcome(ctx, outcomeAIFail)
		_ = b.store.MarkSeen(ctx, url, store.SeenFailed)
		return
	}
	if !info.InScope {
		log.Info().Interface("info", info).Msg("Detected repo is not a game port or toolkit")
		markOutcome(ctx, outcomeIgnored)
		_ = b.store.MarkSeen(ctx, url, store.SeenNotGame)
		return
	}
	entry := store.Entry{
		RepoURL: url, Game: info.Game, OriginalSystem: info.OriginalSystem,
		Kind: info.Kind, OSes: info.OSes, Detected: true,
	}
	if missing := missingInfo(entry); len(missing) > 0 {
		// A game port or toolkit we couldn't fully describe: ask for help, and
		// retry later rather than writing it off.
		log.Warn().Interface("info", info).Strs("missing", missing).Msg("Detected game repo with incomplete info")
		markFailed(ctx)
		markOutcome(ctx, outcomeMissingInfo)
		_ = b.store.MarkSeen(ctx, url, store.SeenFailed)
		b.requestHelp(ctx, evt, url, missing)
		return
	}
	if canAuto(ctx) {
		markOutcome(ctx, b.autoAddOutcome(ctx, evt, repo, entry))
		return
	}
	log.Info().Interface("entry", entry).Msg("Suggesting detected repo")
	markOutcome(ctx, outcomeCaptured)
	b.suggest(ctx, evt, entry)
}

// suggest posts a link-detection add prompt in evt's room.
func (b *Bot) suggest(ctx context.Context, evt *event.Event, entry store.Entry) {
	pings, mentions := b.suggestionPings(ctx, evt.RoomID)
	gameURL, systemURL := b.igdbLinks(ctx, entry)
	what := suggestionWhat(entry, gameURL, systemURL)

	msg := fmt.Sprintf("Detected a %s not in the DB: %s , %s. Would you like to add it to the DB?\n\n"+
		"A moderator can react with **%s** to add it or **%s** to dismiss it. If nobody answers within %s, I'll drop it and ask again the next time it's posted.",
		entry.Kind, entry.RepoURL, what, reactOK, reactCancel, formatHours(store.DetectedPendingTTL))
	if pings != "" {
		msg += "\n\nPinging " + pings
	}
	b.prompt(ctx, evt, "add", entry, msg, mentions)
}

// suggestionWhat describes a suggested entry, e.g. "**Game** from System, for
// Windows", with the game and system linked to IGDB when the URLs aren't empty.
func suggestionWhat(e store.Entry, gameURL, systemURL string) string {
	system := mdLink(e.OriginalSystem, systemURL)
	if e.Kind == store.KindToolkit {
		return fmt.Sprintf("for **%s** games, running on %s", system, oses.JoinDisplay(e.OSes))
	}
	return fmt.Sprintf("**%s** from %s, for %s", mdLink(e.Game, gameURL), system, oses.JoinDisplay(e.OSes))
}

// suggestionPings returns who a suggestion in room pings, as markdown for its last
// line, and the matching m.mentions, per detect.mention. Moderators are looked up
// in room itself each time, so every room pings its own, current moderators, and
// shown by their display name in that room.
func (b *Bot) suggestionPings(ctx context.Context, room id.RoomID) (string, *event.Mentions) {
	switch b.cfg.Detect.Mention {
	case config.MentionRoom:
		return "@room", &event.Mentions{Room: true}
	case config.MentionModerators:
		mods, err := b.roomModerators(ctx, room)
		if err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to list moderators to ping")
			return "", nil
		}
		if len(mods) == 0 {
			return "", nil
		}
		pills := make([]string, len(mods))
		for i, m := range mods {
			pills[i] = namedMention(m, b.userDisplayName(ctx, room, m))
		}
		return strings.Join(pills, " "), &event.Mentions{UserIDs: mods}
	}
	return "", nil
}

// maxPillName caps a display name shown in a pill (in runes).
const maxPillName = 64

// namedMention is a markdown user pill showing name, e.g.
// [Some Mod](https://matrix.to/#/%40mod%3Aexample.org).
func namedMention(userID id.UserID, name string) string {
	name = strings.Join(strings.Fields(name), " ") // no newlines or runs of spaces
	if r := []rune(name); len(r) > maxPillName {
		name = string(r[:maxPillName]) + "…"
	}
	if name == "" {
		name = userID.String()
	}
	return fmt.Sprintf("[%s](%s)", escapeLinkText(name), matrixToURL(userID))
}

func formatHours(d time.Duration) string {
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

// lockRepo serializes link detection per repo, so the same link posted in several
// rooms at once is classified once and then asked about in each room.
func (b *Bot) lockRepo(url string) func() {
	m, _ := b.detecting.LoadOrStore(url, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
