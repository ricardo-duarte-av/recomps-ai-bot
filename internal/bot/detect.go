package bot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/forge"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

// detectLinks looks for git repository links in a regular room message and offers
// to add any recomp/decomp/reimpl that isn't in the list yet.
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
		for _, repo := range repos {
			url := repo.URL()
			unlock := b.lockRepo(url)
			rctx := zerolog.Ctx(ctx).With().Str("repo", url).Logger().WithContext(ctx)
			state, entry := b.detectionState(rctx, url, evt.RoomID)
			switch state {
			case detectKnown:
				if _, err := b.client.SendReaction(rctx, evt.RoomID, evt.ID, repo.Host+"/"+repo.Path); err != nil {
					zerolog.Ctx(rctx).Warn().Err(err).Msg("Failed to add already-known reaction")
				}
			case detectAskedElsewhere:
				// Already classified: ask here, no README or AI needed.
				zerolog.Ctx(rctx).Info().Msg("Suggesting already classified repo")
				b.suggest(rctx, evt, *entry)
			case detectNew:
				if prog == nil {
					ctx, prog = b.startWork(ctx, evt)
					rctx = zerolog.Ctx(ctx).With().Str("repo", url).Logger().WithContext(ctx)
				}
				b.detectRepo(rctx, evt, repo)
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

func (b *Bot) detectRepo(ctx context.Context, evt *event.Event, repo forge.Repo) {
	log := zerolog.Ctx(ctx)
	url := repo.URL()

	log.Info().Msg("Checking detected repository link")
	readme, err := b.forge.FetchReadme(ctx, repo)
	if err != nil {
		log.Warn().Err(err).Msg("Detected link but couldn't fetch README")
		markFailed(ctx)
		_ = b.store.MarkSeen(ctx, url, store.SeenFailed)
		return
	}
	info, err := b.llm.ExtractFromReadme(ctx, url, readme)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to classify detected repo")
		markFailed(ctx)
		_ = b.store.MarkSeen(ctx, url, store.SeenFailed)
		return
	}
	if !info.IsGamePort {
		log.Info().Interface("info", info).Msg("Detected repo is not a game recomp/decomp/reimpl")
		_ = b.store.MarkSeen(ctx, url, store.SeenNotGame)
		return
	}
	if info.Game == "" || info.OriginalSystem == "" || info.Kind == "" || len(info.OSes) == 0 {
		// A game port we couldn't fully describe: retry later rather than writing it off.
		log.Warn().Interface("info", info).Msg("Detected game repo with incomplete info")
		_ = b.store.MarkSeen(ctx, url, store.SeenFailed)
		return
	}

	entry := store.Entry{
		RepoURL: url, Game: info.Game, OriginalSystem: info.OriginalSystem,
		Kind: info.Kind, OSes: info.OSes, Detected: true,
	}
	log.Info().Interface("entry", entry).Msg("Suggesting detected repo")
	b.suggest(ctx, evt, entry)
}

// suggest posts a link-detection add prompt in evt's room.
func (b *Bot) suggest(ctx context.Context, evt *event.Event, entry store.Entry) {
	prefix := ""
	if b.cfg.Detect.ShouldMention() {
		prefix = "@room "
	}
	msg := fmt.Sprintf("%sDetected a %s not in the DB: %s , **%s** from %s, for %s. Would you like to add it to the DB?\n\n"+
		"A moderator can react with **%s** to add it or **%s** to dismiss it. If nobody answers within %s, I'll drop it and ask again the next time it's posted.",
		prefix, entry.Kind, entry.RepoURL, entry.Game, entry.OriginalSystem, oses.JoinDisplay(entry.OSes), reactOK, reactCancel,
		formatHours(store.DetectedPendingTTL))
	b.prompt(ctx, evt, "add", entry, msg, b.cfg.Detect.ShouldMention())
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
