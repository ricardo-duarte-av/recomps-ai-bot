package igdb

import (
	"context"
	"strconv"
	"sync/atomic"
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
	stats  stats
}

// stats counts what the linker fetched from IGDB, for the sync summary.
type stats struct {
	linked, unmatched, ids, covers, noPicture, coverFailed atomic.Int32
}

func (s *stats) reset() {
	for _, c := range []*atomic.Int32{&s.linked, &s.unmatched, &s.ids, &s.covers, &s.noPicture, &s.coverFailed} {
		c.Store(0)
	}
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
	sys, err := l.cached(ctx, store.IGDBSystemKey(system), "", system, func() (string, int64, error) {
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
	link, err := l.cached(ctx, store.IGDBGameKey(game, system), game, system, func() (string, int64, error) {
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
	if err := l.store.SetIGDBID(ctx, link.URL, link.ID); err != nil {
		return link, err
	}
	l.stats.ids.Add(1)
	l.log.Info().Str("game", game).Str("system", system).Int64("igdb_id", link.ID).Msg("Found IGDB game ID")
	return link, nil
}

// cached returns the stored link for k, resolving and storing it when missing
// or when an earlier miss is old enough to retry. Errors aren't cached. game
// (empty for a system) and system are for the log.
func (l *Linker) cached(ctx context.Context, k, game, system string, resolve func() (string, int64, error)) (store.IGDBLink, error) {
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
	if err := l.store.SetIGDBLink(ctx, k, u, id); err != nil {
		return store.IGDBLink{}, err
	}
	what := "game"
	if game == "" {
		what = "system"
	}
	if u != "" {
		l.stats.linked.Add(1)
		ev := l.log.Info().Str("system", system).Str("url", u)
		if game != "" {
			ev = ev.Str("game", game).Int64("igdb_id", id)
		}
		ev.Msg("Linked " + what + " to IGDB")
	} else {
		l.stats.unmatched.Add(1)
		ev := l.log.Info().Str("system", system).Str("retry_after", missRetry.String())
		if game != "" {
			ev = ev.Str("game", game)
		}
		ev.Msg("No IGDB match for " + what)
	}
	return store.IGDBLink{URL: u, ID: id, Checked: time.Now()}, nil
}

// cover returns the cached picture of IGDB game id, downloading and storing it
// when missing or when an earlier miss is old enough to retry. It returns nil
// when the game has no picture.
// game is for the log.
func (l *Linker) cover(ctx context.Context, id int64, game string) (*Image, error) {
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
			l.stats.noPicture.Add(1)
			l.log.Info().Str("game", game).Int64("igdb_id", id).Str("retry_after", missRetry.String()).Msg("IGDB has no picture for game")
			return nil, l.store.SetIGDBCover(ctx, id, nil, "")
		}
		if err := l.store.SetIGDBCover(ctx, id, img.Data, img.MimeType); err != nil {
			return nil, err
		}
		l.stats.covers.Add(1)
		l.log.Info().Str("game", game).Int64("igdb_id", id).Int("bytes", len(img.Data)).Msg("Cached IGDB cover")
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
	l.log.Info().Str("interval", syncInterval.String()).Msg("IGDB linking started")
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
	start := time.Now()
	l.stats.reset()
	done := 0
	defer func() { l.logSummary(len(entries), done, time.Since(start)) }()
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
		done++
	}
}

// logSummary reports a sync round: at info level when it fetched anything or
// stopped early, else at debug level.
func (l *Linker) logSummary(total, done int, took time.Duration) {
	st := &l.stats
	fetched := st.linked.Load() + st.unmatched.Load() + st.ids.Load() + st.covers.Load() + st.noPicture.Load() + st.coverFailed.Load()
	ev := l.log.Debug()
	msg := "IGDB sync finished; nothing new"
	switch {
	case done < total:
		ev, msg = l.log.Info(), "IGDB sync stopped early"
	case fetched > 0:
		ev, msg = l.log.Info(), "IGDB sync finished"
	}
	ev.Int("entries", total).Int("checked", done).
		Int32("linked", st.linked.Load()).Int32("unmatched", st.unmatched.Load()).Int32("ids_found", st.ids.Load()).
		Int32("covers_cached", st.covers.Load()).Int32("no_picture", st.noPicture.Load()).Int32("cover_failures", st.coverFailed.Load()).
		Str("took", took.Round(time.Second).String()).Msg(msg)
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
	if _, err := l.cover(ctx, g.ID, game); err != nil && ctx.Err() == nil {
		l.stats.coverFailed.Add(1)
		l.log.Warn().Err(err).Str("game", game).Int64("igdb_id", g.ID).Msg("Failed to cache IGDB cover; retrying later")
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
		return l.cover(ctx, id, "")
	}
	return l.client.Image(ctx, pageURL)
}
