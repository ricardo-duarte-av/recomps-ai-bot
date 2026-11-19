package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "go.mau.fi/util/dbutil/litestream"
)

// TestMigrateKindCheck opens a database created with the old recomp/decomp-only
// constraint and checks that existing rows survive, newer kinds can be added and
// repos written off as not games are forgotten.
func TestMigrateKindCheck(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	old, err := sql.Open("sqlite3-fk-wal", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE recomps (
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
		INSERT INTO recomps (repo_url, game, original_system, kind, os, added_by, added_at)
		VALUES ('https://github.com/pret/pokeemerald', 'Pokémon Emerald', 'Game Boy Advance', 'decomp', 'linux', '@mod:x', 1);
		CREATE TABLE seen_repos (repo_url TEXT PRIMARY KEY, status TEXT NOT NULL, checked_at INTEGER NOT NULL);
		INSERT INTO seen_repos VALUES ('https://github.com/N64Recomp/N64Recomp', 'not_game', strftime('%s')),
			('https://github.com/a/dismissed', 'dismissed', strftime('%s'));
	`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	for range 2 { // the second open must be a no-op
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got, err := s.GetByRepo(ctx, "https://github.com/pret/pokeemerald"); err != nil || got == nil || got.Kind != "decomp" {
		t.Fatalf("existing row after migration = %+v, %v", got, err)
	}
	e := Entry{RepoURL: "https://github.com/mnkyarts/projectavalanche", Game: "SSX (2012)",
		OriginalSystem: "Xbox 360", Kind: "reimpl", OSes: []string{"windows", "linux"}}
	if err := s.Add(ctx, e, "@mod:x"); err != nil {
		t.Fatalf("adding reimpl after migration: %v", err)
	}
	if err := s.Add(ctx, Entry{RepoURL: "https://github.com/N64Recomp/N64Recomp", OriginalSystem: "Nintendo 64",
		Kind: KindToolkit, OSes: []string{"linux"}}, "@mod:x"); err != nil {
		t.Fatalf("adding toolkit after migration: %v", err)
	}
	if skip, _ := s.ShouldSkipDetection(ctx, "https://github.com/N64Recomp/N64Recomp"); skip {
		t.Error("not_game marks should be forgotten by the migration")
	}
	if skip, _ := s.ShouldSkipDetection(ctx, "https://github.com/a/dismissed"); !skip {
		t.Error("dismissals should survive the migration")
	}
	if err := s.Add(ctx, Entry{RepoURL: "https://x/y", Game: "G", OriginalSystem: "S", Kind: "bogus", OSes: []string{"linux"}}, "@mod:x"); err == nil {
		t.Fatal("constraint should still reject unknown kinds")
	}
}
