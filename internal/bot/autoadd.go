package bot

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

// Text reactions for automatically added repos.
const (
	outcomeAutoAdded          = "Auto-added"                    // added from this message
	outcomeAutoAddedElsewhere = "Auto-added from another room" // added from the same link posted in another room
)

// autoAddStateType is the room state event that turns auto mode on or off in one
// room, overriding detect.auto. Kept in the room (not the bot's config or database)
// so the setting survives restarts and is visible to the room's moderators.
var autoAddStateType = event.Type{Type: "org.recompbot.auto_add", Class: event.StateEventType}

type autoAddState struct {
	// Enabled is a pointer so an empty (e.g. redacted) event means "no override".
	Enabled *bool `json:"enabled,omitempty"`
}

// autoAddedWindow is how long after an auto-add the same link posted in another
// room gets outcomeAutoAddedElsewhere rather than the plain "already listed" reaction.
const autoAddedWindow = time.Hour

type autoAddRecord struct {
	room id.RoomID
	at   time.Time
}

// autoAddSetting reports whether auto mode is on in room, and whether that comes
// from the room's state event (true) or detect.auto (false). When the state can't be
// read, it errs on the side of asking: auto mode is off.
func (b *Bot) autoAddSetting(ctx context.Context, room id.RoomID) (enabled, fromRoom bool) {
	var st autoAddState
	err := b.client.StateEvent(ctx, room, autoAddStateType, "", &st)
	switch {
	case errors.Is(err, mautrix.MNotFound):
		return b.cfg.Detect.Auto, false
	case err != nil:
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to read the room's auto-add setting; asking instead")
		return false, false
	case st.Enabled == nil:
		return b.cfg.Detect.Auto, false
	}
	return *st.Enabled, true
}

// canAutoAdd reports whether repos detected in evt may be added without asking:
// auto mode is on in its room and its sender has at least detect.auto_level power.
func (b *Bot) canAutoAdd(ctx context.Context, evt *event.Event) bool {
	if enabled, _ := b.autoAddSetting(ctx, evt.RoomID); !enabled {
		return false
	}
	pl, err := b.powerLevels(ctx, evt.RoomID)
	if err != nil {
		return false
	}
	return pl.GetUserLevel(evt.Sender) >= b.cfg.Detect.AutoMinLevel()
}

// autoAdd adds a fully classified detected repo posted in evt, unless the repo got
// into the list meanwhile. It closes the repo's open prompts in other rooms and
// announces it, without naming the room or the poster. It reports whether it added it.
func (b *Bot) autoAdd(ctx context.Context, evt *event.Event, e store.Entry) (bool, error) {
	log := zerolog.Ctx(ctx)
	added, err := b.store.AddIfAbsent(ctx, e, evt.Sender.String())
	if err != nil || !added {
		return added, err
	}
	b.autoAdded.Store(e.RepoURL, autoAddRecord{room: evt.RoomID, at: time.Now()})
	log.Info().Interface("entry", e).Msg("Auto-added detected repo")

	others, err := b.store.TakePendingDetectedAdds(ctx, e.RepoURL)
	if err != nil {
		log.Err(err).Msg("Failed to take the repo's prompts in other rooms")
	}
	for _, o := range others {
		room, prompt := id.RoomID(o.RoomID), id.EventID(o.EventID)
		if !b.closePrompt(ctx, room, prompt, outcomeAutoAddedElsewhere) {
			b.reply(ctx, &event.Event{ID: prompt, RoomID: room, Sender: b.userID},
				fmt.Sprintf("%s was auto-added from another room.", e.RepoURL))
		}
	}
	b.announce(ctx, e, "auto-added")
	b.igdb.Kick() // link the game on the web page
	return true, nil
}

// autoAddedElsewhere reports whether url was auto-added recently from a room other than room.
func (b *Bot) autoAddedElsewhere(url string, room id.RoomID) bool {
	v, ok := b.autoAdded.Load(url)
	if !ok {
		return false
	}
	r := v.(autoAddRecord)
	if time.Since(r.at) > autoAddedWindow {
		b.autoAdded.Delete(url)
		return false
	}
	return r.room != room
}

