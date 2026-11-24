package igdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

var testPlatforms = []Platform{
	{ID: 8, Name: "PlayStation 2", Slug: "ps2", URL: "https://www.igdb.com/platforms/ps2", Abbreviation: "PS2"},
	{ID: 41, Name: "Wii U", Slug: "wiiu", URL: "https://www.igdb.com/platforms/wiiu", Abbreviation: "WiiU"},
	{ID: 130, Name: "Nintendo Switch", Slug: "switch", URL: "https://www.igdb.com/platforms/switch", Abbreviation: "Switch"},
	{ID: 21, Name: "Nintendo GameCube", Slug: "ngc", URL: "https://www.igdb.com/platforms/ngc", Abbreviation: "NGC"},
	{ID: 29, Name: "Sega Mega Drive/Genesis", Slug: "genesis-slash-megadrive", Abbreviation: "Genesis/MegaDrive"},
	{ID: 52, Name: "Arcade", Slug: "arcade", URL: "https://www.igdb.com/platforms/arcade"},
	{ID: 6, Name: "PC (Microsoft Windows)", Slug: "win", URL: "https://www.igdb.com/platforms/win", Abbreviation: "PC"},
	{ID: 13, Name: "DOS", Slug: "dos", URL: "https://www.igdb.com/platforms/dos", Abbreviation: "DOS"},
	{ID: 9, Name: "PlayStation 3", Slug: "ps3", URL: "https://www.igdb.com/platforms/ps3", Abbreviation: "PS3"},
	{ID: 12, Name: "Xbox 360", Slug: "xbox360", URL: "https://www.igdb.com/platforms/xbox360", Abbreviation: "X360"},
	{ID: 49, Name: "Xbox One", Slug: "xboxone", Abbreviation: "XONE"},
	{ID: 169, Name: "Xbox Series X|S", Slug: "series-x-s", Abbreviation: "Series X|S"},
}

func TestMatchSystem(t *testing.T) {
	for _, tc := range []struct {
		system string
		ids    []int
		single bool
	}{
		{"PlayStation 2", []int{8}, true},
		{"ps2", []int{8}, true},
		{"GameCube", []int{21}, true},
		{"Sega Genesis / Mega Drive", []int{29}, true},
		{"Arcade (CPS-2)", []int{52}, true},
		{"MS-DOS / Windows", []int{13, 6}, true},
		{"Xbox Series X/S", []int{169}, true},
		{"Windows, PlayStation 3, Xbox 360", []int{6, 9, 12}, false},
		{"Amiga", nil, false},
	} {
		plats, single := matchSystem(testPlatforms, tc.system)
		var ids []int
		for _, p := range plats {
			ids = append(ids, p.ID)
		}
		if !equalInts(ids, tc.ids) || single != tc.single {
			t.Errorf("matchSystem(%q) = %v, %v; want %v, %v", tc.system, ids, single, tc.ids, tc.single)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeIGDB serves a token endpoint and the platforms and games endpoints.
func fakeIGDB(t *testing.T, games func(query string) []Game) (*Client, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.FormValue("client_id") != "id" || r.FormValue("client_secret") != "secret" {
				http.Error(w, "bad credentials", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Client-ID") != "id" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v4/platforms":
			json.NewEncoder(w).Encode(testPlatforms)
		case "/v4/games":
			json.NewEncoder(w).Encode(games(string(body)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := New("id", "secret")
	c.apiURL, c.tokenURL = srv.URL+"/v4/", srv.URL+"/token"
	return c, &calls
}

func TestGameURL(t *testing.T) {
	c, _ := fakeIGDB(t, func(q string) []Game {
		switch {
		case strings.Contains(q, `search "Super Mario 3D World"`) && strings.Contains(q, "where platforms = (41)"):
			return []Game{{Name: "Super Mario 3D World", Slug: "super-mario-3d-world", URL: "https://www.igdb.com/games/super-mario-3d-world", Platforms: []int{41}}}
		case strings.Contains(q, `search "Super Mario 3D World"`) && strings.Contains(q, "where platforms = (130)"):
			return []Game{{Name: "Super Mario 3D World + Bowser's Fury", Slug: "super-mario-3d-world-plus-bowsers-fury", Platforms: []int{130}}}
		case strings.Contains(q, `search "Unknown Game"`):
			return []Game{{Name: "Unknown Game Deluxe", Slug: "ugd"}}
		}
		return nil
	})
	ctx := context.Background()
	for _, tc := range []struct{ game, system, want string }{
		{"Super Mario 3D World", "Wii U", "https://www.igdb.com/games/super-mario-3d-world"},
		{"Super Mario 3D World", "Nintendo Switch", "https://www.igdb.com/games/super-mario-3d-world-plus-bowsers-fury"},
		// Unknown system: only an exact title is accepted.
		{"Unknown Game", "Amiga", ""},
	} {
		got, err := c.GameURL(ctx, tc.game, tc.system)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("GameURL(%q, %q) = %q, want %q", tc.game, tc.system, got, tc.want)
		}
	}
	got, err := c.PlatformURL(ctx, "PS2")
	if err != nil || got != "https://www.igdb.com/platforms/ps2" {
		t.Errorf("PlatformURL(PS2) = %q, %v", got, err)
	}
}

func TestEscape(t *testing.T) {
	if got := escape("Say \"Hi\"\n\\ now"); got != `Say \"Hi\" \\ now` {
		t.Errorf("escape = %q", got)
	}
}

func TestLinkerCaches(t *testing.T) {
	c, calls := fakeIGDB(t, func(q string) []Game {
		return []Game{{Name: "Street Fighter Alpha Anthology", Slug: "street-fighter-alpha-anthology",
			URL: "https://www.igdb.com/games/street-fighter-alpha-anthology", Platforms: []int{8}}}
	})
	st, err := store.Open(filepath.Join(t.TempDir(), "igdb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	l := NewLinker(c, st, zerolog.Nop())
	ctx := context.Background()
	for range 2 {
		g, s := l.Links(ctx, "Street Fighter Alpha Anthology", "PlayStation 2")
		if g != "https://www.igdb.com/games/street-fighter-alpha-anthology" || s != "https://www.igdb.com/platforms/ps2" {
			t.Fatalf("Links = %q, %q", g, s)
		}
	}
	if n := calls.Load(); n != 2 { // platforms once, games once; the second round is cached
		t.Errorf("IGDB was called %d times, want 2", n)
	}
	links, err := st.IGDBLinks(ctx)
	if err != nil || links[store.IGDBGameKey("Street Fighter Alpha Anthology", "PlayStation 2")] == "" {
		t.Errorf("IGDBLinks = %v, %v", links, err)
	}

	var nilLinker *Linker
	if g, s := nilLinker.Links(ctx, "x", "y"); g != "" || s != "" {
		t.Error("nil linker returned links")
	}
	nilLinker.Kick()
}
