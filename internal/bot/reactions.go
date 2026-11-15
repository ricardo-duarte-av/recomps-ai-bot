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
	if evt.Sender == b.userID || !b.rooms[evt.RoomID] {
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
	if !confirm {
		b.reply(ctx, target, fmt.Sprintf("Cancelled by %s.", mention(evt.Sender)))
		return
	}

	switch p.Action {
	case "add":
		if err := b.store.Add(ctx, e, p.RequestedBy); err != nil {
			log.Err(err).Msg("Failed to add entry")
			b.reply(ctx, target, "Database error, the entry was not added.")
			return
		}
		log.Info().Str("repo", e.RepoURL).Strs("oses", e.OSes).Msg("Added entry")
		b.reply(ctx, target, fmt.Sprintf("Added %s (%s of **%s** for %s) for %s. Confirmed by %s.",
			e.RepoURL, e.Kind, e.Game, e.OriginalSystem, oses.JoinDisplay(e.OSes), mention(evt.Sender)))
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
