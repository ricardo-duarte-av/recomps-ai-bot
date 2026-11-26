// Package store persists the recomp/decomp/reimpl/toolkit list and pending confirmations in SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/systems"
	_ "go.mau.fi/util/dbutil/litestream" // registers the sqlite3-fk-wal driver
)

// Kinds of entries in the list.
const (
	KindRecomp = "recomp"
	KindDecomp = "decomp"
	KindReimpl = "reimpl"
	// KindToolkit is a recompiler, decompilation tooling or runtime for one original
	// system rather than a port of one game (e.g. N64Recomp). Its Game is empty.
	KindToolkit = "toolkit"
)

// Kinds lists every valid kind.
var Kinds = []string{KindRecomp, KindDecomp, KindReimpl, KindToolkit}

// ToolkitTitle stands in for the game title of toolkits in Games.
const ToolkitTitle = "(toolkit)"

// recompsTable creates the main list table under the given name (%[1]s) with the
// kind constraint (%[2]s).
const recompsTable = `
CREATE TABLE IF NOT EXISTS %[1]s (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	repo_url        TEXT NOT NULL,
	game            TEXT NOT NULL,
	original_system TEXT NOT NULL,
	kind            TEXT NOT NULL CHECK (kind IN (%[2]s)),
	os              TEXT NOT NULL,
	added_by        TEXT NOT NULL,
	added_at        INTEGER NOT NULL,
	UNIQUE (repo_url, os)
);
`

// kindList renders Kinds for the CHECK constraint, e.g. 'recomp', 'decomp'.
func kindList() string {
	quoted := make([]string, len(Kinds))
	for i, k := range Kinds {
		quoted[i] = "'" + k + "'"
	}
	return strings.Join(quoted, ", ")
}

var schema = fmt.Sprintf(recompsTable, "recomps", kindList()) + `
CREATE INDEX IF NOT EXISTS recomps_game_idx ON recomps (game);

-- Optional free-form notes per repository (e.g. links to files it needs). Kept apart
-- from recomps, which has one row per target platform.
CREATE TABLE IF NOT EXISTS recomp_extras (
	repo_url   TEXT PRIMARY KEY,
	text       TEXT NOT NULL,
	updated_by TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);

-- Repos found by link detection that should not be suggested (again) for a while.
CREATE TABLE IF NOT EXISTS seen_repos (
	repo_url   TEXT PRIMARY KEY,
	status     TEXT NOT NULL,
	checked_at INTEGER NOT NULL
);

-- Classifications of detected repos whose prompts went unanswered, reused when
-- the repo is posted again so it's suggested without another README fetch/AI call.
CREATE TABLE IF NOT EXISTS detected_cache (
	repo_url   TEXT PRIMARY KEY,
	payload    TEXT NOT NULL,
	created_at INTEGER NOT NULL
);

-- IGDB pages of games and original systems, keyed by IGDBGameKey/IGDBSystemKey.
-- An empty url records that IGDB had no match at checked_at. igdb_id is the
-- IGDB game ID of game links (0 for systems, or until it's known).
CREATE TABLE IF NOT EXISTS igdb_links (
	key        TEXT PRIMARY KEY,
	url        TEXT NOT NULL,
	checked_at INTEGER NOT NULL,
	igdb_id    INTEGER NOT NULL DEFAULT 0
);

-- Cover pictures of IGDB games, by IGDB game ID, shown on the web page.
-- Empty data records that the game had no picture at checked_at.
CREATE TABLE IF NOT EXISTS igdb_covers (
	igdb_id    INTEGER PRIMARY KEY,
	data       BLOB NOT NULL,
	mime       TEXT NOT NULL,
	checked_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS pending_actions (
	event_id     TEXT PRIMARY KEY,
	room_id      TEXT NOT NULL,
	action       TEXT NOT NULL CHECK (action IN ('add', 'remove')),
	payload      TEXT NOT NULL,
	requested_by TEXT NOT NULL,
	created_at   INTEGER NOT NULL
);
`

// PendingTTL is how long a confirmation prompt stays valid.
const PendingTTL = 24 * time.Hour

