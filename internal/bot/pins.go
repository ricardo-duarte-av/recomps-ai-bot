package bot

import (
	"context"
	"errors"
	"slices"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// setPinned pins or unpins eventID in roomID. Failures (e.g. the bot lacking the
// power level to change pinned events) are only logged.
func (b *Bot) setPinned(ctx context.Context, roomID id.RoomID, eventID id.EventID, pin bool) {
	log := zerolog.Ctx(ctx).With().Stringer("room", roomID).Stringer("event", eventID).Bool("pin", pin).Logger()
	b.pinMu.Lock()
	defer b.pinMu.Unlock()

	var content event.PinnedEventsEventContent
	if err := b.client.StateEvent(ctx, roomID, event.StatePinnedEvents, "", &content); err != nil && !errors.Is(err, mautrix.MNotFound) {
		log.Warn().Err(err).Msg("Failed to read pinned events")
		return
	}
	pinned := slices.Contains(content.Pinned, eventID)
	if pinned == pin {
		return
	}
	if pin {
		content.Pinned = append(content.Pinned, eventID)
	} else {
		content.Pinned = slices.DeleteFunc(content.Pinned, func(e id.EventID) bool { return e == eventID })
	}
	if content.Pinned == nil {
		content.Pinned = []id.EventID{}
	}
	if _, err := b.client.SendStateEvent(ctx, roomID, event.StatePinnedEvents, "", &content); err != nil {
		log.Warn().Err(err).Msg("Failed to update pinned events")
	}
}
