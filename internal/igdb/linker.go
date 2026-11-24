package igdb

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

const (
	// missRetry is how long a game or system IGDB didn't match stays unlinked
	// before it is looked up again.
	missRetry = 7 * 24 * time.Hour
	// syncInterval is how often the whole list is checked for unlinked entries.
	syncInterval = time.Hour
)

// Linker resolves IGDB links for list entries and caches them in the database,
// where the web page reads them. A nil *Linker is valid and links nothing.
type Linker struct {
	client *Client
	store  *store.Store
	log    zerolog.Logger
	kick   chan struct{}
}

func NewLinker(c *Client, st *store.Store, log zerolog.Logger) *Linker {
	return &Linker{client: c, store: st, log: log, kick: make(chan struct{}, 1)}
}

// Links returns the IGDB pages of game and system ("" when unknown), from the
// cache or by asking IGDB. Toolkits pass an empty game.
func (l *Linker) Links(ctx context.Context, game, system string) (gameURL, systemURL string) {
	if l == nil {
		return "", ""
	}
	gameURL, systemURL, err := l.links(ctx, game, system)
	if err != nil {
		l.log.Warn().Err(err).Str("game", game).Str("system", system).Msg("IGDB lookup failed")
	}
	return gameURL, systemURL
}

func (l *Linker) links(ctx context.Context, game, system string) (gameURL, systemURL string, err error) {
	systemURL, err = l.cached(ctx, store.IGDBSystemKey(system), func() (string, error) {
		return l.client.PlatformURL(ctx, system)
	})
	if err != nil || game == "" {
		return "", systemURL, err
	}
	gameURL, err = l.cached(ctx, store.IGDBGameKey(game, system), func() (string, error) {
		return l.client.GameURL(ctx, game, system)
	})
	return gameURL, systemURL, err
}

// cached returns the stored link for k, resolving and storing it when missing
// or when an earlier miss is old enough to retry. Errors aren't cached.
func (l *Linker) cached(ctx context.Context, k string, resolve func() (string, error)) (string, error) {
	link, checked, ok, err := l.store.IGDBLink(ctx, k)
	if err != nil {
		return "", err
	}
	if ok && (link != "" || time.Since(checked) < missRetry) {
		return link, nil
	}
	link, err = resolve()
	if err != nil {
		return "", err
	}
	return link, l.store.SetIGDBLink(ctx, k, link)
}

// Kick asks Run to look for unlinked entries now, e.g. after an entry was added.
func (l *Linker) Kick() {
	if l == nil {
		return
	}
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

// Run links every entry in the list at start, then hourly and when kicked,
// until ctx is cancelled.
func (l *Linker) Run(ctx context.Context) {
	t := time.NewTicker(syncInterval)
	defer t.Stop()
	for {
		l.sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-l.kick:
		}
	}
}

// sync resolves the links of every entry not linked yet. It stops at the first
// IGDB error and tries again on the next round.
func (l *Linker) sync(ctx context.Context) {
	entries, err := l.store.All(ctx)
	if err != nil {
		l.log.Warn().Err(err).Msg("Failed to load entries for IGDB linking")
		return
	}
	for _, e := range entries {
		game := e.Game
		if e.Kind == store.KindToolkit {
			game = ""
		}
		if _, _, err := l.links(ctx, game, e.OriginalSystem); err != nil {
			if ctx.Err() == nil {
				l.log.Warn().Err(err).Str("game", game).Str("system", e.OriginalSystem).Msg("IGDB lookup failed; retrying later")
			}
			return
		}
	}
}
