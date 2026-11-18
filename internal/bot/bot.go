// Package bot implements the Matrix side of the recomp/decomp/reimpl list bot.
package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/config"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/forge"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

type Bot struct {
	cfg    *config.Config
	log    zerolog.Logger
	client *mautrix.Client
	crypto *cryptohelper.CryptoHelper
	store  *store.Store
	llm    *llm.Client
	forge  *forge.Client

	userID      id.UserID
	displayName string
	rooms       map[id.RoomID]bool
	ignored     map[id.UserID]bool
	startTime   time.Time

	createEvents sync.Map // id.RoomID -> *event.Event (m.room.create never changes)
	detecting    sync.Map // repo URL -> *sync.Mutex held while link detection checks it
}

func New(cfg *config.Config, log zerolog.Logger, st *store.Store, l *llm.Client, fc *forge.Client) *Bot {
	ignored := map[id.UserID]bool{}
	for _, u := range cfg.Matrix.IgnoredUsers {
		ignored[id.UserID(u)] = true
	}
	return &Bot{cfg: cfg, log: log, store: st, llm: l, forge: fc, rooms: map[id.RoomID]bool{}, ignored: ignored}
}

// Run logs in, sets up encryption, joins the configured rooms and syncs until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.userID = id.UserID(b.cfg.Matrix.UserID)
	client, err := mautrix.NewClient(b.cfg.Matrix.Homeserver, b.userID, "")
	if err != nil {
		return err
	}
	client.Log = b.log
	b.client = client

	b.crypto, err = cryptohelper.NewCryptoHelper(client, []byte(b.cfg.Matrix.PickleKey), b.store.DB)
	if err != nil {
		return fmt.Errorf("creating crypto helper: %w", err)
	}
	b.crypto.DBAccountID = b.userID.String()
	b.crypto.LoginAs = &mautrix.ReqLogin{
		Type:                     mautrix.AuthTypePassword,
		Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: b.userID.String()},
		Password:                 b.cfg.Matrix.Password,
		InitialDeviceDisplayName: b.cfg.Matrix.DeviceName,
	}
	if err := b.crypto.Init(ctx); err != nil {
		return fmt.Errorf("initializing crypto (login): %w", err)
	}
	defer b.crypto.Close()
	client.Crypto = b.crypto
	b.log.Info().Str("device_id", client.DeviceID.String()).Msg("Logged in")

	if resp, err := client.GetOwnDisplayName(ctx); err == nil {
		b.displayName = resp.DisplayName
	}

	for _, r := range b.cfg.Matrix.AllowedRooms {
		roomID, err := b.resolveRoom(ctx, r)
		if err != nil {
			return fmt.Errorf("resolving room %s: %w", r, err)
		}
		b.rooms[roomID] = true
		if _, err := client.JoinRoom(ctx, r, nil); err != nil {
			b.log.Warn().Err(err).Str("room", r).Msg("Failed to join allowed room")
		}
	}

	b.startTime = time.Now()
	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, b.onMessage)
	syncer.OnEventType(event.EventReaction, b.onReaction)
	syncer.OnEventType(event.StateMember, b.onMember)

	go b.purgeLoop(ctx)
	go b.expireLoop(ctx)

	b.log.Info().Int("rooms", len(b.rooms)).Msg("Starting sync")
	err = client.SyncWithContext(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (b *Bot) resolveRoom(ctx context.Context, r string) (id.RoomID, error) {
	if strings.HasPrefix(r, "#") {
		resp, err := b.client.ResolveAlias(ctx, id.RoomAlias(r))
		if err != nil {
			return "", err
		}
		return resp.RoomID, nil
	}
	return id.RoomID(r), nil
}

func (b *Bot) purgeLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := b.store.PurgeExpired(ctx); err != nil {
				b.log.Warn().Err(err).Msg("Failed to purge expired pending actions")
			}
		}
	}
}

// onMember auto-joins invites to allowed rooms only.
func (b *Bot) onMember(ctx context.Context, evt *event.Event) {
	if evt.GetStateKey() != b.userID.String() || !b.rooms[evt.RoomID] {
		return
	}
	if evt.Content.AsMember().Membership == event.MembershipInvite {
		if _, err := b.client.JoinRoomByID(ctx, evt.RoomID); err != nil {
			b.log.Warn().Err(err).Stringer("room", evt.RoomID).Msg("Failed to accept invite")
		}
	}
}

