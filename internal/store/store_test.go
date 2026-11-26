package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

func TestStore(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	e := Entry{RepoURL: "https://github.com/pret/pokeemerald", Game: "Pokémon Emerald",
		OriginalSystem: "Game Boy Advance", Kind: "decomp", OSes: []string{"windows", "linux", "macos"}}
	if err := s.Add(ctx, e, "@mod:x"); err != nil {
		t.Fatal(err)
	}
	// Re-adding is idempotent.
	if err := s.Add(ctx, e, "@mod:x"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetByRepo(ctx, e.RepoURL)
	if err != nil || got == nil || !slices.Equal(got.OSes, e.OSes) {
		t.Fatalf("GetByRepo = %+v, %v", got, err)
	}
	found, err := s.SearchGames(ctx, "emerald")
	if err != nil || len(found) != 1 {
		t.Fatalf("SearchGames = %+v, %v", found, err)
	}
	games, _ := s.Games(ctx)
	if len(games) != 1 {
		t.Fatalf("Games = %v", games)
	}
	tk := Entry{RepoURL: "https://github.com/N64Recomp/N64Recomp", OriginalSystem: "Nintendo 64", Kind: KindToolkit, OSes: []string{"linux"}}
	if err := s.Add(ctx, tk, "@mod:x"); err != nil {
		t.Fatal(err)
	}
	games, _ = s.Games(ctx)
	if want := []string{"(toolkit) | Nintendo 64", "Pokémon Emerald | Game Boy Advance"}; !slices.Equal(games, want) {
		t.Fatalf("Games with a toolkit = %v, want %v", games, want)
	}
	if _, err := s.Remove(ctx, tk.RepoURL, nil); err != nil {
		t.Fatal(err)
	}

	p := Pending{EventID: "$ev", RoomID: "!r", Action: "remove", Entry: *got, RequestedBy: "@mod:x"}
	if err := s.SavePending(ctx, p); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.HasPending(ctx, "$ev"); !ok {
		t.Fatal("expected pending")
	}
	taken, err := s.TakePending(ctx, "$ev")
	if err != nil || taken == nil || taken.Entry.Game != e.Game {
		t.Fatalf("TakePending = %+v, %v", taken, err)
	}
	if again, _ := s.TakePending(ctx, "$ev"); again != nil {
		t.Fatal("pending should be consumed")
	}

	n, err := s.Remove(ctx, e.RepoURL, nil)
	if err != nil || n != 3 {
		t.Fatalf("Remove = %d, %v", n, err)
	}
}

func TestExtra(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := Entry{RepoURL: "https://github.com/papand7/nightfire-recomp", Game: "007: Nightfire",
		OriginalSystem: "PlayStation 2", Kind: "recomp", OSes: []string{"windows", "linux"}}
	if err := s.Add(ctx, e, "@mod:x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExtra(ctx, e.RepoURL, "File1: https://file1.com\nFile2: https://file2.com", "@mod:x"); err != nil {
		t.Fatal(err)
	}
	// Re-adding must keep the extra text.
	if err := s.Add(ctx, e, "@mod:x"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByRepo(ctx, e.RepoURL)
	if got == nil || got.Extra != "File1: https://file1.com\nFile2: https://file2.com" || len(got.OSes) != 2 {
		t.Fatalf("GetByRepo = %+v", got)
	}
	// Removing one platform keeps it; removing the last drops it.
	s.Remove(ctx, e.RepoURL, []string{"linux"})
	if got, _ := s.GetByRepo(ctx, e.RepoURL); got == nil || got.Extra == "" {
		t.Fatalf("extra lost after partial remove: %+v", got)
	}
	s.Remove(ctx, e.RepoURL, []string{"windows"})
	var n int
	s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM recomp_extras`).Scan(&n)
	if n != 0 {
		t.Fatal("extra should be deleted with the last platform")
	}
	// Clearing.
	s.Add(ctx, e, "@mod:x")
	s.SetExtra(ctx, e.RepoURL, "x", "@mod:x")
	s.SetExtra(ctx, e.RepoURL, "", "@mod:x")
	if got, _ := s.GetByRepo(ctx, e.RepoURL); got.Extra != "" {
		t.Fatalf("not cleared: %q", got.Extra)
	}
}

func TestOriginalSystemsAreNormalized(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ctx, Entry{RepoURL: "https://a", Game: "A", OriginalSystem: "PS5", Kind: "recomp", OSes: []string{"windows"}}, "@mod:x"); err != nil {
		t.Fatal(err)
	}
	// A row stored before normalization existed is fixed when the store opens.
	if _, err := s.DB.Exec(ctx, `INSERT INTO recomps (repo_url, game, original_system, kind, os, added_by, added_at)
		VALUES ('https://b', 'B', 'Switch2', 'recomp', 'linux', '@mod:x', 0)`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for url, want := range map[string]string{"https://a": "PlayStation 5", "https://b": "Nintendo Switch 2"} {
		if e, err := s.GetByRepo(ctx, url); err != nil || e == nil || e.OriginalSystem != want {
			t.Errorf("GetByRepo(%s) = %+v, %v; want system %q", url, e, err, want)
		}
	}
}

func TestAddIfAbsent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mod := Entry{RepoURL: "https://github.com/x/a", Game: "Real Game", OriginalSystem: "PS2", Kind: "decomp", OSes: []string{"linux"}}
	auto := Entry{RepoURL: mod.RepoURL, Game: "AI Guess", OriginalSystem: "PS2", Kind: "recomp", OSes: []string{"windows"}}
	if added, err := s.AddIfAbsent(ctx, mod, "@poster:x"); err != nil || !added {
		t.Fatalf("first AddIfAbsent = %v, %v", added, err)
	}
	// A repo that's already listed is left exactly as it is.
	if added, err := s.AddIfAbsent(ctx, auto, "@poster:x"); err != nil || added {
		t.Fatalf("second AddIfAbsent = %v, %v", added, err)
	}
	got, err := s.GetByRepo(ctx, mod.RepoURL)
	if err != nil || got == nil || got.Game != "Real Game" || got.Kind != "decomp" || !slices.Equal(got.OSes, []string{"linux"}) {
		t.Fatalf("GetByRepo = %+v, %v", got, err)
	}
}