// DetectedPendingTTL is how long link-detection add prompts for a repo wait for an
// answer, counted from the newest prompt for that repo in any room. Unanswered ones
// are then dropped, and the repo is suggested again the next time it's posted.
const DetectedPendingTTL = 6 * time.Hour

// Entry describes one repository (covering one or more OSes).
type Entry struct {
	RepoURL        string   `json:"repo_url"`
	Game           string   `json:"game"`
	OriginalSystem string   `json:"original_system"`
	Kind           string   `json:"kind"`
	OSes           []string `json:"oses"`
	// Extra is optional free-form text (e.g. links to files the recomp needs).
	Extra string `json:"extra,omitempty"`
	// Detected marks an add prompt raised by link detection rather than a moderator.
	Detected bool `json:"detected,omitempty"`
}

// Pending is an add/remove awaiting moderator confirmation.
type Pending struct {
	EventID     string
	RoomID      string
	Action      string
	Entry       Entry
	RequestedBy string
	CreatedAt   time.Time
}

type Store struct {
	DB *dbutil.Database
}

// Open opens (or creates) the SQLite database at path.
func Open(path string) (*Store, error) {
	db, err := dbutil.NewWithDialect(fmt.Sprintf("file:%s?_txlock=immediate", path), "sqlite3-fk-wal")
	if err != nil {
		return nil, err
	}
	s := &Store{DB: db}
	ctx := context.Background()
	if _, err := db.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	if err := s.migrateKindCheck(ctx); err != nil {
		return nil, fmt.Errorf("migrating recomps kind constraint: %w", err)
	}
	if err := s.addColumn(ctx, "igdb_links", "igdb_id", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return nil, fmt.Errorf("adding igdb_links.igdb_id: %w", err)
	}
	if err := s.normalizeSystems(ctx); err != nil {
		return nil, fmt.Errorf("normalizing original systems: %w", err)
	}
	return s, nil
}

// addColumn adds a column to a table created before the column existed.
func (s *Store) addColumn(ctx context.Context, table, column, def string) error {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM pragma_table_info($1) WHERE name = $2`, table, column).Scan(&n)
	if err != nil || n > 0 {
		return err
	}
	_, err = s.DB.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, def))
	return err
}

// normalizeSystems rewrites stored original systems to their canonical spelling,
// so entries saved before a spelling was known (or before normalization existed)
// join the rest of their system.
func (s *Store) normalizeSystems(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT original_system FROM recomps`)
	if err != nil {
		return err
	}
	renames := map[string]string{}
	for rows.Next() {
		var sys string
		if err := rows.Scan(&sys); err != nil {
			rows.Close()
			return err
		}
		if n := systems.Normalize(sys); n != sys {
			renames[sys] = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(renames) == 0 {
		return err
	}
	return s.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
		for from, to := range renames {
			if _, err := s.DB.Exec(ctx, `UPDATE recomps SET original_system = $1 WHERE original_system = $2`, to, from); err != nil {
				return err
			}
		}
		return nil
	})
}

