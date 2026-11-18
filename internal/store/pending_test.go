package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectedPendingAcrossRooms(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a := Entry{RepoURL: "https://github.com/x/a", Game: "A", OriginalSystem: "PS2", Kind: "recomp", OSes: []string{"linux"}, Detected: true}
	b := a
	b.RepoURL = "https://github.com/x/b"
	manual := a
	manual.Detected = false
	for _, p := range []Pending{
		{EventID: "$a1", RoomID: "!r1", Action: "add", Entry: a},
		{EventID: "$a2", RoomID: "!r2", Action: "add", Entry: a},
		{EventID: "$b1", RoomID: "!r1", Action: "add", Entry: b},
		{EventID: "$m1", RoomID: "!r3", Action: "add", Entry: manual},
	} {
		if err := s.SavePending(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.PendingDetectedAdds(ctx, a.RepoURL)
	if err != nil || len(got) != 2 {
		t.Fatalf("PendingDetectedAdds = %+v, %v", got, err)
	}

	// Answering one room's prompt closes the repo's prompts in every room, but
	// leaves other repos and moderators' own add requests alone.
	taken, err := s.TakePendingDetectedAdds(ctx, a.RepoURL)
	if err != nil || len(taken) != 2 {
		t.Fatalf("TakePendingDetectedAdds = %+v, %v", taken, err)
	}
	if got, _ := s.PendingDetectedAdds(ctx, a.RepoURL); len(got) != 0 {
		t.Fatalf("prompts left after take: %+v", got)
	}
	if ok, _ := s.HasPending(ctx, "$m1"); !ok {
		t.Fatal("manual add prompt should survive")
	}

	// Expiry counts from the newest prompt for the repo.
	old := time.Now().Add(-DetectedPendingTTL - time.Minute).Unix()
	if err := s.SavePending(ctx, Pending{EventID: "$b2", RoomID: "!r2", Action: "add", Entry: b}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE pending_actions SET created_at = $1 WHERE event_id IN ('$b1', '$m1')`, old); err != nil {
		t.Fatal(err)
	}
	if exp, err := s.TakeExpiredDetectedAdds(ctx); err != nil || len(exp) != 0 {
		t.Fatalf("expired while a newer prompt is live: %+v, %v", exp, err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE pending_actions SET created_at = $1 WHERE event_id = '$b2'`, old); err != nil {
		t.Fatal(err)
	}
	exp, err := s.TakeExpiredDetectedAdds(ctx)
	if err != nil || len(exp) != 2 {
		t.Fatalf("TakeExpiredDetectedAdds = %+v, %v", exp, err)
	}
	if ok, _ := s.HasPending(ctx, "$m1"); !ok {
		t.Fatal("manual add prompt shouldn't expire with detection prompts")
	}

	if c, err := s.GetCachedDetected(ctx, b.RepoURL); err != nil || c != nil {
		t.Fatalf("GetCachedDetected before caching = %+v, %v", c, err)
	}
	if err := s.CacheDetected(ctx, exp[0].Entry); err != nil {
		t.Fatal(err)
	}
	if c, err := s.GetCachedDetected(ctx, b.RepoURL); err != nil || c == nil || c.Game != "A" || !c.Detected {
		t.Fatalf("GetCachedDetected = %+v, %v", c, err)
	}
}
