package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/config"
)

func TestAutoAddCommand(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		text   string
		ok     bool
		enable *bool
	}{
		{"stop auto-adding in this room", true, &off},
		{"Stop auto-adding in this room!", true, &off},
		{"disable auto add", true, &off},
		{"start auto-adding in this room", true, &on},
		{"enable  autoadding for this room.", true, &on},
		{"auto-adding status", true, nil},
		{"is auto-adding on?", true, nil},
		{"stop auto-adding Zelda in this room", false, nil},
		{"is there a recomp for Zelda?", false, nil},
	} {
		enable, ok := autoAddCommand(tc.text)
		if ok != tc.ok || (enable == nil) != (tc.enable == nil) || (enable != nil && *enable != *tc.enable) {
			t.Errorf("autoAddCommand(%q) = %v, %v; want %v, %v", tc.text, enable, ok, tc.enable, tc.ok)
		}
	}
}

// The room's state event overrides detect.auto both ways; without one (or when it's
// empty) the config applies, and when it can't be read the bot asks instead.
func TestAutoAddSetting(t *testing.T) {
	var status int
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/state/org.recompbot.auto_add") {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	client, err := mautrix.NewClient(srv.URL, "@bot:x", "token")
	if err != nil {
		t.Fatal(err)
	}
	client.DefaultHTTPRetries = 0
	b := &Bot{cfg: &config.Config{}, client: client, log: zerolog.Nop()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	notFound := `{"errcode":"M_NOT_FOUND","error":"no state"}`
	for _, tc := range []struct {
		cfgAuto      bool
		status       int
		body         string
		want, fromRm bool
	}{
		{false, 404, notFound, false, false},
		{true, 404, notFound, true, false},
		{false, 200, `{"enabled":true}`, true, true},
		{true, 200, `{"enabled":false}`, false, true},
		{true, 200, `{}`, true, false}, // redacted: no override
		{true, 500, `{"errcode":"M_UNKNOWN"}`, false, false},
	} {
		b.cfg.Detect.Auto, status, body = tc.cfgAuto, tc.status, tc.body
		got, fromRoom := b.autoAddSetting(ctx, "!room:x")
		if got != tc.want || fromRoom != tc.fromRm {
			t.Errorf("config %v, state %d %s: got %v (from room %v), want %v (from room %v)",
				tc.cfgAuto, tc.status, tc.body, got, fromRoom, tc.want, tc.fromRm)
		}
	}
}

func TestAutoAddedElsewhere(t *testing.T) {
	b := &Bot{}
	url := "https://github.com/x/a"
	if b.autoAddedElsewhere(url, "!a") {
		t.Error("nothing was auto-added yet")
	}
	b.autoAdded.Store(url, autoAddRecord{room: "!a", at: time.Now()})
	if b.autoAddedElsewhere(url, "!a") || !b.autoAddedElsewhere(url, "!b") {
		t.Error("only other rooms should see it as auto-added elsewhere")
	}
	b.autoAdded.Store(url, autoAddRecord{room: "!a", at: time.Now().Add(-2 * autoAddedWindow)})
	if b.autoAddedElsewhere(url, "!b") {
		t.Error("an old auto-add should get the plain already-listed reaction")
	}
}
