package bot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // decode IGDB image sizes
	_ "image/png"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/attachment"
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
// gameURL and systemURL link the game and original system to IGDB when not empty.
// credit ends it, e.g. "by [Some Mod](https://matrix.to/#/...)" or "auto-added".
func announcement(e store.Entry, gameURL, systemURL, credit string) string {
	kind := kindNoun(e.Kind)
	platforms := oses.JoinDisplay(sortOSes(e.OSes))
	system := mdLink(e.OriginalSystem, systemURL)
	var what string
	if e.Kind == store.KindToolkit {
		what = fmt.Sprintf("a toolkit for %s games, running on %s", system, platforms)
	} else {
		what = fmt.Sprintf("a %s of %s from %s for %s", kind, mdLink(e.Game, gameURL), system, platforms)
	}
	return fmt.Sprintf("New %s added to the DB: **%s**, %s - %s , %s",
		kind, repoName(e.RepoURL), what, e.RepoURL, credit)
}

// creditBy credits a moderator in an announcement, as a pill showing name.
func creditBy(name string, by id.UserID) string {
	return fmt.Sprintf("by [%s](%s)", escapeLinkText(name), matrixToURL(by))
}

// mdLink is text as a markdown link to href, or plain text when href is empty.
func mdLink(text, href string) string {
	if href == "" {
		return text
	}
	return "[" + escapeLinkText(text) + "](" + href + ")"
}

// matrixToURL links to a user with the ID percent-encoded, e.g.
// https://matrix.to/#/%40mod%3Aexample.org, which clients turn into a user pill.
func matrixToURL(userID id.UserID) string {
	return "https://matrix.to/#/" + url.QueryEscape(userID.String())
}

func escapeLinkText(s string) string {
	return strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `(`, `\(`, `)`, `\)`, `*`, `\*`, `_`, `\_`, "`", "\\`", `<`, `\<`, `>`, `\>`).Replace(s)
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

// igdbLinks returns the IGDB pages of e's game and original system ("" when
// unknown or IGDB isn't configured), waiting at most 15 seconds for IGDB.
func (b *Bot) igdbLinks(ctx context.Context, e store.Entry) (gameURL, systemURL string) {
	if b.igdb == nil {
		return "", ""
	}
	game := e.Game
	if e.Kind == store.KindToolkit {
		game = ""
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return b.igdb.Links(ctx, game, e.OriginalSystem)
}

// announce posts a new entry to matrix.announce_room, if one is configured, ending
// with credit (see announcement).
func (b *Bot) announce(ctx context.Context, e store.Entry, credit string) {
	if b.announceRoom == "" {
		return
	}
	log := zerolog.Ctx(ctx)
	gameURL, systemURL := b.igdbLinks(ctx, e)
	content := format.RenderMarkdown(announcement(e, gameURL, systemURL, credit), true, false)
	// Toolkits get their system's logo; games their cover or a screenshot.
	page := gameURL
	if e.Kind == store.KindToolkit {
		page = systemURL
	}
	if withImage, err := b.withIGDBImage(ctx, content, page); err != nil {
		log.Warn().Err(err).Str("page", page).Msg("Failed to attach IGDB image to announcement; posting text only")
	} else if withImage != nil {
		content = *withImage
	}
	if _, err := b.client.SendMessageEvent(ctx, b.announceRoom, event.EventMessage, &content); err != nil {
		log.Warn().Err(err).Msg("Failed to post announcement")
	}
}

// withIGDBImage turns a text message into an image of the IGDB page's game or
// platform with the text as its caption. It returns nil when there's no image.
func (b *Bot) withIGDBImage(ctx context.Context, caption event.MessageEventContent, page string) (*event.MessageEventContent, error) {
	if b.igdb == nil || page == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	img, err := b.igdb.Image(ctx, page)
	if err != nil || img == nil {
		return nil, err
	}
	content := caption
	content.MsgType = event.MsgImage
	content.FileName = img.FileName
	content.Info = &event.FileInfo{MimeType: img.MimeType, Size: len(img.Data)}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data)); err == nil {
		content.Info.Width, content.Info.Height = cfg.Width, cfg.Height
	}

	data, mime := img.Data, img.MimeType
	var file *attachment.EncryptedFile
	if b.isEncrypted(ctx, b.announceRoom) {
		file = attachment.NewEncryptedFile()
		data = file.Encrypt(data)
		mime = "application/octet-stream"
	}
	resp, err := b.client.UploadMedia(ctx, mautrix.ReqUploadMedia{ContentBytes: data, ContentType: mime, FileName: img.FileName})
	if err != nil {
		return nil, fmt.Errorf("uploading image: %w", err)
	}
	if file != nil {
		content.File = &event.EncryptedFileInfo{EncryptedFile: *file, URL: resp.ContentURI.CUString()}
	} else {
		content.URL = resp.ContentURI.CUString()
	}
	return &content, nil
}

// isEncrypted reports whether room has encryption enabled.
func (b *Bot) isEncrypted(ctx context.Context, room id.RoomID) bool {
	if b.client.StateStore != nil {
		if enc, err := b.client.StateStore.IsEncrypted(ctx, room); err == nil && enc {
			return true
		}
	}
	var enc event.EncryptionEventContent
	return b.client.StateEvent(ctx, room, event.StateEncryption, "", &enc) == nil && enc.Algorithm != ""
}