// migrateKindCheck rebuilds the recomps table of databases created before one of
// the current kinds existed, since SQLite can't alter a CHECK constraint in place.
// Repos written off as "not a game" are forgotten too, since some of them may be
// toolkits that can now be listed.
func (s *Store) migrateKindCheck(ctx context.Context) error {
	var def string
	err := s.DB.QueryRow(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'recomps'`).Scan(&def)
	if err != nil {
		return err
	}
	if strings.Contains(def, "CHECK (kind IN ("+kindList()+"))") {
		return nil
	}
	return s.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
		for _, q := range []string{
			`DROP TABLE IF EXISTS recomps_new`,
			fmt.Sprintf(recompsTable, "recomps_new", kindList()),
			`INSERT INTO recomps_new SELECT id, repo_url, game, original_system, kind, os, added_by, added_at FROM recomps`,
			`DROP TABLE recomps`,
			`ALTER TABLE recomps_new RENAME TO recomps`,
			`CREATE INDEX IF NOT EXISTS recomps_game_idx ON recomps (game)`,
			`DELETE FROM seen_repos WHERE status = 'not_game'`,
		} {
			if _, err := s.DB.Exec(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Close() error { return s.DB.Close() }

// Add inserts one row per OS. Existing (repo, os) rows are updated.
func (s *Store) Add(ctx context.Context, e Entry, addedBy string) error {
	_, err := s.add(ctx, e, addedBy, false)
	return err
}

// AddIfAbsent adds e only if its repo isn't in the list at all, checked in the same
// transaction, so an automatic add never overwrites an entry a moderator added
// meanwhile. It reports whether e was added.
func (s *Store) AddIfAbsent(ctx context.Context, e Entry, addedBy string) (bool, error) {
	return s.add(ctx, e, addedBy, true)
}

func (s *Store) add(ctx context.Context, e Entry, addedBy string, ifAbsent bool) (added bool, err error) {
	now := time.Now().Unix()
	e.OriginalSystem = systems.Normalize(e.OriginalSystem)
	err = s.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
		if ifAbsent {
			var n int
			if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM recomps WHERE repo_url = $1`, e.RepoURL).Scan(&n); err != nil {
				return err
			} else if n > 0 {
				return nil
			}
		}
		added = true
		for _, os := range e.OSes {
			_, err := s.DB.Exec(ctx, `
				INSERT INTO recomps (repo_url, game, original_system, kind, os, added_by, added_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (repo_url, os) DO UPDATE SET
					game = excluded.game, original_system = excluded.original_system,
					kind = excluded.kind, added_by = excluded.added_by, added_at = excluded.added_at`,
				e.RepoURL, e.Game, e.OriginalSystem, e.Kind, os, addedBy, now)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return added && err == nil, err
}

// Remove deletes rows for repoURL, restricted to oses if non-empty.
func (s *Store) Remove(ctx context.Context, repoURL string, oses []string) (int64, error) {
	var total int64
	err := s.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
		if len(oses) == 0 {
			res, err := s.DB.Exec(ctx, `DELETE FROM recomps WHERE repo_url = $1`, repoURL)
			if err != nil {
				return err
			}
			total, _ = res.RowsAffected()
			_, err = s.DB.Exec(ctx, `DELETE FROM recomp_extras WHERE repo_url = $1`, repoURL)
			return err
		}
		for _, os := range oses {
			res, err := s.DB.Exec(ctx, `DELETE FROM recomps WHERE repo_url = $1 AND os = $2`, repoURL, os)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			total += n
		}
		// Drop the extra text once the repo has no platforms left.
		_, err := s.DB.Exec(ctx, `
			DELETE FROM recomp_extras WHERE repo_url = $1
			AND NOT EXISTS (SELECT 1 FROM recomps WHERE repo_url = $1)`, repoURL)
		return err
	})
	return total, err
}

// SetExtra stores the extra text for repoURL; an empty text clears it.
func (s *Store) SetExtra(ctx context.Context, repoURL, text, by string) error {
	if text == "" {
		_, err := s.DB.Exec(ctx, `DELETE FROM recomp_extras WHERE repo_url = $1`, repoURL)
		return err
	}
	_, err := s.DB.Exec(ctx, `
		INSERT INTO recomp_extras (repo_url, text, updated_by, updated_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (repo_url) DO UPDATE SET
			text = excluded.text, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		repoURL, text, by, time.Now().Unix())
	return err
}

// GetByRepo returns the entry stored for repoURL, or nil if none.
func (s *Store) GetByRepo(ctx context.Context, repoURL string) (*Entry, error) {
	entries, err := s.query(ctx, `WHERE repo_url = $1`, repoURL)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return &entries[0], nil
}

// GetByGames returns all entries whose game title is in games.
func (s *Store) GetByGames(ctx context.Context, games []string) ([]Entry, error) {
	if len(games) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(games))
	args := make([]any, len(games))
	for i, g := range games {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = g
	}
	return s.query(ctx, `WHERE game IN (`+strings.Join(placeholders, ",")+`)`, args...)
}

// SearchGames does a case-insensitive substring search on game titles.
func (s *Store) SearchGames(ctx context.Context, term string) ([]Entry, error) {
	return s.query(ctx, `WHERE game LIKE '%' || $1 || '%' COLLATE NOCASE`, term)
}

// All returns every entry, one per repository, ordered by game.
func (s *Store) All(ctx context.Context) ([]Entry, error) {
	return s.query(ctx, "")
}

// Games returns all distinct game titles with their original systems, as
// "Title | System" lines. Toolkits are listed once per system with ToolkitTitle
// as their title.
func (s *Store) Games(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT DISTINCT CASE WHEN kind = $1 THEN $2 ELSE game END AS title, original_system
		FROM recomps ORDER BY title`, KindToolkit, ToolkitTitle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var game, system string
		if err := rows.Scan(&game, &system); err != nil {
			return nil, err
		}
		out = append(out, game+" | "+system)
	}
	return out, rows.Err()
}

