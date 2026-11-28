package bot

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/oses"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

// addLog describes where an added entry came from, for the log room.
type addLog struct {
	// Manual is true when a moderator added it by confirming a prompt, false for auto-adds.
	Manual bool
	// Update is true when the repo was already in the list.
	Update bool
	// By is who added it: the confirming moderator, or the link's poster for auto-adds.
	By, ByName string
	// RequestedBy and RequestedByName name who asked for a manual add, when not By.
	RequestedBy, RequestedByName string
	// Room is a markdown link to the room it was added from.
	Room string
}

// logLine is the markdown posted to the log room for an added entry, e.g.
// "Manually adding https://github.com/... by [Mod](https://matrix.to/#/...) in room
// [Room](https://matrix.to/#/...), a recomp for **Game** from _System_, for _Windows and Linux_".
func logLine(e store.Entry, l addLog) string {
	verb := "Adding"
	if l.Update {
		verb = "Updating"
	}
	var sb strings.Builder
	if l.Manual {
		fmt.Fprintf(&sb, "Manually %s %s by %s", strings.ToLower(verb), e.RepoURL, userPill(l.ByName, l.By))
		if l.RequestedBy != "" && l.RequestedBy != l.By {
			fmt.Fprintf(&sb, " (requested by %s)", userPill(l.RequestedByName, l.RequestedBy))
		}
		fmt.Fprintf(&sb, " in room %s", l.Room)
	} else {
		fmt.Fprintf(&sb, "%s %s , auto-added from a link posted by %s in room %s", verb, e.RepoURL, userPill(l.ByName, l.By), l.Room)
	}
	platforms := oses.JoinDisplay(sortOSes(e.OSes))
	if e.Kind == store.KindToolkit {
		fmt.Fprintf(&sb, ", a toolkit for _%s_ games, for _%s_", escapeLinkText(e.OriginalSystem), platforms)
	} else {
		fmt.Fprintf(&sb, ", a %s for **%s** from _%s_ , for _%s_", e.Kind, escapeLinkText(e.Game), escapeLinkText(e.OriginalSystem), platforms)
	}
	return sb.String()
}

// userPill is a markdown link that clients render as a pill for userID, showing name.
func userPill(name, userID string) string {
	if name == "" {
		name = userID
	}
	return fmt.Sprintf("[%s](%s)", escapeLinkText(name), matrixToURL(id.UserID(userID)))
}

// roomPill is a markdown link to room that clients render as a room pill: its
// canonical alias when it has one, else its ID with a via server, showing its name.
func (b *Bot) roomPill(ctx context.Context, room id.RoomID) string {
	var alias event.CanonicalAliasEventContent
	_ = b.client.StateEvent(ctx, room, event.StateCanonicalAlias, "", &alias)
	var name event.RoomNameEventContent
	_ = b.client.StateEvent(ctx, room, event.StateRoomName, "", &name)

	var target string
	if alias.Alias != "" {
		target = url.PathEscape(alias.Alias.String())
	} else {
		via := b.userID.Homeserver()
		if _, server, ok := strings.Cut(room.String(), ":"); ok {
			via = server
		}
		target = url.PathEscape(room.String()) + "?via=" + url.QueryEscape(via)
	}
	text := name.Name
	if text == "" && alias.Alias != "" {
		text = alias.Alias.String()
	}
	if text == "" {
		text = room.String()
	}
	return fmt.Sprintf("[%s](https://matrix.to/#/%s)", escapeLinkText(text), target)
}

// logAdd posts an added entry to matrix.log_room, if one is configured. room is
// where it was added from.
func (b *Bot) logAdd(ctx context.Context, e store.Entry, room id.RoomID, l addLog) {
	if b.logRoom == "" {
		return
	}
	l.Room = b.roomPill(ctx, room)
	if l.ByName == "" {
		l.ByName = b.userDisplayName(ctx, room, id.UserID(l.By))
	}
	if l.RequestedBy != "" && l.RequestedBy != l.By && l.RequestedByName == "" {
		l.RequestedByName = b.userDisplayName(ctx, room, id.UserID(l.RequestedBy))
	}
	content := format.RenderMarkdown(logLine(e, l), true, false)
	content.MsgType = event.MsgNotice
	if _, err := b.client.SendMessageEvent(ctx, b.logRoom, event.EventMessage, &content); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to post to the log room")
	}
}
