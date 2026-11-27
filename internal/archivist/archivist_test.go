package archivist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestNotifierCallsHook(t *testing.T) {
	got := make(chan *http.Request, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := New(srv.URL+"/hooks/sources/recomps", "secret", zerolog.Nop())
	go n.Run(ctx)
	n.Kick()

	select {
	case r := <-got:
		if r.Method != http.MethodPost || r.URL.Path != "/hooks/sources/recomps" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if h := r.Header.Get("Authorization"); h != "Bearer secret" {
			t.Errorf("Authorization = %q", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook not called")
	}
}

func TestNotifierMergesKicks(t *testing.T) {
	n := New("http://unused", "t", zerolog.Nop())
	for range 5 {
		n.Kick() // Run isn't running: only one kick may queue, and none may block
	}
	if len(n.kick) != 1 {
		t.Errorf("queued kicks = %d, want 1", len(n.kick))
	}
}

func TestNilNotifier(t *testing.T) {
	var n *Notifier
	n.Kick() // must not panic
}

func TestCallReportsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if err := New(srv.URL, "wrong", zerolog.Nop()).call(context.Background()); err == nil {
		t.Error("want an error for 401")
	}
}