// query loads rows matching where and groups them into one Entry per repo.
func (s *Store) query(ctx context.Context, where string, args ...any) ([]Entry, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT repo_url, game, original_system, kind, os,
			COALESCE((SELECT text FROM recomp_extras x WHERE x.repo_url = recomps.repo_url), '')
		FROM recomps `+where+`
		ORDER BY game, repo_url, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	idx := map[string]int{}
	for rows.Next() {
		var e Entry
		var os string
		if err := rows.Scan(&e.RepoURL, &e.Game, &e.OriginalSystem, &e.Kind, &os, &e.Extra); err != nil {
			return nil, err
		}
		if i, ok := idx[e.RepoURL]; ok {
			out[i].OSes = append(out[i].OSes, os)
			continue
		}
		e.OSes = []string{os}
		idx[e.RepoURL] = len(out)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SavePending stores a confirmation prompt.
func (s *Store) SavePending(ctx context.Context, p Pending) error {
	payload, err := json.Marshal(p.Entry)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `
		INSERT INTO pending_actions (event_id, room_id, action, payload, requested_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		p.EventID, p.RoomID, p.Action, string(payload), p.RequestedBy, time.Now().Unix())
	return err
}

// TakePending atomically fetches and deletes the pending action for eventID.
// It returns nil if there is none or it has expired.
func (s *Store) TakePending(ctx context.Context, eventID string) (*Pending, error) {
	var p Pending
	var payload string
	var created int64
	err := s.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
		err := s.DB.QueryRow(ctx, `
			SELECT event_id, room_id, action, payload, requested_by, created_at
			FROM pending_actions WHERE event_id = $1`, eventID).
			Scan(&p.EventID, &p.RoomID, &p.Action, &payload, &p.RequestedBy, &created)
		if err != nil {
			return err
		}
		_, err = s.DB.Exec(ctx, `DELETE FROM pending_actions WHERE event_id = $1`, eventID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	p.CreatedAt = time.Unix(created, 0)
	if time.Since(p.CreatedAt) > PendingTTL {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(payload), &p.Entry); err != nil {
		return nil, err
	}
	return &p, nil
}

// HasPending reports whether eventID is an active confirmation prompt.
func (s *Store) HasPending(ctx context.Context, eventID string) (bool, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM pending_actions WHERE event_id = $1 AND created_at > $2`,
		eventID, time.Now().Add(-PendingTTL).Unix()).Scan(&n)
	return n > 0, err
}

// PurgeExpired removes stale confirmation prompts.
func (s *Store) PurgeExpired(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM pending_actions WHERE created_at <= $1`, time.Now().Add(-PendingTTL).Unix())
	return err
}

// Statuses for seen_repos.
const (
	SeenDismissed = "dismissed" // a moderator cancelled the suggestion; never suggest again
	SeenNotGame   = "not_game"  // not a game port or toolkit; re-check after SeenNotGameTTL
	SeenFailed    = "failed"    // README/LLM failed; re-check after SeenFailedTTL
)

const (
	SeenNotGameTTL = 30 * 24 * time.Hour
	SeenFailedTTL  = 24 * time.Hour
)

// MarkSeen records the outcome of checking a detected repo.
func (s *Store) MarkSeen(ctx context.Context, repoURL, status string) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO seen_repos (repo_url, status, checked_at) VALUES ($1, $2, $3)
		ON CONFLICT (repo_url) DO UPDATE SET status = excluded.status, checked_at = excluded.checked_at`,
		repoURL, status, time.Now().Unix())
	return err
}

