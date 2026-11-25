package bot

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	reactWorking = "⏳"
	reactDone    = "🆗"
	reactFailed  = "❌"
)

// Text reactions naming the outcome of checking a posted repository link.
const (
	outcomeCaptured    = "Captured"           // suggested for the list
	outcomeIgnored     = "Ignored"            // not a game port or toolkit
	outcomeMissingData = "Missing data"       // README and description couldn't be fetched
	outcomeAIFail      = "AI fail"            // the AI request failed
	outcomeMissingInfo = "Missing info, help" // game, system, kind or platforms unknown
)

type workKey struct{}

// work tracks the outcome of processing one message.
type work struct {
	failed atomic.Bool

	mu       sync.Mutex
	outcomes []string
}

// markOutcome sets a text reaction that replaces 🆗/❌ when the work attached to
// ctx finishes. Several distinct outcomes (one message with several links) each
// get their own reaction.
func markOutcome(ctx context.Context, outcome string) {
	if w, ok := ctx.Value(workKey{}).(*work); ok {
		w.mu.Lock()
		if !slices.Contains(w.outcomes, outcome) {
			w.outcomes = append(w.outcomes, outcome)
		}
		w.mu.Unlock()
	}
}

// markFailed records that the work attached to ctx failed (AI, forge or database errors,
// or not enough information to complete the request).
func markFailed(ctx context.Context) {
	if w, ok := ctx.Value(workKey{}).(*work); ok {
		w.failed.Store(true)
	}
}

// progress shows that the bot is working on a message: it reacts with ⏳ now, and
// finish() swaps that for 🆗 (or ❌ if markFailed was called on the returned context).
type progress struct {
	b         *Bot
	evt       *event.Event
	hourglass id.EventID
	w         *work
}

func (b *Bot) startWork(ctx context.Context, evt *event.Event) (context.Context, *progress) {
	p := &progress{b: b, evt: evt, w: &work{}}
	resp, err := b.client.SendReaction(ctx, evt.RoomID, evt.ID, reactWorking)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to add working reaction")
	} else {
		p.hourglass = resp.EventID
	}
	return context.WithValue(ctx, workKey{}, p.w), p
}

// finish removes ⏳ and reacts with the outcome. It uses its own context so it still
// runs when the work timed out.
func (p *progress) finish(ctx context.Context) {
	log := zerolog.Ctx(ctx)
	ctx, cancel := context.WithTimeout(log.WithContext(context.Background()), 30*time.Second)
	defer cancel()
	if p.hourglass != "" {
		if _, err := p.b.client.RedactEvent(ctx, p.evt.RoomID, p.hourglass); err != nil {
			log.Warn().Err(err).Msg("Failed to remove working reaction")
		}
	}
	p.w.mu.Lock()
	results := slices.Clone(p.w.outcomes)
	p.w.mu.Unlock()
	if len(results) == 0 {
		results = []string{reactDone}
		if p.w.failed.Load() {
			results[0] = reactFailed
		}
	}
	for _, result := range results {
		if _, err := p.b.client.SendReaction(ctx, p.evt.RoomID, p.evt.ID, result); err != nil {
			log.Warn().Err(err).Str("reaction", result).Msg("Failed to add result reaction")
		}
	}
}
