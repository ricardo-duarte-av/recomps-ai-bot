package bot

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/forge"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

// detectLinks looks for git repository links in a regular room message and offers
// to add any recomp/decomp that isn't in the list yet.
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
			if _, busy := b.detecting.LoadOrStore(url, true); busy {
				continue // someone else posted it at the same moment
			}
			needs, known := b.needsDetection(ctx, url)
			if known {
				if _, err := b.client.SendReaction(ctx, evt.RoomID, evt.ID, repo.Host+"/"+repo.Path); err != nil {
					zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to add already-known reaction")
				}
			}
			if needs {
				if prog == nil {
					ctx, prog = b.startWork(ctx, evt)
				}
				b.detectRepo(zerolog.Ctx(ctx).With().Str("repo", url).Logger().WithContext(ctx), evt, repo)
			}
			b.detecting.Delete(url)
		}
	}()
}

// needsDetection does the cheap checks: not in the list, not dismissed or recently
// checked, and no add prompt already waiting. known reports that the repo is already
// in the list.
func (b *Bot) needsDetection(ctx context.Context, url string) (needs, known bool) {
	existing, err := b.store.GetByRepo(ctx, url)
	if err != nil {
		return false, false
	} else if existing != nil {
		return false, true
	}
	if skip, err := b.store.ShouldSkipDetection(ctx, url); err != nil || skip {
		return false, false
	}
	if pending, err := b.store.HasPendingAddForRepo(ctx, url); err != nil || pending {
		return false, false
	}
	return true, false
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
		log.Info().Interface("info", info).Msg("Detected repo is not a game recomp/decomp")
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
	prefix := ""
	if b.cfg.Detect.ShouldMention() {
		prefix = "@room "
	}
	msg := fmt.Sprintf("%sDetected a %s not in the DB: %s , **%s** from %s, for %s. Would you like to add it to the DB?\n\n"+
		"A moderator can react with **%s** to add it or **%s** to dismiss it.",
		prefix, entry.Kind, entry.RepoURL, entry.Game, entry.OriginalSystem, oses.JoinDisplay(entry.OSes), reactOK, reactCancel)
	log.Info().Interface("entry", entry).Msg("Suggesting detected repo")
	b.prompt(ctx, evt, "add", entry, msg, b.cfg.Detect.ShouldMention())
}