// ShouldSkipDetection reports whether a detected repo was recently checked or dismissed.
func (s *Store) ShouldSkipDetection(ctx context.Context, repoURL string) (bool, error) {
	var status string
	var checked int64
	err := s.DB.QueryRow(ctx, `SELECT status, checked_at FROM seen_repos WHERE repo_url = $1`, repoURL).Scan(&status, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	age := time.Since(time.Unix(checked, 0))
	switch status {
	case SeenDismissed:
		return true, nil
	case SeenNotGame:
		return age < SeenNotGameTTL, nil
	default:
		return age < SeenFailedTTL, nil
	}
}

// HasPendingAddForRepo reports whether an add prompt for repoURL is already awaiting confirmation.
func (s *Store) HasPendingAddForRepo(ctx context.Context, repoURL string) (bool, error) {
	var n int
	err := s.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM pending_actions
		WHERE action = 'add' AND json_extract(payload, '$.repo_url') = $1 AND created_at > $2`,
		repoURL, time.Now().Add(-PendingTTL).Unix()).Scan(&n)
	return n > 0, err
}

// detectedAddWhere selects link-detection add prompts.
const detectedAddWhere = `action = 'add' AND json_extract(payload, '$.detected') = 1`

func (s *Store) queryPending(ctx context.Context, where string, args ...any) ([]Pending, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT event_id, room_id, action, payload, requested_by, created_at
		FROM pending_actions WHERE `+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var p Pending
		var payload string
		var created int64
		if err := rows.Scan(&p.EventID, &p.RoomID, &p.Action, &payload, &p.RequestedBy, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &p.Entry); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(created, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

// PendingDetectedAdds returns the link-detection add prompts for repoURL in all rooms.
func (s *Store) PendingDetectedAdds(ctx context.Context, repoURL string) ([]Pending, error) {
	return s.queryPending(ctx, detectedAddWhere+` AND json_extract(payload, '$.repo_url') = $1`, repoURL)
}

// TakePendingDetectedAdds atomically fetches and deletes the link-detection add
// prompts for repoURL in all rooms, so an answer in one room closes all of them.
func (s *Store) TakePendingDetectedAdds(ctx context.Context, repoURL string) ([]Pending, error) {
	var out []Pending
	err := s.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
		var err error
		out, err = s.PendingDetectedAdds(ctx, repoURL)
		if err != nil {
			return err
		}
		_, err = s.DB.Exec(ctx, `DELETE FROM pending_actions WHERE `+detectedAddWhere+
			` AND json_extract(payload, '$.repo_url') = $1`, repoURL)
		return err
	})
	return out, err
}

// TakeExpiredDetectedAdds atomically fetches and deletes the link-detection add
// prompts of every repo whose newest prompt is older than DetectedPendingTTL.
func (s *Store) TakeExpiredDetectedAdds(ctx context.Context) ([]Pending, error) {
	where := detectedAddWhere + ` AND json_extract(payload, '$.repo_url') IN (
		SELECT json_extract(payload, '$.repo_url') FROM pending_actions WHERE ` + detectedAddWhere + `
		GROUP BY 1 HAVING MAX(created_at) <= $1)`
	cutoff := time.Now().Add(-DetectedPendingTTL).Unix()
	var out []Pending
	err := s.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
		var err error
		out, err = s.queryPending(ctx, where, cutoff)
		if err != nil {
			return err
		}
		_, err = s.DB.Exec(ctx, `DELETE FROM pending_actions WHERE `+where, cutoff)
		return err
	})
	return out, err
}

// CacheDetected remembers the classification of a detected repo.
func (s *Store) CacheDetected(ctx context.Context, e Entry) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `
		INSERT INTO detected_cache (repo_url, payload, created_at) VALUES ($1, $2, $3)
		ON CONFLICT (repo_url) DO UPDATE SET payload = excluded.payload, created_at = excluded.created_at`,
		e.RepoURL, string(payload), time.Now().Unix())
	return err
}

