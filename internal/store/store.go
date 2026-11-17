// Package store persists the recomp/decomp list and pending confirmations in SQLite.
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
	_ "go.mau.fi/util/dbutil/litestream" // registers the sqlite3-fk-wal driver
)

const schema = `
CREATE TABLE IF NOT EXISTS recomps (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	repo_url        TEXT NOT NULL,
	game            TEXT NOT NULL,
	original_system TEXT NOT NULL,
	kind            TEXT NOT NULL CHECK (kind IN ('recomp', 'decomp')),
	os              TEXT NOT NULL,
	added_by        TEXT NOT NULL,
	added_at        INTEGER NOT NULL,
	UNIQUE (repo_url, os)
);
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
	if _, err := db.Exec(context.Background(), schema); err != nil {
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// Add inserts one row per OS. Existing (repo, os) rows are updated.
func (s *Store) Add(ctx context.Context, e Entry, addedBy string) error {
	now := time.Now().Unix()
	return s.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
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

// Games returns all distinct game titles with their original systems.
func (s *Store) Games(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT game, original_system FROM recomps ORDER BY game`)
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
	SeenNotGame   = "not_game"  // not a game recomp/decomp; re-check after SeenNotGameTTL
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