// requestHelp asks for the details the bot couldn't work out about a detected repo.
// The question goes to the announcements room, pinging its moderators and linking
// the message the repo was posted in; without one, it's a reply to that message.
func (b *Bot) requestHelp(ctx context.Context, evt *event.Event, url string, missing []string) {
	text := missingInfoText(url, missing, nil)
	if b.announceRoom == "" {
		b.reply(ctx, evt, text)
		return
	}
	link := evt.RoomID.EventURI(evt.ID).MatrixToURL()
	msg := fmt.Sprintf("**Help needed** with a repository [posted here](%s): %s", link, text)
	pings, mentions := b.suggestionPings(ctx, b.announceRoom)
	if pings != "" {
		msg += "\n\nPinging " + pings
	}
	content := format.RenderMarkdown(msg, true, false)
	if mentions != nil {
		content.Mentions = mentions
	} else {
		content.Mentions = &event.Mentions{}
	}
	if _, err := b.client.SendMessageEvent(ctx, b.announceRoom, event.EventMessage, &content); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to ask for help in the announcements room; replying instead")
		b.reply(ctx, evt, text)
	}
}

var (
	autoAddToggleRe = regexp.MustCompile(`^(stop|start|disable|enable|pause|resume) auto ?-?add(?:ing)?(?: (?:in|for) this room)?$`)
	autoAddStatusRe = regexp.MustCompile(`^(?:is )?auto ?-?add(?:ing)?(?: status| on| enabled)?(?: (?:in|for) this room)?$`)
)

// autoAddCommand recognizes "stop/start auto-adding in this room" and
// "auto-adding status". enable is nil for a status question.
func autoAddCommand(text string) (enable *bool, ok bool) {
	t := strings.Join(strings.Fields(strings.ToLower(strings.Trim(strings.TrimSpace(text), "!?."))), " ")
	if m := autoAddToggleRe.FindStringSubmatch(t); m != nil {
		on := m[1] == "start" || m[1] == "enable" || m[1] == "resume"
		return &on, true
	}
	if autoAddStatusRe.MatchString(t) {
		return nil, true
	}
	return nil, false
}

// handleAutoAddCommand turns auto mode on or off in the message's room by writing the
// room's auto-add state event, or says whether it's on.
func (b *Bot) handleAutoAddCommand(ctx context.Context, evt *event.Event, enable *bool) {
	if !b.rooms[evt.RoomID] {
		b.reply(ctx, evt, "Auto-adding only applies to rooms where I watch for repository links.")
		return
	}
	level := b.cfg.Detect.AutoMinLevel()
	describeOn := fmt.Sprintf("repos posted here by members with power level %d or more are added without asking when I can fully identify them; other links get the usual OK/Cancel prompt", level)
	describeOff := "detected repos posted here get the usual OK/Cancel prompt"
	if enable == nil {
		enabled, fromRoom := b.autoAddSetting(ctx, evt.RoomID)
		source := "the bot's default"
		if fromRoom {
			source = "this room's setting"
		}
		state, what := "off", describeOff
		if enabled {
			state, what = "on", describeOn
		}
		b.reply(ctx, evt, fmt.Sprintf("Auto-adding is **%s** in this room (%s): %s.", state, source, what))
		return
	}
	if !b.isModerator(ctx, evt.RoomID, evt.Sender) {
		b.reply(ctx, evt, "Only moderators can change auto-adding.")
		return
	}
	if _, err := b.client.SendStateEvent(ctx, evt.RoomID, autoAddStateType, "", &autoAddState{Enabled: enable}); err != nil {
		zerolog.Ctx(ctx).Err(err).Bool("enable", *enable).Msg("Failed to save the room's auto-add setting")
		markFailed(ctx)
		b.reply(ctx, evt, fmt.Sprintf("I couldn't save that (I need permission to send `%s` state events in this room).", autoAddStateType.Type))
		return
	}
	zerolog.Ctx(ctx).Info().Bool("enable", *enable).Msg("Changed the room's auto-add setting")
	msg := "OK, auto-adding is now **off** in this room: " + describeOff + "."
	if *enable {
		msg = "OK, auto-adding is now **on** in this room: " + describeOn + "."
		if !b.cfg.Detect.IsEnabled() {
			msg += " Note that link detection is turned off in my config, so nothing is detected right now."
		}
	}
	b.reply(ctx, evt, msg)
}
