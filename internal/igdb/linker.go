package igdb

import (
	"context"
	"strconv"
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
	sys, err := l.cached(ctx, store.IGDBSystemKey(system), func() (string, int64, error) {
		link, err := l.client.PlatformURL(ctx, system)
		return link, 0, err
	})
	if err != nil || game == "" {
		return "", sys.URL, err
	}
	g, err := l.game(ctx, game, system)
	return g.URL, sys.URL, err
}

// game returns the cached IGDB page and ID of game as released on system,
// resolving them when missing.
func (l *Linker) game(ctx context.Context, game, system string) (store.IGDBLink, error) {
	link, err := l.cached(ctx, store.IGDBGameKey(game, system), func() (string, int64, error) {
		g, err := l.client.FindGame(ctx, game, system)
		if g == nil || err != nil {
			return "", 0, err
		}
		return g.Link(), int64(g.ID), nil
	})
	if err != nil || link.URL == "" || link.ID != 0 {
		return link, err
	}
	// Linked before IDs were stored: look the ID up from the page.
	if link.ID, err = l.client.GameID(ctx, link.URL); err != nil || link.ID == 0 {
		return link, err
	}
	return link, l.store.SetIGDBID(ctx, link.URL, link.ID)
}

// cached returns the stored link for k, resolving and storing it when missing
// or when an earlier miss is old enough to retry. Errors aren't cached.
func (l *Linker) cached(ctx context.Context, k string, resolve func() (string, int64, error)) (store.IGDBLink, error) {
	link, ok, err := l.store.IGDBLink(ctx, k)
	if err != nil {
		return store.IGDBLink{}, err
	}
	if ok && (link.URL != "" || time.Since(link.Checked) < missRetry) {
		return link, nil
	}
	u, id, err := resolve()
	if err != nil {
		return store.IGDBLink{}, err
	}
	return store.IGDBLink{URL: u, ID: id, Checked: time.Now()}, l.store.SetIGDBLink(ctx, k, u, id)
}

// cover returns the cached picture of IGDB game id, downloading and storing it
// when missing or when an earlier miss is old enough to retry. It returns nil
// when the game has no picture.
func (l *Linker) cover(ctx context.Context, id int64) (*Image, error) {
	c, ok, err := l.store.IGDBCover(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok || (len(c.Data) == 0 && time.Since(c.Checked) >= missRetry) {
		img, err := l.client.GameImage(ctx, id)
		if err != nil {
			return nil, err
		}
		if img == nil {
			return nil, l.store.SetIGDBCover(ctx, id, nil, "")
		}
		if err := l.store.SetIGDBCover(ctx, id, img.Data, img.MimeType); err != nil {
			return nil, err
		}
		c = store.IGDBCover{Data: img.Data, Mime: img.MimeType}
	}
	if len(c.Data) == 0 {
		return nil, nil
	}
	ext := ".jpg"
	if c.Mime == "image/png" {
		ext = ".png"
	}
	return &Image{Data: c.Data, MimeType: c.Mime, FileName: "igdb-" + strconv.FormatInt(id, 10) + ext}, nil
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

// sync resolves the links, IGDB IDs and covers of every entry missing them. It stops at the first
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
		err := l.syncEntry(ctx, game, e.OriginalSystem)
		if err != nil {
			if ctx.Err() == nil {
				l.log.Warn().Err(err).Str("game", game).Str("system", e.OriginalSystem).Msg("IGDB lookup failed; retrying later")
			}
			return
		}
	}
}

func (l *Linker) syncEntry(ctx context.Context, game, system string) error {
	if _, _, err := l.links(ctx, "", system); err != nil || game == "" {
		return err
	}
	g, err := l.game(ctx, game, system)
	if err != nil || g.ID == 0 {
		return err
	}
	// A picture that fails to download doesn't hold up linking the other entries.
	if _, err := l.cover(ctx, g.ID); err != nil && ctx.Err() == nil {
		l.log.Warn().Err(err).Int64("igdb_id", g.ID).Msg("Failed to cache IGDB cover; retrying later")
	}
	return ctx.Err()
}

// Image returns the picture for an IGDB game or platform page; see Client.Image.
// Pictures of linked games come from the cover cache. It returns nil when
// there's none or IGDB isn't configured.
func (l *Linker) Image(ctx context.Context, pageURL string) (*Image, error) {
	if l == nil || pageURL == "" {
		return nil, nil
	}
	if id, err := l.store.IGDBGameID(ctx, pageURL); err != nil {
		return nil, err
	} else if id != 0 {
		return l.cover(ctx, id)
	}
	return l.client.Image(ctx, pageURL)
}
