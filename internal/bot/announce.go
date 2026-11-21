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

// kindNoun names a kind in announcements.
func kindNoun(kind string) string {
	switch kind {
	case store.KindRecomp:
		return "recompilation"
	case store.KindDecomp:
		return "decompilation"
	case store.KindReimpl:
		return "reimplementation"
	}
	return kind
}

// repoName is the last path segment of a repo URL, e.g. "sm64" for github.com/n64decomp/sm64.
func repoName(repoURL string) string {
	return repoURL[strings.LastIndex(strings.TrimRight(repoURL, "/"), "/")+1:]
}

// announcement is the markdown posted to the announcements room for a new entry.
func announcement(e store.Entry, name string, by id.UserID) string {
	kind := kindNoun(e.Kind)
	platforms := oses.JoinDisplay(sortOSes(e.OSes))
	var what string
	if e.Kind == store.KindToolkit {
		what = fmt.Sprintf("a toolkit for %s games, running on %s", e.OriginalSystem, platforms)
	} else {
		what = fmt.Sprintf("a %s of %s from %s for %s", kind, e.Game, e.OriginalSystem, platforms)
	}
	return fmt.Sprintf("New %s added to the DB: **%s**, %s - %s , by [%s](%s)",
		kind, repoName(e.RepoURL), what, e.RepoURL, escapeLinkText(name), matrixToURL(by))
}

// matrixToURL links to a user with the ID percent-encoded, e.g.
// https://matrix.to/#/%40mod%3Aexample.org, which clients turn into a user pill.
func matrixToURL(userID id.UserID) string {
	return "https://matrix.to/#/" + url.QueryEscape(userID.String())
}

func escapeLinkText(s string) string {
	return strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `*`, `\*`, `_`, `\_`, "`", "\\`").Replace(s)
}

// userDisplayName is userID's display name in roomID, falling back to their global
// display name and then their user ID.
func (b *Bot) userDisplayName(ctx context.Context, roomID id.RoomID, userID id.UserID) string {
	var m event.MemberEventContent
	if err := b.client.StateEvent(ctx, roomID, event.StateMember, userID.String(), &m); err == nil && m.Displayname != "" {
		return m.Displayname
	}
	if resp, err := b.client.GetDisplayName(ctx, userID); err == nil && resp.DisplayName != "" {
		return resp.DisplayName
	}
	return userID.String()
}

// announce posts a new entry to matrix.announce_room, if one is configured.
// roomID is the room the confirming moderator reacted in.
func (b *Bot) announce(ctx context.Context, e store.Entry, roomID id.RoomID, by id.UserID) {
	if b.announceRoom == "" {
		return
	}
	content := format.RenderMarkdown(announcement(e, b.userDisplayName(ctx, roomID, by), by), true, false)
	if _, err := b.client.SendMessageEvent(ctx, b.announceRoom, event.EventMessage, &content); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to post announcement")
	}
}