// GetCachedDetected returns the remembered classification of repoURL, or nil.
func (s *Store) GetCachedDetected(ctx context.Context, repoURL string) (*Entry, error) {
	var payload string
	err := s.DB.QueryRow(ctx, `SELECT payload FROM detected_cache WHERE repo_url = $1`, repoURL).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// IGDBGameKey is the igdb_links key of a game as released on an original system.
func IGDBGameKey(game, system string) string { return "game\x1f" + game + "\x1f" + system }

// IGDBSystemKey is the igdb_links key of an original system.
func IGDBSystemKey(system string) string { return "system\x1f" + system }

// IGDBLink is a cached IGDB page.
type IGDBLink struct {
	URL string // "" when IGDB had no match
	// ID is the IGDB game ID of a game page; 0 for systems or when not known yet.
	ID      int64
	Checked time.Time
}

// IGDBLink returns the cached IGDB link for key. ok is false if it was never looked up.
func (s *Store) IGDBLink(ctx context.Context, key string) (link IGDBLink, ok bool, err error) {
	var at int64
	err = s.DB.QueryRow(ctx, `SELECT url, igdb_id, checked_at FROM igdb_links WHERE key = $1`, key).Scan(&link.URL, &link.ID, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return IGDBLink{}, false, nil
	} else if err != nil {
		return IGDBLink{}, false, err
	}
	link.Checked = time.Unix(at, 0)
	return link, true, nil
}

// SetIGDBLink caches the IGDB link for key ("" for no match) and its IGDB game ID
// (0 when not a game or unknown), checked now.
func (s *Store) SetIGDBLink(ctx context.Context, key, link string, igdbID int64) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO igdb_links (key, url, igdb_id, checked_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET url = excluded.url, igdb_id = excluded.igdb_id, checked_at = excluded.checked_at`,
		key, link, igdbID, time.Now().Unix())
	return err
}

// SetIGDBID records the IGDB game ID of every cached link to the game page link.
func (s *Store) SetIGDBID(ctx context.Context, link string, igdbID int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE igdb_links SET igdb_id = $1 WHERE url = $2`, igdbID, link)
	return err
}

// IGDBGameID returns the IGDB game ID of a cached game page link, or 0.
func (s *Store) IGDBGameID(ctx context.Context, link string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `SELECT igdb_id FROM igdb_links WHERE url = $1 AND igdb_id != 0 LIMIT 1`, link).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// IGDBLinks returns every cached IGDB link that matched, by key.
func (s *Store) IGDBLinks(ctx context.Context) (map[string]IGDBLink, error) {
	rows, err := s.DB.Query(ctx, `SELECT key, url, igdb_id, checked_at FROM igdb_links WHERE url != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]IGDBLink{}
	for rows.Next() {
		var k string
		var l IGDBLink
		var at int64
		if err := rows.Scan(&k, &l.URL, &l.ID, &at); err != nil {
			return nil, err
		}
		l.Checked = time.Unix(at, 0)
		out[k] = l
	}
	return out, rows.Err()
}

// IGDBCover is a cached cover picture of an IGDB game.
type IGDBCover struct {
	Data    []byte // empty when the game had no picture
	Mime    string
	Checked time.Time
}

// IGDBCover returns the cached cover of IGDB game igdbID. ok is false if it was never fetched.
func (s *Store) IGDBCover(ctx context.Context, igdbID int64) (c IGDBCover, ok bool, err error) {
	var at int64
	err = s.DB.QueryRow(ctx, `SELECT data, mime, checked_at FROM igdb_covers WHERE igdb_id = $1`, igdbID).Scan(&c.Data, &c.Mime, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return IGDBCover{}, false, nil
	} else if err != nil {
		return IGDBCover{}, false, err
	}
	c.Checked = time.Unix(at, 0)
	return c, true, nil
}

// SetIGDBCover caches the cover of IGDB game igdbID (empty data for none), checked now.
func (s *Store) SetIGDBCover(ctx context.Context, igdbID int64, data []byte, mime string) error {
	if data == nil {
		data = []byte{}
	}
	_, err := s.DB.Exec(ctx, `
		INSERT INTO igdb_covers (igdb_id, data, mime, checked_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (igdb_id) DO UPDATE SET data = excluded.data, mime = excluded.mime, checked_at = excluded.checked_at`,
		igdbID, data, mime, time.Now().Unix())
	return err
}

// IGDBCoverIDs returns the IGDB game IDs that have a cached cover.
func (s *Store) IGDBCoverIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.DB.Query(ctx, `SELECT igdb_id FROM igdb_covers WHERE length(data) > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
