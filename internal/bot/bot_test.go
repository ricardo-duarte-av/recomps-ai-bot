package bot

import (
	"testing"

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
		if !b.isMentioned(c) {
			t.Errorf("expected mention in %q", c.Body)
		}
	}
	no := []*event.MessageEventContent{
		{Body: "is there any recomp for Pokemon Emerald"},
		{Body: "recompbots are cool"},
	}
	for _, c := range no {
		if b.isMentioned(c) {
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
