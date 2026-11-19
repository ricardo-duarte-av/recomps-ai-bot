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
