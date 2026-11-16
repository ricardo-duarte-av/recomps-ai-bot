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
		for _, repo := range repos {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			log := b.log.With().Str("component", "detect").Stringer("room", evt.RoomID).
				Stringer("event", evt.ID).Str("repo", repo.URL()).Logger()
			b.detectRepo(log.WithContext(ctx), evt, repo)
			cancel()
		}
	}()
}

func (b *Bot) detectRepo(ctx context.Context, evt *event.Event, repo forge.Repo) {
	log := zerolog.Ctx(ctx)
	url := repo.URL()

	// Two people posting the same link at once shouldn't produce two prompts.
	if _, busy := b.detecting.LoadOrStore(url, true); busy {
		return
	}
	defer b.detecting.Delete(url)

	if existing, err := b.store.GetByRepo(ctx, url); err != nil || existing != nil {
		return
	}
	if skip, err := b.store.ShouldSkipDetection(ctx, url); err != nil || skip {
		return
	}
	if pending, err := b.store.HasPendingAddForRepo(ctx, url); err != nil || pending {
		return
	}

	readme, err := b.forge.FetchReadme(ctx, repo)
	if err != nil {
		log.Debug().Err(err).Msg("Detected link but couldn't fetch README")
		_ = b.store.MarkSeen(ctx, url, store.SeenFailed)
		return
	}
	info, err := b.llm.ExtractFromReadme(ctx, url, readme)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to classify detected repo")
		_ = b.store.MarkSeen(ctx, url, store.SeenFailed)
		return
	}
	if !info.IsGamePort {
		log.Debug().Interface("info", info).Msg("Detected repo is not a game recomp/decomp")
		_ = b.store.MarkSeen(ctx, url, store.SeenNotGame)
		return
	}
	if info.Game == "" || info.OriginalSystem == "" || info.Kind == "" || len(info.OSes) == 0 {
		// A game port we couldn't fully describe: retry later rather than writing it off.
		log.Debug().Interface("info", info).Msg("Detected game repo with incomplete info")
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
