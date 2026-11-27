// Package archivist tells a matrix-archivist instance that the list changed, by
// calling its sync hook, so it fetches /api/recomps and clones new repositories
// right away instead of waiting for its next polling interval.
package archivist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Timeout bounds one hook call. The archivist answers as soon as the sync is
// queued, and a lost call is picked up by its next polling sync, so there is no retry.
var Timeout = 5 * time.Second

// Notifier calls the sync hook in the background. A nil *Notifier does nothing.
type Notifier struct {
	url, token string
	http       *http.Client
	log        zerolog.Logger
	kick       chan struct{}
}

// New returns a Notifier for the hook at url (e.g.
// http://archivist:8080/hooks/sources/recomps), authenticated with token.
func New(url, token string, log zerolog.Logger) *Notifier {
	return &Notifier{url: url, token: token, http: &http.Client{Timeout: Timeout}, log: log, kick: make(chan struct{}, 1)}
}

// Kick asks for a hook call without blocking. Kicks that arrive while one is
// pending are merged into it.
func (n *Notifier) Kick() {
	if n == nil {
		return
	}
	select {
	case n.kick <- struct{}{}:
	default:
	}
}

// Run checks the hook once, logging whether it works, then calls it once per
// (merged) kick until ctx is cancelled. The check is a real hook call: it is the
// only way to test the token, and an extra sync costs the archivist nothing.
func (n *Notifier) Run(ctx context.Context) {
	if err := n.call(ctx); err != nil {
		n.log.Warn().Err(err).Str("url", n.url).Msg("Archivist sync hook isn't working; the archivist will only see changes at its polling interval")
	} else {
		n.log.Info().Str("url", n.url).Msg("Archivist sync hook is working")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.kick:
			if err := n.call(ctx); err != nil {
				n.log.Warn().Err(err).Msg("Archivist sync hook failed; its next polling sync will catch up")
			} else {
				n.log.Debug().Msg("Archivist sync queued")
			}
		}
	}
}

func (n *Notifier) call(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.token)
	resp, err := n.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}
