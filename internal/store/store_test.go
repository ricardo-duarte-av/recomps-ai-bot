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