func (b *Bot) onMessage(ctx context.Context, evt *event.Event) {
	if evt.Sender == b.userID || b.ignored[evt.Sender] || !b.rooms[evt.RoomID] {
		return
	}
	if time.UnixMilli(evt.Timestamp).Before(b.startTime) {
		return
	}
	content := evt.Content.AsMessage()
	if content.RelatesTo.GetReplaceID() != "" {
		return
	}
	if content.MsgType != event.MsgText && content.MsgType != event.MsgNotice {
		return
	}
	if content.MsgType == event.MsgNotice || !b.isMentioned(content) {
		// Not a command: just watch for repository links (notices too, e.g. feed bots).
		if b.cfg.Detect.IsEnabled() {
			content.RemoveReplyFallback()
			b.detectLinks(evt, content.Body)
		}
		return
	}
	content.RemoveReplyFallback()
	text := b.stripMention(content.Body)
	if text == "" {
		return
	}
	// LLM and forge calls are slow; don't block the sync loop.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		log := b.log.With().Stringer("room", evt.RoomID).Stringer("sender", evt.Sender).Stringer("event", evt.ID).Logger()
		ctx = log.WithContext(ctx)
		ctx, prog := b.startWork(ctx, evt)
		defer prog.finish(ctx)
		b.handleCommand(ctx, evt, text)
	}()
}

func (b *Bot) isMentioned(content *event.MessageEventContent) bool {
	if content.Mentions != nil && content.Mentions.Has(b.userID) {
		return true
	}
	body := strings.ToLower(content.Body)
	if strings.Contains(body, strings.ToLower(b.userID.String())) {
		return true
	}
	if strings.Contains(content.FormattedBody, b.userID.URI().MatrixToURL()) ||
		strings.Contains(content.FormattedBody, "matrix.to/#/"+b.userID.String()) {
		return true
	}
	for _, name := range b.names() {
		if containsWord(body, name) {
			return true
		}
	}
	return false
}

// names returns lowercase names the bot answers to (display name, localpart).
func (b *Bot) names() []string {
	var out []string
	if b.displayName != "" {
		out = append(out, strings.ToLower(b.displayName))
	}
	out = append(out, strings.ToLower(b.userID.Localpart()))
	return slices.Compact(out)
}

func containsWord(haystack, word string) bool {
	if word == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(haystack[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		if (start == 0 || !isWordChar(haystack[start-1])) && (end == len(haystack) || !isWordChar(haystack[end])) {
			return true
		}
		i = start + 1
	}
}

func isWordChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func (b *Bot) stripMention(body string) string {
	out := strings.ReplaceAll(body, b.userID.String(), "")
	lower := strings.ToLower(out)
	for _, name := range b.names() {
		if idx := strings.Index(lower, name); idx >= 0 {
			out = out[:idx] + out[idx+len(name):]
			lower = strings.ToLower(out)
		}
	}
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(out), ":,"))
}

// reply sends a markdown message answering evt (as a Matrix reply unless
// matrix.answer_with_reply is false) and returns its event ID.
func (b *Bot) reply(ctx context.Context, evt *event.Event, markdown string) id.EventID {
	return b.send(ctx, evt, markdown, false)
}

// send is reply with an optional @room mention.
func (b *Bot) send(ctx context.Context, evt *event.Event, markdown string, mentionRoom bool) id.EventID {
	content := format.RenderMarkdown(markdown, true, false)
	if b.cfg.Matrix.ShouldReply() {
		content.SetReply(evt)
	}
	if mentionRoom {
		if content.Mentions == nil {
			content.Mentions = &event.Mentions{}
		}
		content.Mentions.Room = true
	}
	resp, err := b.client.SendMessageEvent(ctx, evt.RoomID, event.EventMessage, &content)
	if err != nil {
		markFailed(ctx)
		zerolog.Ctx(ctx).Err(err).Msg("Failed to send reply")
		return ""
	}
	return resp.EventID
}

// isModerator reports whether userID has at least the configured moderator power level.
func (b *Bot) isModerator(ctx context.Context, roomID id.RoomID, userID id.UserID) bool {
	var pl event.PowerLevelsEventContent
	if err := b.client.StateEvent(ctx, roomID, event.StatePowerLevels, "", &pl); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to fetch power levels")
		return false
	}
	pl.CreateEvent = b.createEvent(ctx, roomID)
	return pl.GetUserLevel(userID) >= b.cfg.Matrix.ModeratorLevel
}

// createEvent is needed for room v12+, where room creators have unlimited power.
func (b *Bot) createEvent(ctx context.Context, roomID id.RoomID) *event.Event {
	if v, ok := b.createEvents.Load(roomID); ok {
		return v.(*event.Event)
	}
	evt, err := b.client.FullStateEvent(ctx, roomID, event.StateCreate, "")
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to fetch create event")
		return nil
	}
	_ = evt.Content.ParseRaw(evt.Type)
	b.createEvents.Store(roomID, evt)
	return evt
}
