package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
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
	// closeAll swaps the reactions on every answered prompt for the outcome, e.g.
	// "Added by @mod:server". The message is only sent where that reaction can't be.
	closeAll := func(verb, msg, othersMsg string) {
		label := fmt.Sprintf("%s by %s", verb, evt.Sender)
		if !b.closePrompt(ctx, evt.RoomID, promptID, label, evt.ID) {
			b.reply(ctx, target, msg)
		}
		for _, o := range others {
			if !b.closePrompt(ctx, id.RoomID(o.RoomID), id.EventID(o.EventID), label) {
				b.reply(ctx, &event.Event{ID: id.EventID(o.EventID), RoomID: id.RoomID(o.RoomID), Sender: b.userID}, othersMsg)
			}
		}
	}

	if !confirm {
		if p.Action == "add" && e.Detected {
			if err := b.store.MarkSeen(ctx, e.RepoURL, store.SeenDismissed); err != nil {
				log.Err(err).Msg("Failed to remember dismissed repo")
			}
			msg := fmt.Sprintf("I won't suggest %s again (a moderator can still add it explicitly).", e.RepoURL)
			closeAll("Dismissed",
				fmt.Sprintf("Dismissed by %s. %s", mention(evt.Sender), msg),
				fmt.Sprintf("Dismissed by %s in another room. %s", mention(evt.Sender), msg))
			return
		}
		closeAll("Cancelled", fmt.Sprintf("Cancelled by %s.", mention(evt.Sender)), "")
		return
	}

	switch p.Action {
	case "add":
		addedBy := p.RequestedBy
		if e.Detected {
			addedBy = evt.Sender.String() // the link poster didn't ask for this; the confirming mod did
		}
		// Only brand-new repos are announced, not updates of an existing entry.
		existing, err := b.store.GetByRepo(ctx, e.RepoURL)
		if err != nil {
			log.Err(err).Msg("Failed to look up repo")
		}
		if err := b.store.Add(ctx, e, addedBy); err != nil {
			log.Err(err).Msg("Failed to add entry")
			b.reply(ctx, target, "Database error, the entry was not added.")
			return
		}
		log.Info().Str("repo", e.RepoURL).Strs("oses", e.OSes).Msg("Added entry")
		added := fmt.Sprintf("Added %s (%s). Confirmed by %s", e.RepoURL, describe(e), mention(evt.Sender))
		closeAll("Added", added+".", added+" in another room.")
		if existing == nil && err == nil {
			b.announce(ctx, e, evt.RoomID, evt.Sender)
		}
	case "remove":
		n, err := b.store.Remove(ctx, e.RepoURL, e.OSes)
		if err != nil {
			log.Err(err).Msg("Failed to remove entry")
			b.reply(ctx, target, "Database error, nothing was removed.")
			return
		}
		log.Info().Str("repo", e.RepoURL).Strs("oses", e.OSes).Int64("rows", n).Msg("Removed entry")
		closeAll("Removed", fmt.Sprintf("Removed %s from %s for %s. Confirmed by %s.",
			e.RepoURL, oses.JoinDisplay(e.OSes), subject(e), mention(evt.Sender)), "")
	}
}

// closePromptRecheck is how long after closing a prompt its reactions are listed
// again, to catch ones the server hadn't indexed yet the first time.
var closePromptRecheck = 3 * time.Second

// closePrompt redacts every reaction on an answered prompt (the bot's own OK/Cancel
// and everyone's votes) and reacts with label instead, so the prompt shows its outcome
// and can't be mistaken for still open. It reports whether the label reaction was sent.
// Needs the bot to be allowed to redact others' events. All annotations are redacted,
// not just OK/Cancel ones: in encrypted rooms the reaction key isn't readable without
// decrypting each event.
//
// extra are reactions known to be on the prompt (the answering one), redacted even if
// the server's relations listing doesn't include them yet: it can lag behind sync, which
// would leave the moderator's own reaction behind. The listing is also redone after
// closePromptRecheck for the same reason.
func (b *Bot) closePrompt(ctx context.Context, roomID id.RoomID, promptID id.EventID, label string, extra ...id.EventID) bool {
	log := zerolog.Ctx(ctx).With().Stringer("room", roomID).Stringer("prompt", promptID).Logger()
	if v, ok := b.promptSetup.Load(promptID); ok {
		select {
		case <-v.(chan struct{}):
		case <-ctx.Done():
			log.Warn().Msg("Gave up waiting for the prompt's reactions and pin")
		}
	}
	b.setPinned(ctx, roomID, promptID, false)
	redacted := map[id.EventID]bool{}
	redact := func(ids ...id.EventID) {
		for _, r := range ids {
			if redacted[r] {
				continue
			}
			redacted[r] = true
			if _, err := b.client.RedactEvent(ctx, roomID, r); err != nil {
				log.Warn().Err(err).Stringer("reaction", r).Msg("Failed to redact reaction")
			}
		}
	}
	redact(extra...)
	redact(b.promptReactions(ctx, roomID, promptID)...)

	resp, err := b.client.SendReaction(ctx, roomID, promptID, label)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to add outcome reaction")
	} else {
		redacted[resp.EventID] = true // keep the outcome
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		select {
		case <-time.After(closePromptRecheck):
		case <-ctx.Done():
			return
		}
		redact(b.promptReactions(ctx, roomID, promptID)...)
	}()
	return err == nil
}

// promptReactions lists the IDs of all annotations on promptID.
func (b *Bot) promptReactions(ctx context.Context, roomID id.RoomID, promptID id.EventID) []id.EventID {
	var out []id.EventID
	req := &mautrix.ReqGetRelations{RelationType: event.RelAnnotation, Limit: 100}
	for {
		resp, err := b.client.GetRelations(ctx, roomID, promptID, req)
		if err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Stringer("room", roomID).Stringer("prompt", promptID).
				Msg("Failed to list the prompt's reactions")
			return out
		}
		for _, r := range resp.Chunk {
			out = append(out, r.ID)
		}
		if resp.NextBatch == "" {
			return out
		}
		req.From = resp.NextBatch
	}
}

// expireLoop closes link-detection prompts nobody answered in time, in every room
// they were asked in. The repo isn't dismissed, so the next time it's posted it's
// suggested again, reusing the cached classification. Prompts that expired while the bot was down are handled on the
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
		if err := b.store.CacheDetected(ctx, p.Entry); err != nil {
			b.log.Warn().Err(err).Str("repo", p.Entry.RepoURL).Msg("Failed to cache detected repo")
		}
		b.log.Info().Str("repo", p.Entry.RepoURL).Str("room", p.RoomID).Msg("Nobody answered the detection prompt")
		b.reply(ctx, &event.Event{ID: id.EventID(p.EventID), RoomID: id.RoomID(p.RoomID), Sender: b.userID},
			fmt.Sprintf("Nobody answered within %s, so I'm dropping this. I'll ask again the next time %s is posted.",
				formatHours(store.DetectedPendingTTL), p.Entry.RepoURL))
		b.setPinned(ctx, id.RoomID(p.RoomID), id.EventID(p.EventID), false)
	}
}
