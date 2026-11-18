package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

func classifyReaction(key string) (confirm, cancel bool) {
	k := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(key, "️", "")))
	switch k {
	case "ok", "✅", "👍", "✔", "☑":
		return true, false
	case "cancel", "❌", "👎", "✖", "🚫":
		return false, true
	}
	return false, false
}

func (b *Bot) onReaction(ctx context.Context, evt *event.Event) {
	// Reactions are not filtered by timestamp: a moderator may confirm while the bot was offline.
	if evt.Sender == b.userID || b.ignored[evt.Sender] || !b.rooms[evt.RoomID] {
		return
	}
	rel := evt.Content.AsReaction().RelatesTo
	confirm, cancel := classifyReaction(rel.Key)
	if !confirm && !cancel {
		return
	}
	go func() {
		ctx, cancelCtx := context.WithTimeout(context.Background(), time.Minute)
		defer cancelCtx()
		log := b.log.With().Stringer("room", evt.RoomID).Stringer("sender", evt.Sender).Stringer("prompt", rel.EventID).Logger()
		ctx = log.WithContext(ctx)
		b.handleReaction(ctx, evt, rel.EventID, confirm)
	}()
}

func (b *Bot) handleReaction(ctx context.Context, evt *event.Event, promptID id.EventID, confirm bool) {
	log := zerolog.Ctx(ctx)
	ok, err := b.store.HasPending(ctx, promptID.String())
	if err != nil {
		log.Err(err).Msg("Failed to check pending action")
		return
	} else if !ok {
		return
	}
	if !b.isModerator(ctx, evt.RoomID, evt.Sender) {
		log.Debug().Msg("Ignoring confirmation reaction from non-moderator")
		return
	}
	// TakePending is atomic, so two moderators reacting at once can't both apply it.
	p, err := b.store.TakePending(ctx, promptID.String())
	if err != nil {
		log.Err(err).Msg("Failed to take pending action")
		return
	} else if p == nil || p.RoomID != evt.RoomID.String() {
		return
	}

	// Reply to the prompt message rather than the reaction.
	target := &event.Event{ID: promptID, RoomID: evt.RoomID, Sender: b.userID}
	e := p.Entry

	// The same detected repo may have been suggested in other rooms too: the
	// answer here closes those prompts as well (and so does a moderator's own add).
	var others []store.Pending
	if p.Action == "add" && (e.Detected || confirm) {
		if others, err = b.store.TakePendingDetectedAdds(ctx, e.RepoURL); err != nil {
			log.Err(err).Msg("Failed to take the repo's prompts in other rooms")
		}
	}
	tellOthers := func(msg string) {
		for _, o := range others {
			b.reply(ctx, &event.Event{ID: id.EventID(o.EventID), RoomID: id.RoomID(o.RoomID), Sender: b.userID}, msg)
		}
	}

	if !confirm {
		if p.Action == "add" && e.Detected {
			if err := b.store.MarkSeen(ctx, e.RepoURL, store.SeenDismissed); err != nil {
				log.Err(err).Msg("Failed to remember dismissed repo")
			}
			msg := fmt.Sprintf("I won't suggest %s again (a moderator can still add it explicitly).", e.RepoURL)
			b.reply(ctx, target, fmt.Sprintf("Dismissed by %s. %s", mention(evt.Sender), msg))
			tellOthers(fmt.Sprintf("Dismissed by %s in another room. %s", mention(evt.Sender), msg))
			return
		}
		b.reply(ctx, target, fmt.Sprintf("Cancelled by %s.", mention(evt.Sender)))
		return
	}

	switch p.Action {
	case "add":
		addedBy := p.RequestedBy
		if e.Detected {
			addedBy = evt.Sender.String() // the link poster didn't ask for this; the confirming mod did
		}
		if err := b.store.Add(ctx, e, addedBy); err != nil {
			log.Err(err).Msg("Failed to add entry")
			b.reply(ctx, target, "Database error, the entry was not added.")
			return
		}
		log.Info().Str("repo", e.RepoURL).Strs("oses", e.OSes).Msg("Added entry")
		added := fmt.Sprintf("Added %s (%s of **%s** from %s) for %s. Confirmed by %s",
			e.RepoURL, e.Kind, e.Game, e.OriginalSystem, oses.JoinDisplay(e.OSes), mention(evt.Sender))
		b.reply(ctx, target, added+".")
		tellOthers(added + " in another room.")
	case "remove":
		n, err := b.store.Remove(ctx, e.RepoURL, e.OSes)
		if err != nil {
			log.Err(err).Msg("Failed to remove entry")
			b.reply(ctx, target, "Database error, nothing was removed.")
			return
		}
		log.Info().Str("repo", e.RepoURL).Strs("oses", e.OSes).Int64("rows", n).Msg("Removed entry")
		b.reply(ctx, target, fmt.Sprintf("Removed %s from %s for **%s**. Confirmed by %s.",
			e.RepoURL, oses.JoinDisplay(e.OSes), e.Game, mention(evt.Sender)))
	}
}

// expireLoop closes link-detection prompts nobody answered in time, in every room
// they were asked in. The repo isn't remembered, so the next time it's posted it's
// suggested again. Prompts that expired while the bot was down are handled on the
// first tick, once the initial sync has had time to run.
func (b *Bot) expireLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.expireDetected(ctx)
		}
	}
}

func (b *Bot) expireDetected(ctx context.Context) {
	expired, err := b.store.TakeExpiredDetectedAdds(ctx)
	if err != nil {
		b.log.Warn().Err(err).Msg("Failed to expire unanswered detection prompts")
		return
	}
	for _, p := range expired {
		b.log.Info().Str("repo", p.Entry.RepoURL).Str("room", p.RoomID).Msg("Nobody answered the detection prompt")
		b.reply(ctx, &event.Event{ID: id.EventID(p.EventID), RoomID: id.RoomID(p.RoomID), Sender: b.userID},
			fmt.Sprintf("Nobody answered within %s, so I'm dropping this. I'll ask again the next time %s is posted.",
				formatHours(store.DetectedPendingTTL), p.Entry.RepoURL))
	}
}
