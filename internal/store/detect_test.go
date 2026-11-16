package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDetectionBookkeeping(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	url := "https://github.com/a/b"

	if skip, err := s.ShouldSkipDetection(ctx, url); err != nil || skip {
		t.Fatalf("fresh repo: skip=%v err=%v", skip, err)
	}
	for _, status := range []string{SeenFailed, SeenNotGame, SeenDismissed} {
		if err := s.MarkSeen(ctx, url, status); err != nil {
			t.Fatal(err)
		}
		if skip, err := s.ShouldSkipDetection(ctx, url); err != nil || !skip {
			t.Fatalf("%s: skip=%v err=%v", status, skip, err)
		}
	}

	if p, err := s.HasPendingAddForRepo(ctx, url); err != nil || p {
		t.Fatalf("pending before save: %v %v", p, err)
	}
	err = s.SavePending(ctx, Pending{EventID: "$e", RoomID: "!r", Action: "add",
		Entry: Entry{RepoURL: url, Game: "G", OriginalSystem: "S", Kind: "recomp", OSes: []string{"linux"}, Detected: true}})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := s.HasPendingAddForRepo(ctx, url); err != nil || !p {
		t.Fatalf("pending after save: %v %v", p, err)
	}
	got, _ := s.TakePending(ctx, "$e")
	if got == nil || !got.Entry.Detected {
		t.Fatalf("Detected flag not persisted: %+v", got)
	}
}
