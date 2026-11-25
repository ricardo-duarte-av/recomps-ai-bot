package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/config"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/llm"
	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func testBot() *Bot {
	return &Bot{userID: id.UserID("@recompbot:example.org"), displayName: "RecompBot"}
}

func TestIsMentioned(t *testing.T) {
	b := testBot()
	yes := []*event.MessageEventContent{
		{Body: "RecompBot: is there any recomp for Pokemon Emerald"},
		{Body: "hey recompbot add this repo"},
		{Body: "@recompbot:example.org remove repo x"},
		{Body: "x", Mentions: &event.Mentions{UserIDs: []id.UserID{"@recompbot:example.org"}}},
	}
	for _, c := range yes {
		if !b.isMentioned(c, true) {
			t.Errorf("expected mention in %q", c.Body)
		}
	}
	no := []*event.MessageEventContent{
		{Body: "is there any recomp for Pokemon Emerald"},
		{Body: "recompbots are cool"},
	}
	for _, c := range no {
		if b.isMentioned(c, true) {
			t.Errorf("unexpected mention in %q", c.Body)
		}
	}
}

func TestStripMention(t *testing.T) {
	b := testBot()
	if got := b.stripMention("RecompBot: is there any recomp for Zelda"); got != "is there any recomp for Zelda" {
		t.Errorf("got %q", got)
	}
}

func TestClassifyReaction(t *testing.T) {
	for _, k := range []string{"OK", "ok", "✅", "👍️"} {
		if c, _ := classifyReaction(k); !c {
			t.Errorf("%q should confirm", k)
		}
	}
	for _, k := range []string{"Cancel", "❌"} {
		if _, c := classifyReaction(k); !c {
			t.Errorf("%q should cancel", k)
		}
	}
	if c, x := classifyReaction("🎉"); c || x {
		t.Error("🎉 should be ignored")
	}
}

// TestReplyToBot checks that a reply to the bot, which clients mark as
// mentioning it, isn't taken as addressed to the bot unless the new text names it.
func TestReplyToBot(t *testing.T) {
	b := testBot()
	reply := func(body, formatted string) *event.MessageEventContent {
		c := &event.MessageEventContent{MsgType: event.MsgText, Body: body, Format: event.FormatHTML, FormattedBody: formatted,
			Mentions:  &event.Mentions{UserIDs: []id.UserID{"@recompbot:example.org"}},
			RelatesTo: &event.RelatesTo{InReplyTo: &event.InReplyTo{EventID: "$answer"}}}
		c.RemoveReplyFallback()
		return c
	}
	thanks := reply("> <@recompbot:example.org> Found 3 recomps\n\nthanks, the zelda one is great",
		`<mx-reply><blockquote><a href="https://matrix.to/#/@recompbot:example.org">@recompbot:example.org</a> Found 3 recomps</blockquote></mx-reply>thanks, the zelda one is great`)
	if b.isMentioned(thanks, false) {
		t.Error("plain reply to the bot was taken as a mention")
	}
	named := reply("> <@recompbot:example.org> Found 3 recomps\n\nRecompBot: and for Mario?",
		`<mx-reply><blockquote>Found 3 recomps</blockquote></mx-reply>RecompBot: and for Mario?`)
	if !b.isMentioned(named, false) {
		t.Error("reply naming the bot wasn't taken as a mention")
	}
}

func TestHasKeyword(t *testing.T) {
	b := testBot()
	b.cfg = &config.Config{Matrix: config.Matrix{Keywords: []string{"!recomp", "Decomp list"}}}
	for body, want := range map[string]bool{
		"!recomp zelda":                 true,
		"any zelda? !RECOMP":            true,
		"check the decomp list please":  true,
		"!recompile everything":         false,
		"is there a decomp for mario?":  false,
		"no keywords here, just a chat": false,
	} {
		if got := b.hasKeyword(body); got != want {
			t.Errorf("hasKeyword(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestIsRequest(t *testing.T) {
	for _, tc := range []struct {
		intent llm.Intent
		want   bool
	}{
		{llm.Intent{Intent: "add"}, true},
		{llm.Intent{Intent: "complex_query"}, true},
		{llm.Intent{Intent: "query", Game: "Zelda"}, true},
		{llm.Intent{Intent: "query", Count: true}, true},
		{llm.Intent{Intent: "query"}, false},
		{llm.Intent{Intent: "unknown"}, false},
	} {
		if got := isRequest(&tc.intent); got != tc.want {
			t.Errorf("isRequest(%+v) = %v, want %v", tc.intent, got, tc.want)
		}
	}
}

func TestMarkOutcome(t *testing.T) {
	w := &work{}
	ctx := context.WithValue(context.Background(), workKey{}, w)
	markOutcome(ctx, outcomeCaptured)
	markOutcome(ctx, outcomeMissingInfo)
	markOutcome(ctx, outcomeCaptured)
	if got := strings.Join(w.outcomes, "|"); got != "Captured|Missing info, help" {
		t.Errorf("outcomes = %q", got)
	}
	markOutcome(context.Background(), outcomeIgnored) // no work attached: no-op
}

func TestMissingInfoText(t *testing.T) {
	url := "https://github.com/nikitaluger/castlevaniacotmdecomp"
	missing := missingInfo(store.Entry{RepoURL: url, Game: "Castlevania: Circle of the Moon", OriginalSystem: "Game Boy Advance", Kind: "decomp"})
	want := "I couldn't work out the supported platforms for " + url + ". Please tell me, e.g. `add this repo to the list " + url +
		" , a Super Mario 64 recomp for Nintendo 64, for windows, linux and macos`."
	if got := missingInfoText(url, missing, nil); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
